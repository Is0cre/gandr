// Package tui is the gandr terminal client. There is no other
// interface; this is the product. The aesthetic is BBS, 1990–1995:
// phosphor on black, box-drawing characters, ANSI art. Keyboard first,
// 256-color with 16-color degradation, works over SSH, 80x24 native,
// usable at 40x20.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/gandr-net/gandr/pkg/proto"
)

// Styles, rebuilt from the active theme by applyTheme (theme.go).
// Rendering code uses only these — never raw colors.
var (
	sFg1    lipgloss.Style // brightest — you, active
	sFg2    lipgloss.Style // message body
	sFg3    lipgloss.Style // secondary
	sFg4    lipgloss.Style // dim, hints
	sFg5    lipgloss.Style // very dim — timestamps
	sDark   lipgloss.Style // borders
	sDark2  lipgloss.Style // subtle dividers
	sAccent lipgloss.Style // vouched, special
	sBright lipgloss.Style // art highlights

	sActive lipgloss.Style
	sYou    lipgloss.Style

	colorBGActive  lipgloss.Color
	colorGreenDark lipgloss.Color
)

func init() { applyTheme(themes[0]) }

// trustScore maps the protocol's discrete trust levels onto the 0–1
// display scale the badge and bar thresholds use. These are display
// values for real levels, not measurements.
func trustScore(level uint8) float64 {
	switch level {
	case proto.TrustVouched:
		return 0.80
	case proto.TrustTrusted:
		return 0.45
	case proto.TrustNeutral:
		return 0.20
	default:
		return 0.05
	}
}

// trustColor picks the semantic color for a score.
func trustColor(score float64) lipgloss.Color {
	switch {
	case score >= 0.6:
		return theme.Accent
	case score >= 0.3:
		return theme.Fg2
	case score >= 0.1:
		return theme.Fg3
	default:
		return theme.Fg5
	}
}

// trustBadge renders the inline trust badge for a score.
func trustBadge(score float64, isYou bool) string {
	if isYou {
		return sFg1.Render("[you]")
	}
	style := lipgloss.NewStyle().Foreground(trustColor(score))
	switch {
	case score >= 0.6:
		return style.Render("[vouched]")
	case score >= 0.3:
		return style.Render("[trusted]")
	case score >= 0.1:
		return style.Render("[neutral]")
	default:
		return style.Render(fmt.Sprintf("[%.2f·new]", score))
	}
}

// trustBar renders a pip bar: filled Fg2, empty Dark2.
func trustBar(score float64, width int) string {
	if width <= 0 {
		return ""
	}
	filled := int(score*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	return sFg2.Render(strings.Repeat("█", filled)) +
		sDark2.Render(strings.Repeat("░", width-filled))
}

// styleBody renders message content: @mentions accented, `inline code`
// bright, everything else body color.
func styleBody(content string) string {
	var out strings.Builder
	segments := strings.Split(content, "`")
	for i, seg := range segments {
		if i%2 == 1 {
			out.WriteString(sBright.Render(seg))
			continue
		}
		words := strings.Split(seg, " ")
		for j, w := range words {
			if j > 0 {
				out.WriteString(sFg2.Render(" "))
			}
			if strings.HasPrefix(w, "@") && len(w) > 1 {
				out.WriteString(sAccent.Render(w))
			} else {
				out.WriteString(sFg2.Render(w))
			}
		}
	}
	return out.String()
}

// divider renders a labelled horizontal rule:
// ── label ──────────────
func divider(label string, width int) string {
	prefix := "── "
	if label != "" {
		prefix += label + " "
	}
	fill := width - lipgloss.Width(prefix)
	if fill < 0 {
		fill = 0
	}
	return sDark.Render(prefix + strings.Repeat("─", fill))
}

// padRight pads or truncates s (style-free input) to exactly w cells.
func padRight(s string, w int) string {
	d := w - lipgloss.Width(s)
	if d > 0 {
		return s + strings.Repeat(" ", d)
	}
	r := []rune(s)
	if len(r) > w {
		if w < 1 {
			return ""
		}
		return string(r[:w-1]) + "…"
	}
	return s
}
