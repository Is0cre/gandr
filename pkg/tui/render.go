package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// layout modes by terminal size.
type layoutMode int

const (
	layoutFull    layoutMode = iota // >= 120 cols
	layoutCompact                   // 80–119
	layoutMini                      // 40–79 (cyberdeck)
	layoutNarrow                    // < 40: warning only
)

func (m *Model) layout() layoutMode {
	switch {
	case m.width >= 120:
		return layoutFull
	case m.width >= 80:
		return layoutCompact
	case m.width >= 40:
		return layoutMini
	default:
		return layoutNarrow
	}
}

// uiGeometry records where clickable things were drawn during the
// last View, so mouse events can be mapped back to them.
type uiGeometry struct {
	tabRow   int       // y of the tab line
	tabZones []tabZone // x ranges of each tab on that line
	bodyTop  int       // y of the first body line
	sidebarW int       // 0 when the sidebar is hidden
	sideRows []sideRow // clickable sidebar rows
}

type tabZone struct {
	x0, x1 int
	tab    Tab
}

// sidebar row kinds for mouse hits.
const (
	sideChannel = iota
	sideSealed
	sidePerson
)

type sideRow struct {
	y     int // absolute terminal row
	kind  int
	index int
}

// glyph picks the unicode form or its ASCII fallback.
func glyph(uni, ascii string) string {
	if asciiOnly() {
		return ascii
	}
	return uni
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	w, h := m.width, m.height
	if w <= 0 {
		w, h = 80, 24
	}
	mode := m.layout()
	if mode == layoutNarrow {
		return strings.Join([]string{
			"", sFg3.Render(" terminal too narrow"),
			sFg4.Render(" min 40 columns"),
			sFg5.Render(" current: " + strconv.Itoa(w)),
		}, "\n")
	}

	if m.gateActive {
		return m.renderGate(w, h)
	}

	m.ui = uiGeometry{tabRow: 1}
	header := m.renderHeader(w, mode)
	used := lipgloss.Height(header)
	m.ui.bodyTop = used
	bodyH := h - used
	if bodyH < 3 {
		bodyH = 3
	}

	sidebarW := 0
	if m.showSidebar && mode == layoutFull {
		sidebarW = 20
	} else if m.showSidebar && mode == layoutCompact {
		sidebarW = 14
	}
	m.ui.sidebarW = sidebarW
	contentW := w - sidebarW
	if sidebarW > 0 {
		contentW-- // border column
	}

	content := m.renderContent(contentW, bodyH, mode)
	if m.overlay != OverlayNone {
		content = m.renderOverlay(contentW, bodyH, content)
	}
	if !m.connected {
		content = m.renderConnectionLost(contentW) + "\n" + content
		content = clipHeight(content, bodyH)
	}

	body := content
	if sidebarW > 0 {
		sidebar := m.renderSidebar(sidebarW, bodyH)
		border := strings.TrimSuffix(strings.Repeat(sDark.Render("│")+"\n", bodyH), "\n")
		body = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, border, content)
	}
	return header + "\n" + body
}

// clipHeight truncates rendered output to at most h lines.
func clipHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// renderHeader is the compact post-login header: one identity/status
// line, one tab line, one separator. The big logo never appears here.
func (m *Model) renderHeader(w int, mode layoutMode) string {
	mark := sAccent.Render(glyph("ᚷ", "&"))
	state := sFg1.Render(glyph("● ", "* ") + "connected")
	if !m.connected {
		s := sFg4
		if m.pulse {
			s = sFg5
		}
		state = s.Render(glyph("○ ", "o ") + "offline")
	}

	var left string
	if mode == layoutMini {
		left = " " + mark + " " + state
	} else {
		left = " " + mark + " " + sFg1.Render("GANDR") + sDark.Render(" · ") + state
	}
	right := m.renderNetWidget(mode)
	gap := w - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		gap = 1
	}
	line0 := left + strings.Repeat(" ", gap) + right

	line1 := m.renderTabLine(w, mode)

	if mode == layoutMini {
		return line0 + "\n" + line1
	}
	return line0 + "\n" + line1 + "\n" + sDark2.Render(strings.Repeat("─", w))
}

// renderNetWidget is the compact live network status: traffic rates,
// sparkline, peer count, identity. Totals and counts only.
func (m *Model) renderNetWidget(mode layoutMode) string {
	up := glyph("↑", "^") + fmtRate(m.stats.rateOut)
	down := glyph("↓", "v") + fmtRate(m.stats.rateIn)
	peers := glyph("⬡ ", "peers:") + strconv.Itoa(len(m.peerList))
	dot := sDark.Render(" · ")

	if mode == layoutMini {
		return sFg4.Render(up+" "+down) + dot + sFg3.Render(peers) + " "
	}
	spark := sparkline(m.stats.history)
	if spark != "" {
		spark = " " + sFg5.Render(spark)
	}
	return sFg4.Render(up+" "+down) + spark + dot +
		sFg3.Render(peers) + dot +
		sFg4.Render(m.displayName(m.id.Pubkey())) + " "
}

// tabBadge returns the unread count for a tab.
func (m *Model) tabBadge(t Tab) int {
	switch t {
	case TabMessages:
		total := m.sealedUnread
		for _, n := range m.chatUnread {
			total += n
		}
		return total
	case TabFeed:
		return m.feedUnread
	case TabForum:
		return m.forumUnread
	}
	return 0
}

