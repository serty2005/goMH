package networkdiag

import (
	"context"
	"errors"
	"goMH/core"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type temporarySystemFake struct {
	interfaces      []core.NetworkInterfaceInfo
	entries         map[netip.Addr]core.TemporaryIPv4Info
	dadSequences    map[netip.Addr][]core.IPAddressDADState
	created         []core.TemporaryIPv4Request
	deleted         []core.TemporaryIPv4Info
	createErr       error
	watchdogErr     error
	watchdogExists  bool
	watchdogCreates int
	routeLUID       uint64
	routeSource     netip.Addr
}

func (fake *temporarySystemFake) ListNetworkInterfaces() ([]core.NetworkInterfaceInfo, error) {
	return fake.interfaces, nil
}

func (fake *temporarySystemFake) CreateTemporaryIPv4(req core.TemporaryIPv4Request) (core.TemporaryIPv4Info, error) {
	fake.created = append(fake.created, req)
	if fake.createErr != nil {
		return core.TemporaryIPv4Info{}, fake.createErr
	}
	if fake.entries == nil {
		fake.entries = map[netip.Addr]core.TemporaryIPv4Info{}
	}
	info := core.TemporaryIPv4Info{
		InterfaceLUID:     req.InterfaceLUID,
		InterfaceIndex:    req.InterfaceIndex,
		Address:           req.Address,
		PrefixLength:      req.PrefixLength,
		DADState:          core.IPAddressDADTentative,
		ValidLifetime:     req.ValidLifetime,
		PreferredLifetime: req.PreferredLifetime,
		CreationTimestamp: int64(len(fake.created)),
	}
	fake.entries[req.Address] = info
	return info, nil
}

func (fake *temporarySystemFake) GetTemporaryIPv4(_ uint64, _ uint32, address netip.Addr) (core.TemporaryIPv4Info, error) {
	info, ok := fake.entries[address]
	if !ok {
		return core.TemporaryIPv4Info{}, os.ErrNotExist
	}
	if states := fake.dadSequences[address]; len(states) > 0 {
		info.DADState = states[0]
		fake.dadSequences[address] = states[1:]
		fake.entries[address] = info
	}
	return info, nil
}

func (fake *temporarySystemFake) SetTemporaryIPv4Lifetimes(info core.TemporaryIPv4Info, valid, preferred time.Duration) (core.TemporaryIPv4Info, error) {
	info.ValidLifetime = valid
	info.PreferredLifetime = preferred
	fake.entries[info.Address] = info
	return info, nil
}

func (fake *temporarySystemFake) DeleteTemporaryIPv4(info core.TemporaryIPv4Info) error {
	fake.deleted = append(fake.deleted, info)
	delete(fake.entries, info.Address)
	return nil
}

func (fake *temporarySystemFake) GetBestRouteIPv4(luid uint64, index uint32, source, _ netip.Addr) (core.IPv4RouteInfo, error) {
	if fake.routeLUID != 0 {
		luid = fake.routeLUID
	}
	if fake.routeSource.IsValid() {
		source = fake.routeSource
	}
	return core.IPv4RouteInfo{InterfaceLUID: luid, InterfaceIndex: index, SourceAddress: source}, nil
}

func (fake *temporarySystemFake) CreateOneShotScheduledTask(_ string, _ string, _ []string, _ string, _ time.Time) error {
	fake.watchdogCreates++
	if fake.watchdogErr != nil {
		return fake.watchdogErr
	}
	fake.watchdogExists = true
	return nil
}

func (fake *temporarySystemFake) ScheduledTaskExists(_ string) (bool, error) {
	return fake.watchdogExists, nil
}

func (fake *temporarySystemFake) DeleteScheduledTaskByName(_ string) error {
	fake.watchdogExists = false
	return nil
}

func (fake *temporarySystemFake) OpenURL(_ string) error { return nil }

func testTemporaryConfig(t *testing.T) TemporaryAccessConfig {
	t.Helper()
	address := netip.MustParseAddr("192.168.10.57")
	stateDir := filepath.Join(t.TempDir(), transactionDirName)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return TemporaryAccessConfig{
		Interface: core.NetworkInterfaceInfo{
			LUID:        42,
			GUID:        "test-guid",
			Index:       7,
			Alias:       "Ethernet",
			Up:          true,
			DHCPEnabled: true,
			IPv4Addresses: []core.NetworkIPv4Address{{
				Address:      address,
				PrefixLength: 24,
			}},
		},
		TargetIP:        netip.MustParseAddr("192.168.0.100"),
		PreferredIP:     netip.MustParseAddr("192.168.0.101"),
		TTL:             time.Minute,
		StateDir:        stateDir,
		ExecutablePath:  `C:\MH\goMH.exe`,
		DADTimeout:      20 * time.Millisecond,
		DADPollInterval: time.Millisecond,
		Probe: func(context.Context, netip.Addr, netip.Addr) ReachabilityResult {
			return ReachabilityResult{OpenTCPPorts: []int{80}}
		},
	}
}

func TestStartTemporaryAccessHappyPathAndIdempotentCleanup(t *testing.T) {
	cfg := testTemporaryConfig(t)
	fake := &temporarySystemFake{
		interfaces:   []core.NetworkInterfaceInfo{cfg.Interface},
		dadSequences: map[netip.Addr][]core.IPAddressDADState{cfg.PreferredIP: {core.IPAddressDADPreferred}},
	}
	ctx := core.NewSilentTaskContext(t.Context())
	session, err := StartTemporaryAccess(ctx, fake, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !fake.watchdogExists || len(fake.created) != 1 || session.Transaction.State != transactionActive {
		t.Fatalf("unexpected active state: watchdog=%v created=%d state=%s", fake.watchdogExists, len(fake.created), session.Transaction.State)
	}
	if err := CleanupTemporaryAccess(ctx, fake, session.TransactionPath, "test"); err != nil {
		t.Fatal(err)
	}
	if err := CleanupTemporaryAccess(ctx, fake, session.TransactionPath, "test_again"); err != nil {
		t.Fatal(err)
	}
	if fake.watchdogExists || len(fake.deleted) != 1 {
		t.Fatalf("cleanup result: watchdog=%v deletes=%d", fake.watchdogExists, len(fake.deleted))
	}
	if _, err := os.Stat(cfg.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transaction directory still exists after cleanup: %v", err)
	}
}

func TestStartTemporaryAccessRetriesAfterDADDuplicate(t *testing.T) {
	cfg := testTemporaryConfig(t)
	second := netip.MustParseAddr("192.168.0.102")
	fake := &temporarySystemFake{dadSequences: map[netip.Addr][]core.IPAddressDADState{
		cfg.PreferredIP: {core.IPAddressDADDuplicate},
		second:          {core.IPAddressDADPreferred},
	}}
	session, err := StartTemporaryAccess(core.NewSilentTaskContext(t.Context()), fake, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if session.Transaction.TemporaryIP != second || len(fake.created) != 2 || len(fake.deleted) != 1 {
		t.Fatalf("duplicate retry: ip=%s creates=%d deletes=%d", session.Transaction.TemporaryIP, len(fake.created), len(fake.deleted))
	}
}

func TestStartTemporaryAccessDoesNotMutateWhenWatchdogFails(t *testing.T) {
	cfg := testTemporaryConfig(t)
	fake := &temporarySystemFake{watchdogErr: errors.New("scheduler unavailable")}
	_, err := StartTemporaryAccess(core.NewSilentTaskContext(t.Context()), fake, cfg)
	if err == nil || len(fake.created) != 0 {
		t.Fatalf("err=%v created=%d", err, len(fake.created))
	}
	if _, statErr := os.Stat(cfg.StateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("transaction directory remains after watchdog failure: %v", statErr)
	}
}

func TestStartTemporaryAccessDisarmsWatchdogOnCreateError(t *testing.T) {
	cfg := testTemporaryConfig(t)
	fake := &temporarySystemFake{createErr: errors.New("CreateUnicastIpAddressEntry failed")}
	_, err := StartTemporaryAccess(core.NewSilentTaskContext(t.Context()), fake, cfg)
	if err == nil {
		t.Fatal("expected create error")
	}
	if fake.watchdogExists || len(fake.created) != 1 || len(fake.deleted) != 0 {
		t.Fatalf("watchdog=%v creates=%d deletes=%d", fake.watchdogExists, len(fake.created), len(fake.deleted))
	}
	if _, statErr := os.Stat(cfg.StateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("transaction directory remains after create failure: %v", statErr)
	}
}

func TestStartTemporaryAccessCleansUpOnDADTimeout(t *testing.T) {
	cfg := testTemporaryConfig(t)
	cfg.DADTimeout = 3 * time.Millisecond
	fake := &temporarySystemFake{}
	_, err := StartTemporaryAccess(core.NewSilentTaskContext(t.Context()), fake, cfg)
	if err == nil {
		t.Fatal("expected DAD timeout")
	}
	if len(fake.deleted) != 1 || fake.watchdogExists {
		t.Fatalf("cleanup after timeout: deletes=%d watchdog=%v", len(fake.deleted), fake.watchdogExists)
	}
}

func TestStartTemporaryAccessCleansUpOnRouteMismatch(t *testing.T) {
	cfg := testTemporaryConfig(t)
	fake := &temporarySystemFake{
		dadSequences: map[netip.Addr][]core.IPAddressDADState{cfg.PreferredIP: {core.IPAddressDADPreferred}},
		routeLUID:    99,
	}
	_, err := StartTemporaryAccess(core.NewSilentTaskContext(t.Context()), fake, cfg)
	if err == nil || len(fake.deleted) != 1 {
		t.Fatalf("err=%v deletes=%d", err, len(fake.deleted))
	}
}

func TestStartTemporaryAccessRejectsExistingTransactionOnInterface(t *testing.T) {
	cfg := testTemporaryConfig(t)
	transaction := temporaryAccessTransaction{
		Version:        1,
		ID:             "existing",
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(time.Minute),
		State:          transactionActive,
		InterfaceLUID:  cfg.Interface.LUID,
		InterfaceIndex: cfg.Interface.Index,
		TargetIP:       cfg.TargetIP,
		TemporaryIP:    cfg.PreferredIP,
		PrefixLength:   24,
	}
	if err := saveTransaction(filepath.Join(cfg.StateDir, "existing.json"), &transaction); err != nil {
		t.Fatal(err)
	}
	fake := &temporarySystemFake{}
	if _, err := StartTemporaryAccess(core.NewSilentTaskContext(t.Context()), fake, cfg); err == nil {
		t.Fatal("expected conflicting transaction error")
	}
	if len(fake.created) != 0 {
		t.Fatal("network was mutated despite conflict")
	}
}

func TestStartTemporaryAccessCancellationAfterMutationCleansUp(t *testing.T) {
	cfg := testTemporaryConfig(t)
	ctx, cancel := context.WithCancel(t.Context())
	cfg.Probe = func(context.Context, netip.Addr, netip.Addr) ReachabilityResult {
		cancel()
		return ReachabilityResult{}
	}
	fake := &temporarySystemFake{dadSequences: map[netip.Addr][]core.IPAddressDADState{
		cfg.PreferredIP: {core.IPAddressDADPreferred},
	}}
	_, err := StartTemporaryAccess(core.NewSilentTaskContext(ctx), fake, cfg)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(fake.deleted) != 1 || fake.watchdogExists {
		t.Fatalf("deletes=%d watchdog=%v", len(fake.deleted), fake.watchdogExists)
	}
}

func TestCleanupRefusesAddressWithoutSavedOwnershipTimestamp(t *testing.T) {
	cfg := testTemporaryConfig(t)
	transaction := temporaryAccessTransaction{
		Version:        1,
		ID:             "crash-window",
		CreatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(time.Minute),
		State:          transactionCreating,
		InterfaceLUID:  cfg.Interface.LUID,
		InterfaceIndex: cfg.Interface.Index,
		TargetIP:       cfg.TargetIP,
		TemporaryIP:    cfg.PreferredIP,
		PrefixLength:   24,
		WatchdogTask:   "test-watchdog",
	}
	path := filepath.Join(cfg.StateDir, "crash-window.json")
	if err := saveTransaction(path, &transaction); err != nil {
		t.Fatal(err)
	}
	fake := &temporarySystemFake{entries: map[netip.Addr]core.TemporaryIPv4Info{
		cfg.PreferredIP: {
			InterfaceLUID:     cfg.Interface.LUID,
			InterfaceIndex:    cfg.Interface.Index,
			Address:           cfg.PreferredIP,
			PrefixLength:      24,
			CreationTimestamp: 123,
		},
	}}
	if err := CleanupTemporaryAccess(core.NewSilentTaskContext(t.Context()), fake, path, "watchdog"); err == nil {
		t.Fatal("expected fail-safe ownership error")
	}
	if len(fake.deleted) != 0 {
		t.Fatal("address without saved ownership was deleted")
	}
}

func TestCleanupKeepsDirectoryWhileAnotherTransactionExists(t *testing.T) {
	cfg := testTemporaryConfig(t)
	fake := &temporarySystemFake{
		interfaces:   []core.NetworkInterfaceInfo{cfg.Interface},
		dadSequences: map[netip.Addr][]core.IPAddressDADState{cfg.PreferredIP: {core.IPAddressDADPreferred}},
	}
	session, err := StartTemporaryAccess(core.NewSilentTaskContext(t.Context()), fake, cfg)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(cfg.StateDir, "keep.json")
	if err := os.WriteFile(marker, []byte("diagnostic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupTemporaryAccess(core.NewSilentTaskContext(t.Context()), fake, session.TransactionPath, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
	if _, err := os.Stat(session.TransactionPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed transaction was not removed: %v", err)
	}
}

func TestListAndResumeActiveTemporaryTransaction(t *testing.T) {
	cfg := testTemporaryConfig(t)
	fake := &temporarySystemFake{
		interfaces:   []core.NetworkInterfaceInfo{cfg.Interface},
		dadSequences: map[netip.Addr][]core.IPAddressDADState{cfg.PreferredIP: {core.IPAddressDADPreferred}},
	}
	session, err := StartTemporaryAccess(core.NewSilentTaskContext(t.Context()), fake, cfg)
	if err != nil {
		t.Fatal(err)
	}
	active, err := listActiveTemporaryTransactions(cfg.StateDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].path != session.TransactionPath {
		t.Fatalf("unexpected active transactions: %#v", active)
	}
	resumed, err := ResumeTemporaryAccess(t.Context(), fake, session.TransactionPath)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Transaction.ID != session.Transaction.ID || resumed.Transaction.TemporaryIP != cfg.PreferredIP {
		t.Fatalf("unexpected resumed session: %#v", resumed)
	}
}

func TestResumeRejectsEntryWithDifferentCreationTimestamp(t *testing.T) {
	cfg := testTemporaryConfig(t)
	fake := &temporarySystemFake{
		dadSequences: map[netip.Addr][]core.IPAddressDADState{cfg.PreferredIP: {core.IPAddressDADPreferred}},
	}
	session, err := StartTemporaryAccess(core.NewSilentTaskContext(t.Context()), fake, cfg)
	if err != nil {
		t.Fatal(err)
	}
	entry := fake.entries[cfg.PreferredIP]
	entry.CreationTimestamp++
	fake.entries[cfg.PreferredIP] = entry
	if _, err := ResumeTemporaryAccess(t.Context(), fake, session.TransactionPath); err == nil {
		t.Fatal("expected ownership verification error")
	}
}
