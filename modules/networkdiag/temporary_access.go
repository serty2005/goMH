package networkdiag

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"goMH/core"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	defaultTemporaryAccessTTL = 30 * time.Minute
	defaultDADTimeout         = 12 * time.Second
	defaultDADPollInterval    = 300 * time.Millisecond
	maxCandidateAttempts      = 253
	transactionDirName        = "network-transactions"
)

var errDADDuplicate = errors.New("Windows DAD обнаружил дублирующийся IPv4")

type temporaryAccessSystem interface {
	ListNetworkInterfaces() ([]core.NetworkInterfaceInfo, error)
	CreateTemporaryIPv4(req core.TemporaryIPv4Request) (core.TemporaryIPv4Info, error)
	GetTemporaryIPv4(interfaceLUID uint64, interfaceIndex uint32, address netip.Addr) (core.TemporaryIPv4Info, error)
	SetTemporaryIPv4Lifetimes(info core.TemporaryIPv4Info, valid, preferred time.Duration) (core.TemporaryIPv4Info, error)
	DeleteTemporaryIPv4(info core.TemporaryIPv4Info) error
	GetBestRouteIPv4(interfaceLUID uint64, interfaceIndex uint32, source, destination netip.Addr) (core.IPv4RouteInfo, error)
	CreateOneShotScheduledTask(taskName, executablePath string, arguments []string, workingDir string, runAt time.Time) error
	ScheduledTaskExists(taskName string) (bool, error)
	DeleteScheduledTaskByName(taskName string) error
	OpenURL(rawURL string) error
}

type transactionState string

const (
	transactionPrepared      transactionState = "prepared"
	transactionCreating      transactionState = "creating"
	transactionActive        transactionState = "active"
	transactionCleaning      transactionState = "cleaning"
	transactionCleanupFailed transactionState = "cleanup_failed"
	transactionComplete      transactionState = "complete"
)

type networkSnapshot struct {
	InterfaceUp bool     `json:"interface_up"`
	DHCPEnabled bool     `json:"dhcp_enabled"`
	Gateways    []string `json:"gateways"`
	DNSServers  []string `json:"dns_servers"`
}

type temporaryAccessTransaction struct {
	Version        int                     `json:"version"`
	ID             string                  `json:"id"`
	CreatedAt      time.Time               `json:"created_at"`
	ExpiresAt      time.Time               `json:"expires_at"`
	State          transactionState        `json:"state"`
	InterfaceLUID  uint64                  `json:"interface_luid"`
	InterfaceGUID  string                  `json:"interface_guid"`
	InterfaceIndex uint32                  `json:"interface_index"`
	InterfaceAlias string                  `json:"interface_alias"`
	TargetIP       netip.Addr              `json:"target_ip"`
	TemporaryIP    netip.Addr              `json:"temporary_ip"`
	PrefixLength   uint8                   `json:"prefix_length"`
	Entry          *core.TemporaryIPv4Info `json:"entry,omitzero"`
	Route          *core.IPv4RouteInfo     `json:"route,omitzero"`
	Reachability   *ReachabilityResult     `json:"reachability,omitzero"`
	WatchdogTask   string                  `json:"watchdog_task"`
	Snapshot       networkSnapshot         `json:"snapshot"`
	CleanupReason  string                  `json:"cleanup_reason,omitzero"`
	CleanupResult  string                  `json:"cleanup_result,omitzero"`
}

type TemporaryAccessConfig struct {
	Interface         core.NetworkInterfaceInfo
	TargetIP          netip.Addr
	PreferredIP       netip.Addr
	ExcludedAddresses []netip.Addr
	TTL               time.Duration
	StateDir          string
	ExecutablePath    string
	DADTimeout        time.Duration
	DADPollInterval   time.Duration
	Probe             func(context.Context, netip.Addr, netip.Addr) ReachabilityResult
}

type TemporaryAccessSession struct {
	TransactionPath string
	Transaction     temporaryAccessTransaction
	Reachability    ReachabilityResult
}

