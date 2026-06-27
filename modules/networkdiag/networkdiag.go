package networkdiag

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"goMH/core"
	"goMH/tui"

	"github.com/beevik/etree"
)

const sectionLine = "****************************************************************************"

// ActionType определяет выбранное действие в подменю диагностики.
type ActionType int

const (
	ActionFullDiag ActionType = iota // полный сбор диагностики
	ActionTLS                        // проверка и настройка TLS
	ActionNetScan                    // сканирование сети
)

// Module реализует QueueModule для диагностики сетевого подключения.
type Module struct{}

func (m *Module) ID() string       { return "NetworkDiag" }
func (m *Module) MenuText() string { return "Диагностика сети" }

// Config хранит параметры выбранного действия.
type Config struct {
	Action ActionType

	// ActionFullDiag
	Host string

	// ActionTLS — ключи TLS-исправлений для применения
	TLSFixes []string

	// ActionNetScan — CIDR-подсети для сканирования
	Subnets []string
}

func (cfg *Config) TaskConfirmation() core.TaskConfirmation {
	switch cfg.Action {
	case ActionFullDiag:
		return core.TaskConfirmation{
			Details:      []string{"Сервер: " + cfg.Host},
			ConfirmLabel: "Запустить диагностику",
		}
	case ActionTLS:
		details := make([]string, 0, len(cfg.TLSFixes)+1)
		details = append(details, "Будут применены исправления TLS:")
		for _, k := range cfg.TLSFixes {
			if lbl, ok := tlsFixLabel[k]; ok {
				details = append(details, "  • "+lbl)
			}
		}
		return core.TaskConfirmation{
			Details:      details,
			ConfirmLabel: "Применить",
		}
	case ActionNetScan:
		return core.TaskConfirmation{
			Details:      append([]string{"Сканируемые подсети:"}, cfg.Subnets...),
			ConfirmLabel: "Запустить сканирование",
		}
	}
	return core.TaskConfirmation{}
}

func toConfig(c any) (*Config, error) {
	cfg, ok := c.(*Config)
	if !ok || cfg == nil {
		return nil, fmt.Errorf("неверный тип конфигурации NetworkDiag")
	}
	return cfg, nil
}

// Run — точка входа для консольного режима (прямой запуск без очереди).
func (m *Module) Run(_ core.AssetManager, wu core.WinUtils) error {
	slog.Info("Запуск модуля NetworkDiag")
	ctx := tui.NewConsoleContext()

	cfg, err := m.Configure(ctx)
	if err != nil {
		slog.Error("Ошибка конфигурации NetworkDiag", "error", err)
		return err
	}
	if cfg == nil {
		slog.Info("Диагностика сети отменена пользователем")
		return nil
	}

	return m.Execute(ctx, wu, cfg)
}

func (m *Module) ConfigureTask(ctx core.TaskContext, _ core.ModuleServices) (any, error) {
	return m.Configure(ctx)
}

func (m *Module) BuildTask(c any) (core.ModuleTaskPlan, error) {
	cfg, err := toConfig(c)
	if err != nil {
		return core.ModuleTaskPlan{}, err
	}

	var title, sig string
	switch cfg.Action {
	case ActionFullDiag:
		title = "Диагностика сети: " + cfg.Host
		sig = "networkdiag|fulldiag|" + cfg.Host
	case ActionTLS:
		title = "Применение настроек TLS"
		sig = "networkdiag|tls|" + strings.Join(cfg.TLSFixes, ",")
	case ActionNetScan:
		title = "Сканирование сети: " + strings.Join(cfg.Subnets, ", ")
		sig = "networkdiag|netscan|" + strings.Join(cfg.Subnets, "|")
	}

	plan := core.ModuleTaskPlan{
		Mode: core.ModuleRunModeQueue,
		Task: core.ModuleTaskSpec{
			Title:     title,
			Signature: sig,
		},
	}
	if cfg.Action == ActionNetScan {
		plan.SkipConfirmation = true
	}
	return plan, nil
}

