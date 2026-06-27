package networkdiag

import (
	"fmt"
	"goMH/core"
	"goMH/tui"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beevik/etree"
)

const sectionLine = "****************************************************************************"

// Module реализует QueueModule для диагностики сетевого подключения к серверу iiko/Syrve.
type Module struct{}

func (m *Module) ID() string       { return "NetworkDiag" }
func (m *Module) MenuText() string { return "Диагностика сети" }

// Config хранит параметры диагностики.
type Config struct {
	Host string
}

func (cfg *Config) TaskConfirmation() core.TaskConfirmation {
	return core.TaskConfirmation{
		Details:      []string{"Сервер: " + cfg.Host},
		ConfirmLabel: "Запустить диагностику",
	}
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

	slog.Info("Запуск диагностики сети", "host", cfg.Host)
	return m.RunDiag(ctx, wu, cfg)
}

func (m *Module) ConfigureTask(ctx core.TaskContext, _ core.ModuleServices) (any, error) {
	return m.Configure(ctx)
}

func (m *Module) BuildTask(c any) (core.ModuleTaskPlan, error) {
	cfg, err := toConfig(c)
	if err != nil {
		return core.ModuleTaskPlan{}, err
	}
	return core.ModuleTaskPlan{
		Mode: core.ModuleRunModeQueue,
		Task: core.ModuleTaskSpec{
			Title:     "Диагностика сети: " + cfg.Host,
			Signature: "networkdiag|" + cfg.Host,
		},
	}, nil
}

func (m *Module) ExecuteTask(ctx core.TaskContext, services core.ModuleServices, c any) error {
	cfg, err := toConfig(c)
	if err != nil {
		return err
	}
	return m.RunDiag(ctx, services.WinUtils, cfg)
}

// Configure опрашивает пользователя и возвращает конфигурацию.
// Сначала пытается определить адрес из config.xml iiko/Syrve.
func (m *Module) Configure(ctx core.TaskContext) (*Config, error) {
	host, brand := detectServerAddress()

	if host != "" {
		idx, err := tui.SelectItem([]tui.ChoiceItem{
			{Title: host, Description: fmt.Sprintf("Из config.xml (%s)", brand)},
			{Title: "Ввести адрес вручную"},
		}, tui.SelectionConfig{
			Title:            "Диагностика сети",
			Subtitle:         "Выберите адрес сервера для диагностики:",
			DisableShortcuts: true,
		})
		if err != nil {
			return nil, err
		}
		if idx < 0 {
			return nil, nil
		}
		if idx == 0 {
			return &Config{Host: host}, nil
		}
		// idx == 1: пользователь выбрал ручной ввод; host используется как начальное значение
	}

	entered, err := tui.PromptText(tui.InputConfig{
		Title:        "Диагностика сети",
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
	return &Config{Host: h}, nil
}

// RunDiag выполняет все шаги диагностики и сохраняет результат в файл на Рабочем столе.
func (m *Module) RunDiag(ctx core.TaskContext, wu core.WinUtils, cfg *Config) error {
	host := cfg.Host

	desktop, err := wu.GetDesktopDir()
	if err != nil {
		desktop, _ = os.UserHomeDir()
	}

	safeHost := strings.NewReplacer(".", "_", ":", "_", "/", "_", "\\", "_").Replace(host)
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	outputPath := filepath.Join(desktop, fmt.Sprintf("net_diag_%s_%s.log", safeHost, timestamp))

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
		{
			"netsh interface ipv4 show subinterfaces",
			func() string { return runCmd("netsh", "interface", "ipv4", "show", "subinterfaces") },
		},
		{
			"route print",
			func() string { return runCmd("route", "print") },
		},
		{
			"ipconfig /all",
			func() string { return runCmd("ipconfig", "/all") },
		},
		{
			"Настройки прокси-сервера",
			func() string { return wu.CollectProxyInfo() },
		},
		{
			"Состояние TLS в реестре",
			func() string { return wu.CollectTLSInfo() },
		},
		{
			fmt.Sprintf("nslookup %s (DNS-разрешение имени)", host),
			func() string { return runCmd("nslookup", host) },
		},
		{
			fmt.Sprintf("ping %s -n 10", host),
			func() string { return runCmd("ping", host, "-n", "10") },
		},
		{
			fmt.Sprintf("ping %s -f -l 1472 (MTU тест)", host),
			func() string { return runCmd("ping", host, "-f", "-l", "1472") },
		},
		{
			fmt.Sprintf("ping %s -f -l 1462 (MTU тест)", host),
			func() string { return runCmd("ping", host, "-f", "-l", "1462") },
		},
		{
			fmt.Sprintf("ping %s -f -l 1452 (MTU тест)", host),
			func() string { return runCmd("ping", host, "-f", "-l", "1452") },
		},
		{
			fmt.Sprintf("ping %s -f -l 1372 (MTU тест)", host),
			func() string { return runCmd("ping", host, "-f", "-l", "1372") },
		},
		{
			fmt.Sprintf("ping %s -l 65500 (крупный пакет)", host),
			func() string { return runCmd("ping", host, "-l", "65500") },
		},
		{
			"Состояние брандмауэра Windows",
			func() string { return runCmd("netsh", "advfirewall", "show", "allprofiles", "state") },
		},
		{
			fmt.Sprintf("tracert %s", host),
			func() string {
				ctx.Info("Трассировка маршрута — может занять несколько минут...")
				return runCmd("tracert", host)
			},
		},
		{
			fmt.Sprintf("pathping %s", host),
			func() string {
				ctx.Info("pathping — занимает 5–10 минут, пожалуйста, ожидайте...")
				return runCmd("pathping", host)
			},
		},
	}

	total := len(steps)
	for i, s := range steps {
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

// readServerAddressFromXML извлекает hostname сервера из config.xml iiko/Syrve.
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

// normalizeHost убирает схему и путь из URI, возвращая только hostname.
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