func defaultTransactionDir(rootPath string) string {
	return filepath.Join(rootPath, transactionDirName)
}

func TemporaryTransactionDir(rootPath string) string {
	return defaultTransactionDir(rootPath)
}

func StartTemporaryAccess(ctx core.TaskContext, system temporaryAccessSystem, cfg TemporaryAccessConfig) (*TemporaryAccessSession, error) {
	ctx.SetStatus("Проверка параметров и сетевого интерфейса...")
	if _, err := parseDeviceIPv4(cfg.TargetIP.String()); err != nil {
		return nil, err
	}
	if cfg.TTL <= 0 {
		cfg.TTL = defaultTemporaryAccessTTL
	}
	if cfg.DADTimeout <= 0 {
		cfg.DADTimeout = defaultDADTimeout
	}
	if cfg.DADPollInterval <= 0 {
		cfg.DADPollInterval = defaultDADPollInterval
	}
	if cfg.Probe == nil {
		cfg.Probe = probeTarget
	}
	if cfg.StateDir == "" || cfg.ExecutablePath == "" {
		return nil, errors.New("не заданы state directory или executable для watchdog")
	}
	absoluteStateDir, err := filepath.Abs(cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("абсолютный путь network transaction: %w", err)
	}
	cfg.StateDir = absoluteStateDir
	if cfg.Interface.LUID == 0 || !cfg.Interface.Up {
		return nil, errors.New("выбранный сетевой интерфейс недоступен")
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("создание каталога network transaction: %w", err)
	}
	ctx.SetStatus("Проверка незавершённых сетевых операций...")
	if conflict, err := findInterfaceTransaction(cfg.StateDir, cfg.Interface.LUID); err != nil {
		return nil, err
	} else if conflict != "" {
		return nil, fmt.Errorf("на интерфейсе %s уже существует active transaction: %s", cfg.Interface.Alias, conflict)
	}

	excluded := append(allLocalIPv4(cfg.Interface, nil), cfg.ExcludedAddresses...)
	candidates, err := candidateAddresses(cfg.TargetIP, cfg.PreferredIP, excluded)
	if err != nil {
		return nil, err
	}
	if len(candidates) > maxCandidateAttempts {
		candidates = candidates[:maxCandidateAttempts]
	}

	id, err := newTransactionID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	transaction := temporaryAccessTransaction{
		Version:        1,
		ID:             id,
		CreatedAt:      now,
		ExpiresAt:      now.Add(cfg.TTL),
		State:          transactionPrepared,
		InterfaceLUID:  cfg.Interface.LUID,
		InterfaceGUID:  cfg.Interface.GUID,
		InterfaceIndex: cfg.Interface.Index,
		InterfaceAlias: cfg.Interface.Alias,
		TargetIP:       cfg.TargetIP,
		TemporaryIP:    candidates[0],
		PrefixLength:   temporaryPrefixLength,
		WatchdogTask:   "goMH_TemporaryIPv4_" + id,
		Snapshot:       snapshotInterface(cfg.Interface),
	}
	transactionPath := filepath.Join(cfg.StateDir, id+".json")
	ctx.SetStatus("Сохранение network transaction...")
	if err := saveTransaction(transactionPath, &transaction); err != nil {
		return nil, err
	}

	workingDir := filepath.Dir(cfg.ExecutablePath)
	watchdogArgs := []string{"-internal-network-temp-cleanup", transactionPath}
	ctx.SetStatus("Создание watchdog автоматического удаления...")
	if err := system.CreateOneShotScheduledTask(transaction.WatchdogTask, cfg.ExecutablePath, watchdogArgs, workingDir, transaction.ExpiresAt); err != nil {
		transaction.State = transactionComplete
		transaction.CleanupReason = "watchdog_arm_failed"
		transaction.CleanupResult = err.Error()
		_ = saveTransaction(transactionPath, &transaction)
		return nil, fmt.Errorf("watchdog не создан, сеть не изменена: %w", err)
	}
	logTransaction(&transaction, "temporary IPv4 watchdog armed", "watchdog_task", transaction.WatchdogTask)

	for _, candidate := range candidates {
		if err := ctx.Context().Err(); err != nil {
			_ = disarmPreparedTransaction(system, transactionPath, &transaction, "cancelled_before_create")
			return nil, err
		}
		transaction.State = transactionCreating
		transaction.TemporaryIP = candidate
		transaction.Entry = nil
		if err := saveTransaction(transactionPath, &transaction); err != nil {
			_ = disarmPreparedTransaction(system, transactionPath, &transaction, "transaction_save_failed")
			return nil, err
		}

		ctx.SetStatus("Создание временного IPv4 " + candidate.String())
		entry, err := system.CreateTemporaryIPv4(core.TemporaryIPv4Request{
			InterfaceLUID:     cfg.Interface.LUID,
			InterfaceIndex:    cfg.Interface.Index,
			Address:           candidate,
			PrefixLength:      temporaryPrefixLength,
			ValidLifetime:     cfg.TTL,
			PreferredLifetime: cfg.TTL,
		})
		if err != nil {
			_ = disarmPreparedTransaction(system, transactionPath, &transaction, "create_failed")
			return nil, err
		}
		transaction.Entry = &entry
		if err := saveTransaction(transactionPath, &transaction); err != nil {
			_ = system.DeleteTemporaryIPv4(entry)
			_ = disarmPreparedTransaction(system, transactionPath, &transaction, "entry_save_failed")
			return nil, err
		}

		ctx.SetStatus("Проверка уникальности " + candidate.String() + " через Windows DAD...")
		entry, err = waitForPreferred(ctx.Context(), system, entry, cfg.DADTimeout, cfg.DADPollInterval)
		if errors.Is(err, errDADDuplicate) {
			ctx.Warn(fmt.Sprintf("Адрес %s занят, проверяется следующий кандидат", candidate))
			logTransaction(&transaction, "temporary IPv4 DAD duplicate", "dad", entry.DADState)
			if deleteErr := system.DeleteTemporaryIPv4(entry); deleteErr != nil {
				return nil, fmt.Errorf("DAD duplicate и cleanup кандидата %s завершился ошибкой: %w", candidate, deleteErr)
			}
			continue
		}
		if err != nil {
			cleanupErr := CleanupTemporaryAccess(ctx, system, transactionPath, "dad_failed")
			return nil, errors.Join(err, cleanupErr)
		}
		transaction.Entry = &entry

		ctx.SetStatus("Проверка маршрута до " + cfg.TargetIP.String() + "...")
		route, err := system.GetBestRouteIPv4(cfg.Interface.LUID, cfg.Interface.Index, candidate, cfg.TargetIP)
		if err != nil || route.InterfaceLUID != cfg.Interface.LUID || route.SourceAddress != candidate {
			cleanupErr := CleanupTemporaryAccess(ctx, system, transactionPath, "route_mismatch")
			if err == nil {
				err = fmt.Errorf("Windows выбрала другой маршрут: LUID=%d source=%s", route.InterfaceLUID, route.SourceAddress)
			}
			return nil, errors.Join(err, cleanupErr)
		}
		transaction.Route = &route

		transaction.State = transactionActive
		if err := saveTransaction(transactionPath, &transaction); err != nil {
			cleanupErr := CleanupTemporaryAccess(ctx, system, transactionPath, "active_save_failed")
			return nil, errors.Join(err, cleanupErr)
		}
		ctx.SetStatus("Проверка доступности " + cfg.TargetIP.String() + " по ICMP и TCP...")
		reachability := cfg.Probe(ctx.Context(), cfg.TargetIP, candidate)
		transaction.Reachability = &reachability
		if err := saveTransaction(transactionPath, &transaction); err != nil {
			cleanupErr := CleanupTemporaryAccess(ctx, system, transactionPath, "reachability_save_failed")
			return nil, errors.Join(err, cleanupErr)
		}
		if err := ctx.Context().Err(); err != nil {
			cleanupErr := CleanupTemporaryAccess(ctx, system, transactionPath, "cancelled_after_create")
			return nil, errors.Join(err, cleanupErr)
		}
		logTransaction(&transaction, "temporary IPv4 active", "dad", entry.DADState, "route_source", route.SourceAddress, "reachable", reachability.Reachable())
		return &TemporaryAccessSession{TransactionPath: transactionPath, Transaction: transaction, Reachability: reachability}, nil
	}

	cleanupErr := CleanupTemporaryAccess(ctx, system, transactionPath, "candidate_exhausted")
	return nil, errors.Join(errors.New("не удалось подобрать свободный временный IPv4"), cleanupErr)
}