// renderTabLine renders the one-line tab bar and records click zones.
func (m *Model) renderTabLine(w int, mode layoutMode) string {
	m.ui.tabZones = m.ui.tabZones[:0]
	var b strings.Builder
	b.WriteString(" ")
	x := 1
	for i, spec := range tabSpecs {
		t := Tab(i)
		active := t == m.tab

		// plain form first, for exact zone widths
		var plain string
		if mode == layoutMini {
			plain = tabIcon(t)
		} else {
			plain = fmt.Sprintf("[%d]%s %s", i+1, tabIcon(t), spec.label)
		}
		badge := ""
		if n := m.tabBadge(t); n > 0 {
			badge = strconv.Itoa(n)
		}

		style := sFg4
		if active {
			style = sActive
		}
		styled := style.Render(plain)
		if badge != "" {
			styled += sFg1.Render(badge)
		}

		width := lipgloss.Width(plain) + len(badge)
		m.ui.tabZones = append(m.ui.tabZones, tabZone{x0: x, x1: x + width - 1, tab: t})
		b.WriteString(styled)
		x += width

		sep := "  "
		if mode == layoutMini {
			sep = " "
		}
		if i < len(tabSpecs)-1 {
			b.WriteString(sep)
			x += len(sep)
		}
	}
	return b.String()
}

// renderSidebar renders channels, the sealed inbox entry, people, and
// network sections, recording mouse rows.
func (m *Model) renderSidebar(w, h int) string {
	m.ui.sideRows = m.ui.sideRows[:0]
	y := m.ui.bodyTop
	var b strings.Builder
	line := func(s string) {
		b.WriteString(s + "\n")
		y++
	}
	// letter-spaced headers double their width; fall back when narrow
	header := func(s string) string {
		if len(s)*2 < w {
			return letterSpace(s)
		}
		return s
	}

	line(" " + sFg5.Render(header("CHANNELS")))
	for i, c := range m.channels {
		name := "# " + c.Name
		var l string
		sel := i == m.channelSel && m.tab == TabMessages && m.msgView == msgViewChat
		if sel {
			l = sActive.Render(padRight(" ▶ "+name, w-1))
		} else {
			l = sFg3.Render(padRight("   "+name, w-1))
		}
		if n := m.chatUnread[c.ID]; n > 0 && !sel {
			cnt := strconv.Itoa(n)
			l = sFg3.Render(padRight("   "+name, w-1-lipgloss.Width(cnt))) + sFg1.Render(cnt)
		}
		m.ui.sideRows = append(m.ui.sideRows, sideRow{y: y, kind: sideChannel, index: i})
		line(l)
	}

	sealedLabel := glyph("◈", "*") + " sealed"
	if m.sealedUnread > 0 {
		sealedLabel += " " + strconv.Itoa(m.sealedUnread)
	}
	sealedStyle := sFg3
	if m.tab == TabMessages && m.msgView == msgViewSealed {
		sealedStyle = sActive
	}
	m.ui.sideRows = append(m.ui.sideRows, sideRow{y: y, kind: sideSealed})
	line(" " + sealedStyle.Render(padRight(sealedLabel, w-2)))

	line("")
	line(" " + sFg5.Render(header("PEOPLE")))
	for i, p := range m.people {
		dot := sFg4.Render("○")
		if recentlySeen(p.lastSeen) {
			dot = sFg1.Render("●")
		}
		name := m.displayName(p.pubkey)
		style := sFg3
		if i+1 == m.peopleSel && m.tab == TabPeople {
			style = sFg1
		}
		m.ui.sideRows = append(m.ui.sideRows, sideRow{y: y, kind: sidePerson, index: i})
		line(" " + dot + " " + style.Render(padRight(name, w-4)))
	}

	line("")
	line(" " + sFg5.Render(header("NETWORK")))
	line(" " + sFg5.Render(padRight("you: "+m.displayName(m.id.Pubkey()), w-2)))
	b.WriteString(" " + sFg5.Render("peers: ") + sFg3.Render(strconv.Itoa(len(m.peerList))))
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(b.String())
}

// renderConnectionLost renders the pulsing reconnect banner.
func (m *Model) renderConnectionLost(w int) string {
	style := sFg4
	if m.pulse {
		style = sFg5
	}
	label := fmt.Sprintf("── CONNECTION LOST ── retrying in %ds ── [r]=retry now ──", m.retryIn)
	fill := w - lipgloss.Width(label) - 2
	if fill < 0 {
		fill = 0
	}
	return " " + style.Render(label+strings.Repeat("─", fill))
}

// renderContent dispatches to the active tab's renderer.
func (m *Model) renderContent(w, h int, mode layoutMode) string {
	var out string
	switch m.tab {
	case TabMessages:
		if m.msgView == msgViewSealed {
			out = m.renderSealedInbox(w, h)
		} else {
			out = m.renderChat(w, h, mode)
		}
	case TabPeople:
		out = m.renderPeople(w, h)
	case TabFeed:
		out = m.renderFeed(w, h)
	case TabForum:
		out = m.renderForum(w, h)
	case TabNetwork:
		out = m.renderNetwork(w, h)
	case TabSettings:
		out = m.renderSettings(w, h)
	}
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(out)
}
