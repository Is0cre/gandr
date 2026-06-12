package tui

import (
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// The ANSI art logo. After the redesign it appears only on the entry
// banner, the About section, and the pre-TUI splash — never in the
// main interface, where vertical space belongs to content.
var headerLines = []string{
	`░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░`,
	`                      ᚷ`,
	` ██████╗  █████╗ ███╗   ██╗██████╗ ██████╗`,
	`██╔════╝ ██╔══██╗████╗  ██║██╔══██╗██╔══██╗`,
	`██║  ███╗███████║██╔██╗ ██║██║  ██║██████╔╝`,
	`██║   ██║██╔══██║██║╚██╗██║██║  ██║██╔══██╗`,
	`╚██████╔╝██║  ██║██║ ╚████║██████╔╝██║  ██║`,
	` ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═══╝╚═════╝ ╚═╝  ╚═╝`,
	`              ── a sending ──`,
}

// headerBrightRow is the index in headerLines whose █ characters get
// the brightest shade.
const headerBrightRow = 4

// SplashArt is the colorized logo for the pre-TUI startup splash.
func SplashArt(width int) string { return renderLogoArt(width) }

// renderLogoArt colorizes and centers the full logo block.
func renderLogoArt(width int) string {
	var out []string
	for i, line := range headerLines {
		var b strings.Builder
		for _, r := range line {
			switch r {
			case '░':
				b.WriteString(sDark.Render(string(r)))
			case '▓':
				b.WriteString(sFg4.Render(string(r)))
			case '█':
				if i == headerBrightRow {
					b.WriteString(sBright.Render(string(r)))
				} else {
					b.WriteString(sFg2.Render(string(r)))
				}
			case 'ᚷ':
				b.WriteString(sAccent.Render(string(r)))
			case '╔', '╗', '╚', '╝', '║', '═':
				b.WriteString(sFg4.Render(string(r)))
			case '─':
				b.WriteString(sFg5.Render(string(r)))
			default:
				b.WriteString(sFg5.Render(string(r)))
			}
		}
		out = append(out, lipgloss.PlaceHorizontal(width, lipgloss.Center, b.String()))
	}
	return strings.Join(out, "\n")
}

// Tab metadata: a terminal-safe glyph (no emoji, no Nerd Fonts), an
// ASCII fallback for dumb terminals, and the label.
type tabSpec struct {
	label string
	icon  string // single-cell glyph from the box-drawing/geometric set
	ascii string // fallback marker for non-UTF-8 terminals
}

var tabSpecs = []tabSpec{
	{"MESSAGES", "✉", "[M]"},
	{"PEOPLE", "◉", "[P]"},
	{"FEED", "≡", "[F]"},
	{"FORUM", "▤", "[T]"},
	{"NETWORK", "⬡", "[N]"},
	{"SETTINGS", "⚙", "[S]"},
}

// tabIcon returns the icon for a tab, degraded to ASCII when the
// terminal can't be trusted with Unicode.
func tabIcon(t Tab) string {
	if asciiOnly() {
		return tabSpecs[t].ascii
	}
	return tabSpecs[t].icon
}

var (
	asciiOnce sync.Once
	asciiMode bool
)

// asciiOnly reports whether to avoid non-ASCII glyphs: forced with
// GANDR_ASCII=1, or implied by TERM=dumb or a non-UTF-8 locale.
func asciiOnly() bool {
	asciiOnce.Do(func() {
		if os.Getenv("GANDR_ASCII") == "1" {
			asciiMode = true
			return
		}
		if os.Getenv("TERM") == "dumb" {
			asciiMode = true
			return
		}
		locale := os.Getenv("LC_ALL")
		if locale == "" {
			locale = os.Getenv("LC_CTYPE")
		}
		if locale == "" {
			locale = os.Getenv("LANG")
		}
		if locale != "" {
			l := strings.ToLower(locale)
			asciiMode = !strings.Contains(l, "utf-8") && !strings.Contains(l, "utf8")
		}
	})
	return asciiMode
}

// letterSpace renders "CHAT" as "C H A T".
func letterSpace(s string) string {
	return strings.Join(strings.Split(s, ""), " ")
}