func CleanupTemporaryAccess(ctx core.TaskContext, system temporaryAccessSystem, transactionPath, reason string) error {
	ctx.SetStatus("Чтение network transaction...")
	transaction, err := loadTransaction(transactionPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if transaction.State == transactionComplete {
		if exists, taskErr := system.ScheduledTaskExists(transaction.WatchdogTask); taskErr == nil && exists {
			return system.DeleteScheduledTaskByName(transaction.WatchdogTask)
		}
		return nil
	}
	transaction.State = transactionCleaning
	transaction.CleanupReason = reason
	if err := saveTransaction(transactionPath, transaction); err != nil {
		return err
	}

	if transaction.Entry == nil {
		ctx.SetStatus("Проверка наличия временного IPv4...")
		_, lookupErr := system.GetTemporaryIPv4(transaction.InterfaceLUID, transaction.InterfaceIndex, transaction.TemporaryIP)
		if lookupErr == nil {
			transaction.State = transactionCleanupFailed
			transaction.CleanupResult = "найден адрес без сохранённого CreationTimeStamp; удаление запрещено"
			_ = saveTransaction(transactionPath, transaction)
			return errors.New(transaction.CleanupResult)
		}
		if !errors.Is(lookupErr, os.ErrNotExist) {
			return lookupErr
		}
	} else {
		ctx.SetStatus("Удаление временного IPv4 " + transaction.TemporaryIP.String() + "...")
		if err := system.DeleteTemporaryIPv4(*transaction.Entry); err != nil {
			transaction.State = transactionCleanupFailed
			transaction.CleanupResult = err.Error()
			_ = saveTransaction(transactionPath, transaction)
			logTransaction(transaction, "temporary IPv4 cleanup failed", "reason", reason, "error", err)
			return err
		}
	}
	ctx.SetStatus("Проверка удаления временного IPv4...")
	if _, err := system.GetTemporaryIPv4(transaction.InterfaceLUID, transaction.InterfaceIndex, transaction.TemporaryIP); err == nil {
		return errors.New("temporary IPv4 всё ещё присутствует после cleanup")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("verification после cleanup: %w", err)
	}

	ctx.SetStatus("Проверка DHCP, gateway, DNS и состояния интерфейса...")
	verifyNetworkSnapshot(ctx, system, transaction)
	ctx.SetStatus("Удаление watchdog task...")
	if exists, taskErr := system.ScheduledTaskExists(transaction.WatchdogTask); taskErr != nil {
		ctx.Warn("Не удалось проверить watchdog task: " + taskErr.Error())
	} else if exists {
		if taskErr := system.DeleteScheduledTaskByName(transaction.WatchdogTask); taskErr != nil {
			ctx.Warn("Temporary IPv4 удалён, но watchdog task не удалена: " + taskErr.Error())
		}
	}
	transaction.State = transactionComplete
	transaction.CleanupResult = "temporary IPv4 отсутствует"
	if err := saveTransaction(transactionPath, transaction); err != nil {
		return err
	}
	logTransaction(transaction, "temporary IPv4 cleanup complete", "reason", reason)
	return nil
}

func ExtendTemporaryAccess(system temporaryAccessSystem, transactionPath, executablePath string, extension time.Duration) (*temporaryAccessTransaction, error) {
	transaction, err := loadTransaction(transactionPath)
	if err != nil {
		return nil, err
	}
	if transaction.State != transactionActive || transaction.Entry == nil {
		return nil, errors.New("продлить можно только активную temporary session")
	}
	if extension <= 0 {
		extension = defaultTemporaryAccessTTL
	}
	newExpiry := time.Now().Add(extension)
	arguments := []string{"-internal-network-temp-cleanup", transactionPath}
	if err := system.CreateOneShotScheduledTask(transaction.WatchdogTask, executablePath, arguments, filepath.Dir(executablePath), newExpiry); err != nil {
		return nil, fmt.Errorf("watchdog не перенесён, lifetime не изменён: %w", err)
	}
	updatedEntry, err := system.SetTemporaryIPv4Lifetimes(*transaction.Entry, extension, extension)
	if err != nil {
		_ = system.CreateOneShotScheduledTask(transaction.WatchdogTask, executablePath, arguments, filepath.Dir(executablePath), transaction.ExpiresAt)
		return nil, err
	}
	transaction.Entry = &updatedEntry
	transaction.ExpiresAt = newExpiry
	if err := saveTransaction(transactionPath, transaction); err != nil {
		return nil, err
	}
	return transaction, nil
}

func RefreshTemporaryAccess(ctx context.Context, system temporaryAccessSystem, transactionPath string) (core.TemporaryIPv4Info, ReachabilityResult, error) {
	transaction, err := loadTransaction(transactionPath)
	if err != nil {
		return core.TemporaryIPv4Info{}, ReachabilityResult{}, err
	}
	entry, err := system.GetTemporaryIPv4(transaction.InterfaceLUID, transaction.InterfaceIndex, transaction.TemporaryIP)
	if err != nil {
		return core.TemporaryIPv4Info{}, ReachabilityResult{}, err
	}
	route, err := system.GetBestRouteIPv4(transaction.InterfaceLUID, transaction.InterfaceIndex, transaction.TemporaryIP, transaction.TargetIP)
	if err != nil {
		return entry, ReachabilityResult{}, err
	}
	if route.InterfaceLUID != transaction.InterfaceLUID || route.SourceAddress != transaction.TemporaryIP {
		return entry, ReachabilityResult{}, fmt.Errorf("маршрут изменился: LUID=%d source=%s", route.InterfaceLUID, route.SourceAddress)
	}
	return entry, probeTarget(ctx, transaction.TargetIP, transaction.TemporaryIP), nil
}

func RecoverTemporaryTransactions(ctx core.TaskContext, system temporaryAccessSystem, stateDir string, now time.Time) error {
	paths, err := filepath.Glob(filepath.Join(stateDir, "*.json"))
	if err != nil {
		return err
	}
	var recoveryErrors []error
	for _, path := range paths {
		transaction, err := loadTransaction(path)
		if err != nil {
			ctx.Warn("Не удалось прочитать network transaction: " + err.Error())
			continue
		}
		if transaction.State == transactionComplete {
			continue
		}
		if !now.Before(transaction.ExpiresAt) {
			slog.Info("Обнаружена expired temporary network transaction", "transaction_id", transaction.ID, "path", path)
			if err := CleanupTemporaryAccess(ctx, system, path, "startup_expired_recovery"); err != nil {
				recoveryErrors = append(recoveryErrors, fmt.Errorf("recovery %s: %w", transaction.ID, err))
			}
			continue
		}
		if transaction.Entry != nil {
			current, lookupErr := system.GetTemporaryIPv4(transaction.InterfaceLUID, transaction.InterfaceIndex, transaction.TemporaryIP)
			if errors.Is(lookupErr, os.ErrNotExist) {
				if err := CleanupTemporaryAccess(ctx, system, path, "startup_address_absent"); err != nil {
					recoveryErrors = append(recoveryErrors, err)
				}
				continue
			}
			if lookupErr != nil || current.CreationTimestamp != transaction.Entry.CreationTimestamp {
				ctx.Warn(fmt.Sprintf("Transaction %s не прошла ownership verification; автоматическое удаление запрещено", transaction.ID))
				continue
			}
		}
		exists, taskErr := system.ScheduledTaskExists(transaction.WatchdogTask)
		if taskErr != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("проверка watchdog %s: %w", transaction.ID, taskErr))
		} else if !exists {
			executablePath, executableErr := os.Executable()
			if executableErr != nil {
				recoveryErrors = append(recoveryErrors, executableErr)
			} else if taskErr := system.CreateOneShotScheduledTask(
				transaction.WatchdogTask,
				executablePath,
				[]string{"-internal-network-temp-cleanup", path},
				filepath.Dir(executablePath),
				transaction.ExpiresAt,
			); taskErr != nil {
				recoveryErrors = append(recoveryErrors, fmt.Errorf("восстановление watchdog %s: %w", transaction.ID, taskErr))
			}
		}
		message := fmt.Sprintf("Активен временный сетевой доступ %s: %s через %s до %s", transaction.ID, transaction.TargetIP, transaction.InterfaceAlias, transaction.ExpiresAt.Format("15:04:05"))
		ctx.Info(message)
		slog.Info("Обнаружена active temporary network transaction", "transaction_id", transaction.ID, "target", transaction.TargetIP, "interface", transaction.InterfaceAlias, "expires_at", transaction.ExpiresAt)
	}
	return errors.Join(recoveryErrors...)
}