func (m *Module) ExecuteTask(ctx core.TaskContext, services core.ModuleServices, c any) error {
	cfg, err := toConfig(c)
	if err != nil {
		return err
	}
	return m.Execute(ctx, services.WinUtils, cfg)
}

// Execute — маршрутизатор выполнения по типу действия.
func (m *Module) Execute(ctx core.TaskContext, wu core.WinUtils, cfg *Config) error {
	switch cfg.Action {
	case ActionFullDiag:
		return m.RunDiag(ctx, wu, cfg)
	case ActionTLS:
		return m.RunTLS(ctx, cfg)
	case ActionNetScan:
		return m.RunNetScan(ctx, cfg)
	}
	return fmt.Errorf("неизвестное действие NetworkDiag: %d", cfg.Action)
}

// ── Configure: подменю и делегирование ──────────────────────────────────────

func (m *Module) Configure(ctx core.TaskContext) (*Config, error) {
	idx, err := tui.SelectItem([]tui.ChoiceItem{
		{Title: "Полная диагностика", Description: "Сбор сетевых данных для передачи в поддержку"},
		{Title: "Настройка TLS", Description: "Проверка и исправление SCHANNEL / WinHTTP / .NET"},
		{Title: "Сканирование сети", Description: "Поиск активных устройств в локальных подсетях"},
	}, tui.SelectionConfig{
		Title:            "Диагностика сети",
		Subtitle:         "Выберите действие:",
		DisableShortcuts: true,
	})
	if err != nil {
		return nil, err
	}
	if idx < 0 {
		return nil, nil
	}
	switch idx {
	case 0:
		return m.configureFullDiag(ctx)
	case 1:
		return m.configureTLS(ctx)
	case 2:
		return m.configureNetScan(ctx)
	}
	return nil, nil
}

