package tui

import "github.com/charmbracelet/lipgloss"

// Theme is the complete color vocabulary of the UI. Every style in the
// client is derived from one of these slots; nothing else hardcodes a
// color. Themes are a purely local display choice — the selection is
// stored in the encrypted client database and never transmitted.
type Theme struct {
	Name string

	Fg1 lipgloss.Color // brightest — you, active selection
	Fg2 lipgloss.Color // message body
	Fg3 lipgloss.Color // secondary text
	Fg4 lipgloss.Color // dim — hints, labels
	Fg5 lipgloss.Color // very dim — timestamps
	// Dark and Dark2 are border/divider shades, dimmer than any text.
	Dark  lipgloss.Color
	Dark2 lipgloss.Color

	Accent   lipgloss.Color // highlights: vouched, @mentions, the ᚷ rune
	Bright   lipgloss.Color // art highlights, inline code
	BGActive lipgloss.Color // background of the active selection
}

// Built-in themes, in display order. Classic is the default and the
// project's identity; the rest are for operators who stare at this
// all day.
var themes = []Theme{
	{
		Name: "classic", // black bg, green phosphor, amber highlights
		Fg1:  "#00ff41", Fg2: "#00cc33", Fg3: "#009926",
		Fg4: "#006619", Fg5: "#004d14",
		Dark: "#003300", Dark2: "#002200",
		Accent: "#ffb000", Bright: "#66ff88", BGActive: "#001a00",
	},
	{
		Name: "midnight", // near-black blue bg, white fg, cyan highlights
		Fg1:  "#ffffff", Fg2: "#d8e0f0", Fg3: "#9aa8c0",
		Fg4: "#5a6b85", Fg5: "#3b4a63",
		Dark: "#1c2840", Dark2: "#131c30",
		Accent: "#00d7ff", Bright: "#9be8ff", BGActive: "#0a1530",
	},
	{
		Name: "paper", // cream bg, black fg, dark red highlights
		Fg1:  "#000000", Fg2: "#1a1a1a", Fg3: "#404040",
		Fg4: "#6e6e6e", Fg5: "#8e8e8e",
		Dark: "#c0b8a8", Dark2: "#d8d0c0",
		Accent: "#8b0000", Bright: "#5c1010", BGActive: "#e8e0d0",
	},
	{
		Name: "ice", // gray/dark bg, white fg, blue highlights
		Fg1:  "#ffffff", Fg2: "#dde4ea", Fg3: "#aab4be",
		Fg4: "#6e7a86", Fg5: "#4d565f",
		Dark: "#2a3138", Dark2: "#1e2429",
		Accent: "#4da6ff", Bright: "#b3d9ff", BGActive: "#16202a",
	},
}

// theme is the active theme. The s* style variables in palette.go are
// rebuilt from it by applyTheme; rendering reads only those.
var theme = themes[0]

// themeByName finds a built-in theme; ok=false leaves the caller on
// the current one.
func themeByName(name string) (Theme, bool) {
	for _, t := range themes {
		if t.Name == name {
			return t, true
		}
	}
	return Theme{}, false
}

// applyTheme switches the active theme and rebuilds every style.
// NO_COLOR and low-color terminals degrade automatically: lipgloss
// resolves these colors through the terminal profile, dropping to
// 16-color or no color as the environment dictates.
func applyTheme(t Theme) {
	theme = t

	sFg1 = lipgloss.NewStyle().Foreground(t.Fg1)
	sFg2 = lipgloss.NewStyle().Foreground(t.Fg2)
	sFg3 = lipgloss.NewStyle().Foreground(t.Fg3)
	sFg4 = lipgloss.NewStyle().Foreground(t.Fg4)
	sFg5 = lipgloss.NewStyle().Foreground(t.Fg5)
	sDark = lipgloss.NewStyle().Foreground(t.Dark)
	sDark2 = lipgloss.NewStyle().Foreground(t.Dark2)
	sAccent = lipgloss.NewStyle().Foreground(t.Accent)
	sBright = lipgloss.NewStyle().Foreground(t.Bright)

	sActive = lipgloss.NewStyle().Foreground(t.Fg1).Background(t.BGActive)
	sYou = sFg1

	colorBGActive = t.BGActive
	colorGreenDark = t.Dark
}