func waitForPreferred(ctx context.Context, system temporaryAccessSystem, entry core.TemporaryIPv4Info, timeout, interval time.Duration) (core.TemporaryIPv4Info, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		current, err := system.GetTemporaryIPv4(entry.InterfaceLUID, entry.InterfaceIndex, entry.Address)
		if err != nil {
			return entry, err
		}
		entry = current
		switch current.DADState {
		case core.IPAddressDADPreferred:
			return current, nil
		case core.IPAddressDADDuplicate:
			return current, errDADDuplicate
		case core.IPAddressDADInvalid, core.IPAddressDADDeprecated:
			return current, fmt.Errorf("непригодное DAD state: %d", current.DADState)
		}
		select {
		case <-ctx.Done():
			return entry, ctx.Err()
		case <-deadline.C:
			return entry, fmt.Errorf("таймаут DAD для %s", entry.Address)
		case <-ticker.C:
		}
	}
}

func saveTransaction(path string, transaction *temporaryAccessTransaction) error {
	data, err := json.MarshalIndent(transaction, "", "  ")
	if err != nil {
		return fmt.Errorf("кодирование network transaction: %w", err)
	}
	tempFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("создание temporary transaction file: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if err := tempFile.Chmod(0o600); err != nil {
		tempFile.Close()
		return err
	}
	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("atomic update network transaction: %w", err)
	}
	return nil
}