func (m *Module) configureFullDiag(ctx core.TaskContext) (*Config, error) {
	host, brand := detectServerAddress()

	if host != "" {
		idx, err := tui.SelectItem([]tui.ChoiceItem{
			{Title: host, Description: fmt.Sprintf("Из config.xml (%s)", brand)},
			{Title: "Ввести адрес вручную"},
		}, tui.SelectionConfig{
			Title:            "Диагностика сети — Полная",
			Subtitle:         "Выберите адрес сервера:",
			DisableShortcuts: true,
		})
		if err != nil {
			return nil, err
		}
		if idx < 0 {
			return nil, nil
		}
		if idx == 0 {
			return &Config{Action: ActionFullDiag, Host: host}, nil
		}
	}

	entered, err := tui.PromptText(tui.InputConfig{
		Title:        "Диагностика сети — Полная",
		Subtitle:     "Введите адрес сервера (hostname или IP)",
		Placeholder:  "server.example.com",
		InitialValue: host,
		Validate: func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("адрес не может быть пустым")
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	h := strings.TrimSpace(entered)
	if h == "" {
		return nil, nil
	}
	return &Config{Action: ActionFullDiag, Host: h}, nil
}

func (m *Module) configureTLS(_ core.TaskContext) (*Config, error) {
	items := ReadTLSState()

	choices := make([]tui.ChoiceItem, len(items))
	preSelected := []int{}
	for i, item := range items {
		choices[i] = tui.ChoiceItem{
			Title:       item.Label,
			Description: item.StatusText,
			Disabled:    !item.NeedsChange,
		}
		if item.NeedsChange {
			preSelected = append(preSelected, i)
		}
	}

	if len(preSelected) == 0 {
		// Всё в порядке — показываем статус и выходим
		tui.SelectItem(choices, tui.SelectionConfig{ //nolint:errcheck
			Title:            "Настройка TLS",
			Subtitle:         "Все параметры TLS настроены корректно.",
			DisableShortcuts: true,
		})
		return nil, nil
	}

	indices, err := tui.SelectItems(choices, tui.SelectionConfig{
		Title:            "Настройка TLS",
		Subtitle:         "Выберите параметры для исправления:",
		Multi:            true,
		DisableShortcuts: true,
		PreSelected:      preSelected,
		ConfirmKey:       "s",
	})
	if err != nil || len(indices) == 0 {
		return nil, err
	}

	fixes := make([]string, 0, len(indices))
	for _, idx := range indices {
		fixes = append(fixes, items[idx].Key)
	}
	return &Config{Action: ActionTLS, TLSFixes: fixes}, nil
}

func (m *Module) configureNetScan(_ core.TaskContext) (*Config, error) {
	subnets, err := GetLocalSubnets()
	if err != nil || len(subnets) == 0 {
		// Ручной ввод, если не удалось определить подсети
		entered, err := tui.PromptText(tui.InputConfig{
			Title:       "Сканирование сети",
			Subtitle:    "Не удалось определить подсети автоматически. Введите CIDR (например 192.168.1.0/24):",
			Placeholder: "192.168.1.0/24",
			Validate: func(s string) error {
				_, _, e := net.ParseCIDR(strings.TrimSpace(s))
				return e
			},
		})
		if err != nil || strings.TrimSpace(entered) == "" {
			return nil, err
		}
		return &Config{Action: ActionNetScan, Subnets: []string{strings.TrimSpace(entered)}}, nil
	}

	choices := make([]tui.ChoiceItem, len(subnets))
	preSelected := make([]int, len(subnets))
	for i, s := range subnets {
		choices[i] = tui.ChoiceItem{Title: s}
		preSelected[i] = i
	}

	indices, err := tui.SelectItems(choices, tui.SelectionConfig{
		Title:            "Сканирование сети",
		Subtitle:         "Выберите подсети для сканирования  [Space — отметить]:",
		Multi:            true,
		DisableShortcuts: true,
		PreSelected:      preSelected,
	})
	if err != nil || len(indices) == 0 {
		return nil, err
	}

	selected := make([]string, len(indices))
	for i, idx := range indices {
		selected[i] = subnets[idx]
	}
	return &Config{Action: ActionNetScan, Subnets: selected}, nil
}

// ── RunDiag: полный сбор диагностики ────────────────────────────────────────

func (m *Module) RunDiag(ctx core.TaskContext, wu core.WinUtils, cfg *Config) error {
	host := cfg.Host

	outputDir, err := execDir()
	if err != nil {
		outputDir, _ = os.UserHomeDir()
	}

	safeHost := strings.NewReplacer(".", "_", ":", "_", "/", "_", "\\", "_").Replace(host)
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	outputPath := filepath.Join(outputDir, fmt.Sprintf("net_diag_%s_%s.log", safeHost, timestamp))

	lw := &logWriter{}
	lw.writeHeader(fmt.Sprintf("Диагностика сети для сервера: %s", host))
	lw.writeLine(fmt.Sprintf("Дата и время: %s\n", time.Now().Format("02.01.2006 15:04:05")))

	type step struct {
		title string
		fn    func() string
	}

	runCmd := func(name string, args ...string) string {
		out, cmdErr := wu.RunCommand(name, args...)
		if cmdErr != nil && strings.TrimSpace(out) == "" {
			return fmt.Sprintf("Ошибка выполнения команды: %v\n", cmdErr)
		}
		return out
	}

	steps := []step{
		{"netsh interface ipv4 show subinterfaces",
			func() string { return runCmd("netsh", "interface", "ipv4", "show", "subinterfaces") }},
		{"route print",
			func() string { return runCmd("route", "print") }},
		{"ipconfig /all",
			func() string { return runCmd("ipconfig", "/all") }},
		{"Настройки прокси-сервера",
			func() string { return wu.CollectProxyInfo() }},
		{"Состояние TLS в реестре",
			func() string { return wu.CollectTLSInfo() }},
		{fmt.Sprintf("nslookup %s", host),
			func() string { return runCmd("nslookup", host) }},
		{fmt.Sprintf("ping %s -n 10", host),
			func() string { return runCmd("ping", host, "-n", "10") }},
		{fmt.Sprintf("ping %s -f -l 1472 (MTU тест)", host),
			func() string { return runCmd("ping", host, "-f", "-l", "1472") }},
		{fmt.Sprintf("ping %s -f -l 1462 (MTU тест)", host),
			func() string { return runCmd("ping", host, "-f", "-l", "1462") }},
		{fmt.Sprintf("ping %s -f -l 1452 (MTU тест)", host),
			func() string { return runCmd("ping", host, "-f", "-l", "1452") }},
		{fmt.Sprintf("ping %s -f -l 1372 (MTU тест)", host),
			func() string { return runCmd("ping", host, "-f", "-l", "1372") }},
		{fmt.Sprintf("ping %s -l 65500 (крупный пакет)", host),
			func() string { return runCmd("ping", host, "-l", "65500") }},
		{"Состояние брандмауэра Windows",
			func() string { return runCmd("netsh", "advfirewall", "show", "allprofiles", "state") }},
		{fmt.Sprintf("tracert %s", host), func() string {
			ctx.Info("Трассировка маршрута — может занять несколько минут...")
			return runCmd("tracert", host)
		}},
		{fmt.Sprintf("pathping %s", host), func() string {
			ctx.Info("pathping — занимает 5–10 минут, пожалуйста, ожидайте...")
			return runCmd("pathping", host)
		}},
	}

	slog.Info("Запуск диагностики сети", "host", host, "output", outputPath)
	total := len(steps)
	for i, s := range steps {
		slog.Info("NetworkDiag: шаг", "step", fmt.Sprintf("%d/%d", i+1, total), "title", s.title)
		ctx.SetStatus(fmt.Sprintf("[%d/%d] %s", i+1, total, s.title))
		ctx.SetProgress(i * 100 / total)
		lw.writeSection(i+1, s.title)
		lw.writeLine(s.fn())
	}
	ctx.SetProgress(100)

	if err := os.WriteFile(outputPath, []byte(lw.String()), 0644); err != nil {
		return fmt.Errorf("не удалось сохранить файл диагностики: %w", err)
	}

	ctx.Success("Диагностика завершена. Файл сохранён:\n" + outputPath)
	slog.Info("Диагностика сети завершена", "host", host, "output", outputPath)
	return nil
}

// ── RunTLS: применение настроек TLS ─────────────────────────────────────────

func (m *Module) RunTLS(ctx core.TaskContext, cfg *Config) error {
	slog.Info("Применение настроек TLS", "fixes", cfg.TLSFixes)
	ctx.SetStatus("Запись параметров TLS в реестр...")
	ctx.SetProgress(-1)

	if err := ApplyTLSFixes(cfg.TLSFixes); err != nil {
		return fmt.Errorf("ошибка применения TLS: %w", err)
	}

	ctx.SetProgress(100)
	ctx.Success("Настройки TLS применены. Для вступления изменений в силу может потребоваться перезагрузка.")
	slog.Info("Настройки TLS успешно применены", "fixes", cfg.TLSFixes)
	return nil
}

// ── RunNetScan: сканирование сети ────────────────────────────────────────────

func (m *Module) RunNetScan(ctx core.TaskContext, cfg *Config) error {
	slog.Info("Запуск сканирования сети", "subnets", cfg.Subnets)

	// Подсчёт общего числа адресов
	totalIPs := 0
	for _, cidr := range cfg.Subnets {
		hosts, _ := hostsInCIDR(cidr)
		totalIPs += len(hosts)
	}

	ctx.SetStatus(fmt.Sprintf("Подготовка: %d адресов для проверки...", totalIPs))
	ctx.SetProgress(0)

	var aliveCount int
	results, err := ScanSubnets(ctx.Context(), cfg.Subnets, func(done, total int, latest ScanHost) {
		if latest.Alive {
			aliveCount++
			ctx.Info(fmt.Sprintf("✓  %-18s  %v", latest.IP, latest.Latency.Round(time.Millisecond)))
		}
		ctx.SetStatus(fmt.Sprintf("%s  |  %d/%d проверено  |  найдено: %d", latest.IP, done, total, aliveCount))
		ctx.SetProgress(done * 100 / total)
	})
	if err != nil {
		return err
	}

	var alive []ScanHost
	for _, r := range results {
		if r.Alive {
			alive = append(alive, r)
		}
	}

	// Сохраняем результат в файл рядом с exe
	outputDir, _ := execDir()
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	outputPath := filepath.Join(outputDir, fmt.Sprintf("net_scan_%s.log", timestamp))

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Сканирование сети\n"))
	sb.WriteString(fmt.Sprintf("Дата: %s\n", time.Now().Format("02.01.2006 15:04:05")))
	sb.WriteString(fmt.Sprintf("Подсети: %s\n", strings.Join(cfg.Subnets, ", ")))
	sb.WriteString(fmt.Sprintf("Проверено адресов: %d\n", totalIPs))
	sb.WriteString(fmt.Sprintf("Активных узлов: %d\n\n", len(alive)))
	for _, h := range alive {
		sb.WriteString(fmt.Sprintf("  %-18s  %v\n", h.IP, h.Latency.Round(time.Millisecond)))
		slog.Info("Активный узел", "ip", h.IP, "latency", h.Latency)
	}

	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		slog.Warn("Не удалось сохранить результаты сканирования", "error", err)
	}

	ctx.SetProgress(100)
	msg := fmt.Sprintf("Найдено %d активных узлов из %d", len(alive), totalIPs)
	if err == nil {
		msg += "\nРезультат сохранён: " + outputPath
	}
	ctx.Success(msg)
	slog.Info("Сканирование сети завершено", "alive", len(alive), "total", totalIPs, "output", outputPath)
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

// execDir возвращает директорию исполняемого файла goMH.
func execDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// logWriter накапливает вывод диагностики.
type logWriter struct {
	buf strings.Builder
}

func (lw *logWriter) writeHeader(title string) {
	lw.buf.WriteString(sectionLine + "\n")
	lw.buf.WriteString("* " + title + "\n")
	lw.buf.WriteString(sectionLine + "\n\n")
}

func (lw *logWriter) writeSection(n int, title string) {
	lw.buf.WriteString(sectionLine + "\n")
	lw.buf.WriteString(fmt.Sprintf("* %d. %s\n", n, title))
	lw.buf.WriteString(sectionLine + "\n")
}

func (lw *logWriter) writeLine(s string) {
	lw.buf.WriteString(s)
	if !strings.HasSuffix(s, "\n") {
		lw.buf.WriteByte('\n')
	}
	lw.buf.WriteByte('\n')
}

func (lw *logWriter) String() string { return lw.buf.String() }

// detectServerAddress ищет адрес сервера в config.xml iiko и Syrve.
func detectServerAddress() (host, brand string) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", ""
	}
	for _, c := range []struct{ brand, path string }{
		{"iiko", filepath.Join(configDir, "iiko", "CashServer", "config.xml")},
		{"Syrve", filepath.Join(configDir, "Syrve", "CashServer", "config.xml")},
	} {
		if _, statErr := os.Stat(c.path); os.IsNotExist(statErr) {
			continue
		}
		if h := readServerAddressFromXML(c.path); h != "" {
			return h, c.brand
		}
	}
	return "", ""
}

func readServerAddressFromXML(path string) string {
	doc := etree.NewDocument()
	if err := doc.ReadFromFile(path); err != nil {
		return ""
	}
	root := doc.SelectElement("config")
	if root == nil {
		return ""
	}
	el := root.SelectElement("serverUrl")
	if el == nil {
		return ""
	}
	return normalizeHost(strings.TrimSpace(el.Text()))
}

func normalizeHost(raw string) string {
	for _, scheme := range []string{"https://", "http://"} {
		raw = strings.TrimPrefix(raw, scheme)
	}
	if idx := strings.IndexByte(raw, '/'); idx >= 0 {
		raw = raw[:idx]
	}
	if h, _, err := net.SplitHostPort(raw); err == nil {
		return h
	}
	return raw
}
