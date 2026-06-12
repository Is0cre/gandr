package tui

import (
	"encoding/hex"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderOverlay draws the active overlay over the content area. The
// header, status bar, and tabs are never covered.
func (m *Model) renderOverlay(w, h int, under string) string {
	var box string
	switch m.overlay {
	case OverlayHelp:
		box = m.renderHelpBox(w)
	case OverlayQuit:
		box = overlayBox("QUIT", []string{
			sFg2.Render("quit gandr? ") + sFg4.Render("[y/N]"),
		}, w)
	case OverlayNick:
		current := "(none)"
		if n, err := m.db.GetNickname(m.ovTarget); err == nil && n.Name != "" {
			current = n.Name
		}
		box = overlayBox("SET NICKNAME", append([]string{
			sFg5.Render("pubkey: ") + sFg3.Render(hex.EncodeToString(m.ovTarget[:])[:16]+"…"),
			sFg5.Render("current: ") + sFg3.Render(current),
			"",
		}, m.fieldLines()...), w)
	case OverlaySeal:
		mode := sFg5.Render("[^D]=deniable: ") + sFg4.Render("off")
		if m.ovDeniable {
			mode = sFg5.Render("[^D]=deniable: ") + sAccent.Render("ON")
		}
		box = overlayBox("SEALED TO: "+m.displayName(m.ovTarget), append([]string{mode, ""}, m.fieldLines()...), w)
	case OverlayGuestbook:
		box = overlayBox("GUESTBOOK ENTRY FOR "+m.displayName(m.ovTarget), m.fieldLines(), w)
	case OverlayPostNew:
		title := "NEW POST"
		if m.ovTarget != ([32]byte{}) {
			title = "REPLY"
		}
		box = overlayBox(title, m.fieldLines(), w)
	case OverlayThreadNew:
		box = overlayBox("NEW THREAD", m.fieldLines(), w)
	case OverlayProfileEdit:
		box = overlayBox("EDIT PROFILE", m.fieldLines(), w)
	case OverlayPeerConnect:
		box = overlayBox("CONNECT TO PEER", m.fieldLines(), w)
	default:
		return under
	}
	// vertical placement: a third of the way down the content area
	pad := h/3 - lipgloss.Height(box)/2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat("\n", pad) + box
}

// fieldLines renders the overlay's input fields with the active cursor.
func (m *Model) fieldLines() []string {
	labelW := 0
	for _, l := range m.ovLabels {
		if len(l) > labelW {
			labelW = len(l)
		}
	}
	var out []string
	for i, label := range m.ovLabels {
		cursor := ""
		style := sFg3
		if i == m.ovField {
			cursor = sFg1.Render("▋")
			style = sFg1
		}
		out = append(out, sFg5.Render(padRight(label, labelW))+" "+sFg1.Render("❯")+" "+style.Render(m.ovFields[i])+cursor)
	}
	out = append(out, "", sFg5.Render("[Enter]=save  [Esc]=cancel  [Tab]=next field"))
	return out
}

// overlayBox draws a bordered box with a title and content lines.
func overlayBox(title string, lines []string, w int) string {
	boxW := min(w-4, 60)
	if boxW < 24 {
		boxW = 24
	}
	bd := sFg4
	var b []string
	head := "── " + title + " "
	b = append(b, " "+bd.Render("┌"+head+strings.Repeat("─", max(0, boxW-lipgloss.Width(head)-1))+"┐"))
	for _, l := range lines {
		b = append(b, " "+bd.Render("│")+" "+l+strings.Repeat(" ", max(0, boxW-lipgloss.Width(l)-2))+" "+bd.Render("│"))
	}
	b = append(b, " "+bd.Render("└"+strings.Repeat("─", boxW)+"┘"))
	return strings.Join(b, "\n")
}

// renderHelpBox is the full key reference.
func (m *Model) renderHelpBox(w int) string {
	rows := []string{
		sFg3.Render("NAVIGATION") + "                " + sFg3.Render("COMMANDS"),
		sFg4.Render("1-6 / Tab    switch tabs   /join /leave"),
		sFg4.Render("j k g G      scroll        /nick /note"),
		sFg4.Render("\\            sidebar       /block /unblock"),
		sFg4.Render("Esc          back          /trust /connect"),
		sFg4.Render("q            quit          /seal /sealed"),
		"",
		sFg3.Render("ON A MESSAGE OR PERSON") + "      " + sFg3.Render("/profile /people /peers /set"),
		sFg4.Render("n nickname   p profile"),
		sFg4.Render("s seal       r reply"),
		"",
		sFg3.Render("MESSAGES") + ": " + sFg4.Render("i sealed inbox  Esc chat"),
		sFg3.Render("PEOPLE") + ": " + sFg4.Render("Enter profile  b block  e edit own  g guestbook"),
		sFg3.Render("NETWORK") + ": " + sFg4.Render("c connect  v vouch  b untrust"),
		sFg3.Render("SEALED") + ": " + sFg4.Render("Enter open  d deniable reply"),
	}
	return overlayBox("HELP", rows, w)
}