func loadTransaction(path string) (*temporaryAccessTransaction, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var transaction temporaryAccessTransaction
	if err := json.Unmarshal(data, &transaction); err != nil {
		return nil, fmt.Errorf("чтение network transaction %s: %w", path, err)
	}
	if transaction.Version != 1 || transaction.ID == "" || !transaction.TemporaryIP.Is4() || transaction.InterfaceLUID == 0 {
		return nil, fmt.Errorf("некорректная network transaction: %s", path)
	}
	return &transaction, nil
}

func findInterfaceTransaction(stateDir string, interfaceLUID uint64) (string, error) {
	paths, err := filepath.Glob(filepath.Join(stateDir, "*.json"))
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		transaction, err := loadTransaction(path)
		if err != nil {
			continue
		}
		if transaction.InterfaceLUID == interfaceLUID && transaction.State != transactionComplete {
			return path, nil
		}
	}
	return "", nil
}

func disarmPreparedTransaction(system temporaryAccessSystem, path string, transaction *temporaryAccessTransaction, reason string) error {
	if exists, err := system.ScheduledTaskExists(transaction.WatchdogTask); err == nil && exists {
		_ = system.DeleteScheduledTaskByName(transaction.WatchdogTask)
	}
	transaction.State = transactionComplete
	transaction.CleanupReason = reason
	transaction.CleanupResult = "сеть не изменена"
	return saveTransaction(path, transaction)
}

