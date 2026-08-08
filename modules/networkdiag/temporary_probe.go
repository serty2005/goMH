package networkdiag

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
)

type ReachabilityResult struct {
	ICMPReachable bool
	OpenTCPPorts  []int
}

func (result ReachabilityResult) Reachable() bool {
	return result.ICMPReachable || len(result.OpenTCPPorts) > 0
}

func probeTarget(ctx context.Context, target, source netip.Addr) ReachabilityResult {
	const timeout = 800 * time.Millisecond
	result := ReachabilityResult{}
	var mutex sync.Mutex
	var waitGroup sync.WaitGroup

	waitGroup.Go(func() {
		scanner, err := newScanner()
		if err != nil {
			return
		}
		defer scanner.close()
		reachable, _ := scanner.ping(target.String(), timeout)
		mutex.Lock()
		result.ICMPReachable = reachable
		mutex.Unlock()
	})

	for _, port := range []int{80, 443, 9100} {
		waitGroup.Go(func() {
			dialer := net.Dialer{Timeout: timeout}
			if source.IsValid() {
				dialer.LocalAddr = &net.TCPAddr{IP: net.IP(source.AsSlice())}
			}
			connection, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", target, port))
			if err != nil {
				return
			}
			connection.Close()
			mutex.Lock()
			result.OpenTCPPorts = append(result.OpenTCPPorts, port)
			mutex.Unlock()
		})
	}
	waitGroup.Wait()
	return result
}
