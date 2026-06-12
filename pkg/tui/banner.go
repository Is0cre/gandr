package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The entry manifesto. Shown full-screen once per local client
// profile, before the network; acceptance is stored locally and never
// transmitted. Re-viewable from Settings → About/Advanced.
// The wording is fixed — only wrapping may adapt to the terminal.
var manifestoLines = []string{
	"The century began with a promise.",
	"",
	"Connection.",
	"",
	"It ends with a request.",
	"",
	"Identification.",
	"",
	"Show your face.",
	"Show your papers.",
	"Show your age.",
	"Show your history.",
	"Show your friends.",
	"Show your thoughts.",
	"",
	"Every action has an equal and opposite reaction.",
	"",
	"The more they demand to know,",
	"the less they deserve to know.",
	"",
	"The screen asks:",
	"",
	"VERIFY.",
	"",
	"The human being replies:",
	"",
	"NO.",
	"",
	"──────────────────────────────",
	"",
	"Gandr is a network of peers.",
	"",
	"There are no moderators.",
	"There are no algorithms.",
	"There is no authority above your own judgement.",
	"",
	"Your words are yours.",
	"Your reputation is yours.",
	"Your identity is your key.",
	"",
	"Think before you speak.",
	"Remember there are people on the other side of the screen.",
}

// renderGate renders the first-run entry banner (or its read-only
// re-view). Text wraps to the terminal; j/k scrolls when it cannot
// fit; ←/→ pick a button, Enter commits.
func (m *Model) renderGate(w, h int) string {
	textW := min(w-6, 64)
	if textW < 20 {
		textW = 20
	}

	var lines []string
	if h >= len(manifestoLines)+len(headerLines)+8 {
		lines = append(lines, strings.Split(renderLogoArt(w), "\n")...)
		lines = append(lines, "")
	}
	for _, l := range manifestoLines {
		if l == "" {
			lines = append(lines, "")
			continue
		}
		style := sFg2
		switch l {
		case "VERIFY.":
			style = sBright
		case "NO.":
			style = sFg1
		case "Connection.", "Identification.":
			style = sFg1
		}
		if strings.HasPrefix(l, "──") {
			lines = append(lines, lipgloss.PlaceHorizontal(w, lipgloss.Center, sDark.Render(l)))
			continue
		}
		for _, wrapped := range wrapPlain(l, textW) {
			lines = append(lines, lipgloss.PlaceHorizontal(w, lipgloss.Center, style.Render(wrapped)))
		}
	}

	var buttons string
	if m.gateView {
		buttons = lipgloss.PlaceHorizontal(w, lipgloss.Center,
			sFg5.Render("[ press any key to return ]"))
	} else {
		enter := " ENTER AT YOUR OWN RISK "
		gtfo := " GTFO "
		eStyle, gStyle := sFg3, sFg3
		if m.gateBtn == 0 {
			eStyle = sActive
		} else {
			gStyle = sActive
		}
		buttons = lipgloss.PlaceHorizontal(w, lipgloss.Center,
			sFg4.Render("[")+eStyle.Render(enter)+sFg4.Render("]")+
				"    "+
				sFg4.Render("[")+gStyle.Render(gtfo)+sFg4.Render("]"))
	}

	// reserve rows for the buttons and a hint line
	bodyH := h - 3
	if bodyH < 5 {
		bodyH = 5
	}
	maxScroll := len(lines) - bodyH
	if maxScroll < 0 {
		maxScroll = 0
	}
	m.gateScroll = clamp(m.gateScroll, 0, maxScroll)
	visible := lines
	if len(lines) > bodyH {
		visible = lines[m.gateScroll : m.gateScroll+bodyH]
	} else {
		// vertically center short content
		pad := (bodyH - len(lines)) / 2
		visible = append(make([]string, pad), lines...)
		for len(visible) < bodyH {
			visible = append(visible, "")
		}
	}

	hint := ""
	if maxScroll > 0 && !m.gateView {
		hint = lipgloss.PlaceHorizontal(w, lipgloss.Center, sFg5.Render("j/k scroll · ←/→ choose · Enter"))
	}
	return strings.Join(visible, "\n") + "\n" + buttons + "\n" + hint
}