func snapshotInterface(info core.NetworkInterfaceInfo) networkSnapshot {
	return networkSnapshot{
		InterfaceUp: info.Up,
		DHCPEnabled: info.DHCPEnabled,
		Gateways:    addressesToStrings(info.DefaultGateways),
		DNSServers:  addressesToStrings(info.DNSServers),
	}
}

func verifyNetworkSnapshot(ctx core.TaskContext, system temporaryAccessSystem, transaction *temporaryAccessTransaction) {
	interfaces, err := system.ListNetworkInterfaces()
	if err != nil {
		ctx.Warn("Не удалось проверить исходную конфигурацию: " + err.Error())
		return
	}
	index := slices.IndexFunc(interfaces, func(info core.NetworkInterfaceInfo) bool {
		return info.LUID == transaction.InterfaceLUID || (transaction.InterfaceGUID != "" && info.GUID == transaction.InterfaceGUID)
	})
	if index < 0 {
		ctx.Warn("Интерфейс не найден после cleanup")
		return
	}
	current := interfaces[index]
	if transaction.Snapshot.InterfaceUp && !current.Up {
		ctx.Warn("Интерфейс был Up до операции, но сейчас неактивен; goMH не выполнял reset NIC")
	}
	if transaction.Snapshot.DHCPEnabled != current.DHCPEnabled {
		ctx.Warn("DHCP state отличается от snapshot; goMH не изменял DHCP")
	}
	if !sameStrings(transaction.Snapshot.Gateways, addressesToStrings(current.DefaultGateways)) {
		ctx.Warn("Default gateway отличается от snapshot; goMH не изменял gateway")
	}
	if !sameStrings(transaction.Snapshot.DNSServers, addressesToStrings(current.DNSServers)) {
		ctx.Warn("DNS отличается от snapshot; goMH не изменял DNS")
	}
}

