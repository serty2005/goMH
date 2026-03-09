package tui

import "github.com/charmbracelet/lipgloss"

type Theme struct {
	App         lipgloss.Style
	Panel       lipgloss.Style
	PanelFocus  lipgloss.Style
	Title       lipgloss.Style
	Subtitle    lipgloss.Style
	Item        lipgloss.Style
	ItemFocus   lipgloss.Style
	ItemMuted   lipgloss.Style
	Badge       lipgloss.Style
	BadgeMuted  lipgloss.Style
	Status      lipgloss.Style
	Info        lipgloss.Style
	Warning     lipgloss.Style
	Error       lipgloss.Style
	Success     lipgloss.Style
	Key         lipgloss.Style
	Help        lipgloss.Style
	Input       lipgloss.Style
	InputFocus  lipgloss.Style
	Placeholder lipgloss.Style
}

func DefaultTheme() Theme {
	return Theme{
		App: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F7F3E9")).
			Background(lipgloss.Color("#1E2622")),
		Panel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4C5B52")).
			Padding(1, 2),
		PanelFocus: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#D5A021")).
			Padding(1, 2),
		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F7F3E9")),
		Subtitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A5B0A6")),
		Item: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F7F3E9")),
		ItemFocus: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#101512")).
			Background(lipgloss.Color("#D5A021")).
			Bold(true).
			Padding(0, 1),
		ItemMuted: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A5B0A6")),
		Badge: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#102A1E")).
			Background(lipgloss.Color("#9CCF9A")).
			Padding(0, 1),
		BadgeMuted: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10212A")).
			Background(lipgloss.Color("#98B7D8")).
			Padding(0, 1),
		Status: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F7F3E9")).
			Background(lipgloss.Color("#365B4B")).
			Padding(0, 1),
		Info: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#B8D7FF")),
		Warning: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F5C35B")),
		Error: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F17C7C")),
		Success: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#98D39A")),
		Key: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D5A021")).
			Bold(true),
		Help: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#869389")),
		Input: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4C5B52")).
			Padding(0, 1),
		InputFocus: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#D5A021")).
			Padding(0, 1),
		Placeholder: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6F7D73")),
	}
}
