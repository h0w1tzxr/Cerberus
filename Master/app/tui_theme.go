package master

import "github.com/charmbracelet/lipgloss"

const (
	mochaRosewater = "#f5e0dc"
	mochaFlamingo  = "#f2cdcd"
	mochaPink      = "#f5c2e7"
	mochaMauve     = "#cba6f7"
	mochaRed       = "#f38ba8"
	mochaMaroon    = "#eba0ac"
	mochaPeach     = "#fab387"
	mochaYellow    = "#f9e2af"
	mochaGreen     = "#a6e3a1"
	mochaTeal      = "#94e2d5"
	mochaSky       = "#89dceb"
	mochaSapphire  = "#74c7ec"
	mochaBlue      = "#89b4fa"
	mochaLavender  = "#b4befe"
	mochaText      = "#cdd6f4"
	mochaSubtext1  = "#bac2de"
	mochaSubtext0  = "#a6adc8"
	mochaOverlay2  = "#9399b2"
	mochaOverlay1  = "#7f849c"
	mochaSurface2  = "#585b70"
	mochaSurface1  = "#45475a"
	mochaSurface0  = "#313244"
	mochaBase      = "#1e1e2e"
	mochaMantle    = "#181825"
	mochaCrust     = "#11111b"
)

var tuiStyles = struct {
	screen        lipgloss.Style
	header        lipgloss.Style
	title         lipgloss.Style
	subtle        lipgloss.Style
	muted         lipgloss.Style
	accent        lipgloss.Style
	success       lipgloss.Style
	warn          lipgloss.Style
	danger        lipgloss.Style
	tab           lipgloss.Style
	activeTab     lipgloss.Style
	section       lipgloss.Style
	tableHeader   lipgloss.Style
	tableCell     lipgloss.Style
	command       lipgloss.Style
	commandInput  lipgloss.Style
	commandOutput lipgloss.Style
	footer        lipgloss.Style
	confirm       lipgloss.Style
}{
	screen: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaText)).
		Background(lipgloss.Color(mochaBase)),
	header: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaText)).
		Background(lipgloss.Color(mochaMantle)).
		Padding(0, 1),
	title: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaMauve)).
		Bold(true),
	subtle: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaSubtext0)),
	muted: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaOverlay2)),
	accent: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaSky)).
		Bold(true),
	success: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaGreen)),
	warn: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaYellow)),
	danger: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaRed)),
	tab: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaSubtext0)).
		Background(lipgloss.Color(mochaSurface0)).
		Padding(0, 1),
	activeTab: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaCrust)).
		Background(lipgloss.Color(mochaMauve)).
		Bold(true).
		Padding(0, 1),
	section: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaLavender)).
		Bold(true),
	tableHeader: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaSky)).
		Bold(true),
	tableCell: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaText)),
	command: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaText)).
		Background(lipgloss.Color(mochaMantle)).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTop(true).
		BorderForeground(lipgloss.Color(mochaSurface2)).
		Padding(0, 1),
	commandInput: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaGreen)),
	commandOutput: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaSubtext1)),
	footer: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaSubtext0)).
		Background(lipgloss.Color(mochaCrust)).
		Padding(0, 1),
	confirm: lipgloss.NewStyle().
		Foreground(lipgloss.Color(mochaYellow)).
		Background(lipgloss.Color(mochaSurface0)).
		Padding(0, 1),
}