func addressesToStrings(addresses []netip.Addr) []string {
	result := make([]string, len(addresses))
	for index, address := range addresses {
		result[index] = address.String()
	}
	slices.Sort(result)
	return result
}

func sameStrings(left, right []string) bool {
	left = slices.Clone(left)
	right = slices.Clone(right)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}

func allLocalIPv4(primary core.NetworkInterfaceInfo, others []core.NetworkInterfaceInfo) []netip.Addr {
	interfaces := append([]core.NetworkInterfaceInfo{primary}, others...)
	var result []netip.Addr
	for _, info := range interfaces {
		for _, address := range info.IPv4Addresses {
			result = append(result, address.Address)
		}
	}
	return result
}

func newTransactionID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("создание transaction ID: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func logTransaction(transaction *temporaryAccessTransaction, message string, args ...any) {
	base := []any{
		"transaction_id", transaction.ID,
		"interface_luid", transaction.InterfaceLUID,
		"interface_alias", transaction.InterfaceAlias,
		"target", transaction.TargetIP,
		"temporary_ip", transaction.TemporaryIP,
		"expires_at", transaction.ExpiresAt,
	}
	slog.Info(message, append(base, args...)...)
}

func describeReachability(result ReachabilityResult) string {
	parts := make([]string, 0, 2)
	if result.ICMPReachable {
		parts = append(parts, "ICMP")
	}
	if len(result.OpenTCPPorts) > 0 {
		ports := make([]string, len(result.OpenTCPPorts))
		for index, port := range result.OpenTCPPorts {
			ports[index] = fmt.Sprint(port)
		}
		parts = append(parts, "TCP "+strings.Join(ports, ", "))
	}
	if len(parts) == 0 {
		return "маршрут настроен, устройство не ответило на ICMP/TCP"
	}
	return strings.Join(parts, "; ")
}
