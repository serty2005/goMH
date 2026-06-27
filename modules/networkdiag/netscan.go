package networkdiag

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// ScanHost хранит результат проверки одного IP-адреса.
type ScanHost struct {
	IP      string
	Alive   bool
	Latency time.Duration
}

// GetLocalSubnets возвращает IPv4-подсети всех активных сетевых интерфейсов.
// Исключает loopback и слишком большие сети (префикс < 20, т.е. > 4094 хостов).
func GetLocalSubnets() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var subnets []string

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ipNet *net.IPNet
			switch v := addr.(type) {
			case *net.IPNet:
				ipNet = v
			case *net.IPAddr:
				ipNet = &net.IPNet{IP: v.IP, Mask: v.IP.DefaultMask()}
			}
			if ipNet == nil {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil {
				continue
			}
			ones, bits := ipNet.Mask.Size()
			if bits != 32 || ones < 20 {
				// слишком большая сеть — пропускаем
				continue
			}
			networkIP := ip4.Mask(ipNet.Mask)
			cidr := fmt.Sprintf("%s/%d", networkIP.String(), ones)
			if !seen[cidr] {
				seen[cidr] = true
				subnets = append(subnets, cidr)
			}
		}
	}
	return subnets, nil
}

// hostsInCIDR возвращает список хостовых IP-адресов в сети (без адреса сети и broadcast).
func hostsInCIDR(cidr string) ([]net.IP, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	ones, bits := ipNet.Mask.Size()
	// количество хостов = 2^(bits-ones) - 2 (сеть + broadcast)
	count := (1 << (bits - ones)) - 2
	if count <= 0 {
		return nil, nil
	}
	ips := make([]net.IP, 0, count)
	ip := cloneIP(ipNet.IP.To4())
	incrIP(ip) // пропускаем адрес сети
	for i := 0; i < count; i++ {
		ips = append(ips, cloneIP(ip))
		incrIP(ip)
	}
	return ips, nil
}

func cloneIP(ip net.IP) net.IP {
	c := make(net.IP, len(ip))
	copy(c, ip)
	return c
}

func incrIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

// scanner управляет одним сырым ICMP-сокетом для параллельного опроса хостов.
type scanner struct {
	conn    *icmp.PacketConn
	id      uint16
	mu      sync.Mutex
	pending map[uint16]*pingState
	nextSeq uint16
}

type pingState struct {
	result chan struct{}
	sent   time.Time
}

func newScanner() (*scanner, error) {
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, err
	}
	s := &scanner{
		conn:    conn,
		id:      uint16(rand.Int31n(0xFFFF) + 1),
		pending: make(map[uint16]*pingState),
	}
	go s.receiveLoop()
	return s, nil
}

func (s *scanner) close() {
	s.conn.Close()
}

func (s *scanner) receiveLoop() {
	buf := make([]byte, 1500)
	for {
		n, _, err := s.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		msg, err := icmp.ParseMessage(1, buf[:n])
		if err != nil || msg.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := msg.Body.(*icmp.Echo)
		if !ok || echo.ID != int(s.id) {
			continue
		}
		seq := uint16(echo.Seq)
		s.mu.Lock()
		if state, ok := s.pending[seq]; ok {
			close(state.result)
			delete(s.pending, seq)
		}
		s.mu.Unlock()
	}
}

func (s *scanner) ping(ip string, timeout time.Duration) (alive bool, latency time.Duration) {
	s.mu.Lock()
	seq := s.nextSeq
	s.nextSeq++
	state := &pingState{
		result: make(chan struct{}),
		sent:   time.Now(),
	}
	s.pending[seq] = state
	s.mu.Unlock()

	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   int(s.id),
			Seq:  int(seq),
			Data: []byte("goMH"),
		},
	}
	b, err := msg.Marshal(nil)
	if err != nil {
		s.mu.Lock()
		delete(s.pending, seq)
		s.mu.Unlock()
		return false, 0
	}

	sent := time.Now()
	if _, err := s.conn.WriteTo(b, &net.IPAddr{IP: net.ParseIP(ip)}); err != nil {
		s.mu.Lock()
		delete(s.pending, seq)
		s.mu.Unlock()
		return false, 0
	}

	select {
	case <-state.result:
		return true, time.Since(sent)
	case <-time.After(timeout):
		s.mu.Lock()
		delete(s.pending, seq)
		s.mu.Unlock()
		return false, 0
	}
}

// tcpProbe проверяет доступность хоста через TCP-подключение на типичные Windows-порты.
// Используется как запасной вариант, если ICMP-сокет недоступен.
func tcpProbe(ip string, timeout time.Duration) (bool, time.Duration) {
	ports := []int{445, 135, 3389, 80, 443}
	perPort := timeout / time.Duration(len(ports))
	start := time.Now()
	for _, port := range ports {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), perPort)
		if err == nil {
			conn.Close()
			return true, time.Since(start)
		}
	}
	return false, 0
}

// ScanSubnets параллельно опрашивает все хосты в указанных CIDR-подсетях.
// onProgress вызывается после каждой проверки с результатом проверенного хоста.
func ScanSubnets(rCtx context.Context, cidrs []string, onProgress func(done, total int, latest ScanHost)) ([]ScanHost, error) {
	var ips []string
	for _, cidr := range cidrs {
		hosts, err := hostsInCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("ошибка разбора %s: %w", cidr, err)
		}
		for _, h := range hosts {
			ips = append(ips, h.String())
		}
	}
	if len(ips) == 0 {
		return nil, nil
	}

	sc, icmpErr := newScanner()
	useICMP := icmpErr == nil
	if !useICMP {
		// ICMP недоступен — используем TCP-пробы
		sc = nil
	}
	if useICMP {
		defer sc.close()
	}

	const (
		concurrency  = 64
		pingTimeout  = 500 * time.Millisecond
	)

	total := len(ips)
	results := make([]ScanHost, total)
	for i, ip := range ips {
		results[i].IP = ip
	}

	var (
		mu      sync.Mutex
		scanned int
		wg      sync.WaitGroup
		sem     = make(chan struct{}, concurrency)
	)

	for i, ip := range ips {
		select {
		case <-rCtx.Done():
			goto wait
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(idx int, ip string) {
			defer func() { <-sem; wg.Done() }()

			var alive bool
			var lat time.Duration
			if useICMP {
				alive, lat = sc.ping(ip, pingTimeout)
			} else {
				alive, lat = tcpProbe(ip, pingTimeout)
			}

			mu.Lock()
			results[idx].Alive = alive
			results[idx].Latency = lat
			scanned++
			if onProgress != nil {
				onProgress(scanned, total, results[idx])
			}
			mu.Unlock()
		}(i, ip)
	}

wait:
	wg.Wait()
	return results, nil
}
