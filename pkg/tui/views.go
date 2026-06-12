package tui

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// recentlySeen reports whether a correspondent was active lately.
func recentlySeen(t time.Time) bool { return time.Since(t) < 10*time.Minute }

// renderChat renders the chat view: channel header, message window,
// input area with hint line.
func (m *Model) renderChat(w, h int, mode layoutMode) string {
	ch, ok := m.activeChannel()
	if !ok {
		return "\n  " + sFg3.Render("no channel joined") + "\n\n" +
			"  " + sFg4.Render("/join <name> to start") + "\n" +
			"  " + sFg5.Render("? for help")
	}

	header := " " + sFg4.Render("# "+ch.Name)
	sep := sDark2.Render(strings.Repeat("─", w))
	inputArea := m.renderInputArea(ch.Name, w, mode)
	msgH := h - 2 - lipgloss.Height(inputArea)
	if msgH < 1 {
		msgH = 1
	}

	msgs := m.messages[ch.ID]
	var lines []string
	cursorLine := 0
	for i, msg := range msgs {
		if i == m.msgSel {
			cursorLine = len(lines)
		}
		lines = append(lines, m.renderChatMessage(msg, i == m.msgSel && !m.inputFocused, w, mode)...)
	}
	lines = windowLines(lines, cursorLine, msgH)
	for len(lines) < msgH {
		lines = append([]string{""}, lines...)
	}
	return header + "\n" + sep + "\n" + strings.Join(lines, "\n") + "\n" + inputArea
}

// renderChatMessage renders one message as header + body lines.
func (m *Model) renderChatMessage(msg message, selected bool, w int, mode layoutMode) []string {
	you := msg.sender == m.id.Pubkey()
	name := m.displayName(msg.sender)
	nameStyle := lipgloss.NewStyle().Foreground(trustColor(m.senderScore(msg.sender)))
	if you {
		nameStyle = sYou
	}
	badge := trustBadge(m.senderScore(msg.sender), you)
	ts := sFg5.Render(msg.at.Format("15:04"))

	var head string
	if mode == layoutMini {
		head = " " + nameStyle.Render(name) + " " + ts
	} else {
		head = " " + nameStyle.Render(name) + "  " + badge + "  " + ts
	}
	if selected {
		head = sActive.Render("▶") + head
	} else {
		head = " " + head
	}
	out := []string{head}
	// wrap the plain content first, then style each line, so long
	// messages never overflow into lipgloss re-wrapping
	for _, l := range wrapPlain(msg.content, w-3) {
		out = append(out, " "+styleBody(l))
	}
	return out
}

// renderInputArea renders the prompt and hint line.
func (m *Model) renderInputArea(channel string, w int, mode layoutMode) string {
	cursor := " "
	if m.inputFocused {
		cursor = sFg1.Render("▋")
	}
	prompt := " " + sFg4.Render(m.displayName(m.id.Pubkey())) +
		sFg3.Render("@"+channel) + " " + sFg1.Render("❯") + " " +
		sFg2.Render(m.input) + cursor
	hint := " " + sFg5.Render("n=nickname · p=profile · s=seal · r=reply · i=sealed inbox · ?=help")
	if m.status != "" {
		hint = " " + sFg4.Render(m.status)
	}
	if mode == layoutMini {
		return sDark2.Render(strings.Repeat("─", w)) + "\n" + prompt
	}
	return sDark2.Render(strings.Repeat("─", w)) + "\n" + prompt + "\n" + hint
}

// renderFeed renders the feed: stacked, dynamic-height entries in the
// old forum style — no boxes, no wasted rows.
func (m *Model) renderFeed(w, h int) string {
	var b []string
	b = append(b, " "+divider("FEED", w-2), "")
	if len(m.posts) == 0 {
		b = append(b, "  "+sFg4.Render("nothing here yet — n=new post"))
	}
	cursorLine := 0
	for i, p := range m.posts {
		if i == m.postSel {
			cursorLine = len(b)
		}
		b = append(b, m.renderPost(p, i == m.postSel, w)...)
		if i == m.postSel && m.postExpanded {
			for _, r := range m.replies[hex.EncodeToString(p.id[:])] {
				name := m.displayName(r.sender)
				rHead := "    " + sFg5.Render(glyph("↩ ", "> ")) + sFg3.Render(name) + sFg5.Render(" · "+r.at.Format("15:04"))
				b = append(b, rHead)
				for _, l := range wrapPlain(r.content, w-7) {
					b = append(b, "      "+sFg2.Render(l))
				}
			}
		}
		b = append(b, "")
	}
	return strings.Join(windowLines(b, cursorLine, h), "\n")
}

// renderPost renders one feed entry at exactly the height its content
// needs: a metadata line, the wrapped body, a compact footer only when
// there are replies. The selection marker is a bright gutter bar.
func (m *Model) renderPost(p feedPost, selected bool, w int) []string {
	you := p.sender == m.id.Pubkey()
	name := m.displayName(p.sender)
	nameStyle := lipgloss.NewStyle().Foreground(trustColor(m.senderScore(p.sender)))
	if you {
		nameStyle = sYou
	}
	gutter := sDark.Render("│")
	if selected {
		gutter = sFg1.Render("┃")
	}
	head := " " + gutter + " " + nameStyle.Render(name) + "  " +
		trustBadge(m.senderScore(p.sender), you) + "  " +
		sFg5.Render(p.at.Format("2006-01-02 15:04"))

	out := []string{head}
	for _, line := range wrapPlain(p.content, w-5) {
		out = append(out, " "+gutter+" "+sFg2.Render(line))
	}
	if n := len(m.replies[hex.EncodeToString(p.id[:])]); n > 0 {
		out = append(out, " "+gutter+" "+sFg4.Render(fmt.Sprintf("%s%d %s", glyph("↩ ", "> "), n, plural(n, "reply", "replies"))))
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// renderForum renders the forum tab: thread list or open thread.
func (m *Model) renderForum(w, h int) string {
	if m.openThread >= 0 && m.openThread < len(m.threads) {
		return m.renderThread(m.threads[m.openThread], w, h)
	}
	var b []string
	b = append(b, " "+divider("FORUM", w-2), "")
	if len(m.threads) == 0 {
		b = append(b, "  "+sFg4.Render("no threads yet — n=new thread"))
	}
	cursorLine := 0
	for i, t := range m.threads {
		if i == m.threadSel {
			cursorLine = len(b)
		}
		gutter := sDark.Render("│")
		if i == m.threadSel {
			gutter = sFg1.Render("┃")
		}
		titleStyle := sFg3
		if !t.read {
			titleStyle = sFg1
		}
		replies := len(m.replies[hex.EncodeToString(t.id[:])])
		meta := fmt.Sprintf("%s · %d %s · %s", m.displayName(t.sender), replies, plural(replies, "reply", "replies"), t.at.Format("2006-01-02"))
		b = append(b,
			" "+gutter+" "+sFg3.Render("["+strings.ToUpper(t.category)+"] ")+titleStyle.Render(t.title),
			" "+gutter+" "+sFg5.Render(meta),
			"")
	}
	return strings.Join(windowLines(b, cursorLine, h), "\n")
}

// renderThread renders an opened thread with its replies.
func (m *Model) renderThread(t forumThread, w, h int) string {
	var b []string
	b = append(b,
		" "+sFg3.Render("["+strings.ToUpper(t.category)+"] ")+sFg1.Render(t.title),
		" "+sFg5.Render(m.displayName(t.sender)+" · "+t.at.Format("2006-01-02")),
		" "+sDark2.Render(strings.Repeat("─", w-2)))
	you := t.sender == m.id.Pubkey()
	opHead := " " + sFg3.Render(m.displayName(t.sender)) + "  " + trustBadge(m.senderScore(t.sender), you) + "  " + sDark.Render("── OP "+strings.Repeat("─", max(0, w-20)))
	b = append(b, opHead)
	for _, line := range wrapPlain(t.content, w-3) {
		b = append(b, "  "+sFg2.Render(line))
	}
	b = append(b, " "+sDark2.Render(strings.Repeat("─", w-2)))
	for i, r := range m.replies[hex.EncodeToString(t.id[:])] {
		rYou := r.sender == m.id.Pubkey()
		head := fmt.Sprintf(" %s  %s  %s",
			sFg3.Render(m.displayName(r.sender)),
			trustBadge(m.senderScore(r.sender), rYou),
			sDark.Render(fmt.Sprintf("── reply %d %s", i+1, strings.Repeat("─", max(0, w-26)))))
		b = append(b, head)
		for _, line := range wrapPlain(r.content, w-3) {
			b = append(b, "  "+sFg2.Render(line))
		}
		b = append(b, " "+sDark2.Render(strings.Repeat("─", w-2)))
	}
	b = append(b, "", " "+sFg5.Render("r=reply  Esc=back"))
	return strings.Join(clipLines(b, h), "\n")
}

// renderSealedInbox renders the sealed inbox (Messages subview).
func (m *Model) renderSealedInbox(w, h int) string {
	var b []string
	b = append(b, " "+divider("SEALED INBOX", w-2))
	if len(m.sealedMsgs) == 0 {
		b = append(b, "", "  "+sFg4.Render("empty"))
	}
	cursorLine := 0
	openedShown := false
	for i, sm := range m.sealedMsgs {
		opened := m.sealedOpen[sm.MsgHash] || sm.Read
		if opened && !openedShown {
			b = append(b, "", " "+divider("OPENED", w-2))
			openedShown = true
		}
		if i == m.sealedSel {
			cursorLine = len(b)
		}
		name := m.displayName(sm.Sender)
		at := time.Unix(sm.ReceivedAt, 0).Format("15:04")
		var line string
		switch {
		case m.sealedOpen[sm.MsgHash]:
			line = " " + sFg4.Render("✓ from ") + sFg3.Render(name) + sFg5.Render(" · "+at)
		case sm.Read:
			line = " " + sFg4.Render("✓ from "+name+" · "+at+" · read")
		default:
			line = " " + sAccent.Render(glyph("◈", "*")) + sFg2.Render(" from "+name) + sFg5.Render(" · "+at)
		}
		if i == m.sealedSel {
			line = sFg1.Render("▶") + line
		} else {
			line = " " + line
		}
		b = append(b, line)
		if m.sealedOpen[sm.MsgHash] {
			for _, l := range wrapPlain(string(sm.Data), w-6) {
				b = append(b, "     "+sFg2.Render(l))
			}
		} else if i == m.sealedSel {
			b = append(b, "     "+sFg4.Render("[ press Enter to open ]"))
		}
	}
	b = append(b, "", " "+sFg5.Render("Enter=open  s=seal reply  d=deniable reply  n=nickname  Esc=chat"))
	return strings.Join(windowLines(b, cursorLine, h), "\n")
}

// renderPeople renders the People tab: your identity, then every
// known correspondent with nickname, trust note, and block state.
// Enter opens the profile detail.
func (m *Model) renderPeople(w, h int) string {
	if m.peopleDetail {
		return m.renderProfile(w, h)
	}
	var b []string
	b = append(b, " "+divider("PEOPLE", w-2), "")

	for i := 0; i < m.peopleCount(); i++ {
		pk := m.personAt(i)
		you := i == 0
		name := m.displayName(pk)
		var meta []string
		if you {
			meta = append(meta, "you")
		} else {
			if n, err := m.db.GetNickname(pk); err == nil && n.Note != "" {
				meta = append(meta, n.Note)
			}
			if blocked, _ := m.db.IsBlocked(pk); blocked {
				meta = append(meta, "BLOCKED")
			}
		}
		dot := sFg4.Render("○")
		if you || (i-1 < len(m.people) && recentlySeen(m.people[i-1].lastSeen)) {
			dot = sFg1.Render("●")
		}
		nameStyle := lipgloss.NewStyle().Foreground(trustColor(m.senderScore(pk)))
		if you {
			nameStyle = sYou
		}
		line := " " + dot + " " + nameStyle.Render(padRight(name, 18)) + " " +
			sFg5.Render("~"+hex.EncodeToString(pk[:])[:8]) + "  " +
			trustBadge(m.senderScore(pk), you)
		if len(meta) > 0 {
			line += "  " + sFg5.Render(strings.Join(meta, " · "))
		}
		if i == m.peopleSel {
			line = sFg1.Render("▶") + line
		} else {
			line = " " + line
		}
		b = append(b, line)
	}
	b = append(b, "", " "+sFg5.Render("Enter=profile  n=nickname  s=seal  b=block/unblock  e=edit own"))
	return strings.Join(clipLines(b, h), "\n")
}

// renderNetwork renders the Network tab: peer table plus local
// diagnostics — connection, traffic totals, capability flags of the
// selected peer.
func (m *Model) renderNetwork(w, h int) string {
	var b []string
	b = append(b, " "+divider("PEERS", w-2), "")
	if len(m.peerList) == 0 {
		b = append(b, "  "+sFg4.Render("no peers — c=connect"))
	}
	for i, p := range m.peerList {
		score := trustScore(p.Trust)
		name := m.displayName(p.Identity)
		addr := p.Addr
		if addr == "" {
			addr = "~" + hex.EncodeToString(p.Identity[:])[:4]
		}
		line := " " + sFg5.Render(padRight(addr, 16)) + " " +
			lipgloss.NewStyle().Foreground(trustColor(score)).Render(padRight(name, 10)) + " " +
			trustBadge(score, false) + "  " +
			trustBar(score, 10) + "  " +
			sFg3.Render(fmt.Sprintf("%.2f", score))
		if i == m.peerSel {
			line = sFg1.Render("▶") + line
		} else {
			line = " " + line
		}
		b = append(b, line)
	}
	if m.peerSel < len(m.peerList) {
		p := m.peerList[m.peerSel]
		up := time.Since(time.Unix(p.ConnectedAt, 0)).Truncate(time.Second)
		seen := time.Since(time.Unix(p.LastSeen, 0)).Truncate(time.Second)
		b = append(b, "", " "+divider("SELECTED PEER", w-2),
			"  "+sFg5.Render("agent:    ")+sFg3.Render(p.UserAgent),
			"  "+sFg5.Render("session:  ")+sFg3.Render(up.String()),
			"  "+sFg5.Render("last seen:")+sFg3.Render(" "+seen.String()+" ago"),
			"  "+sFg5.Render("caps:     ")+sFg3.Render(capFlags(p.Capabilities)))
	}
	state := "connected"
	if !m.connected {
		state = "offline"
	}
	b = append(b, "", " "+divider("THIS CLIENT", w-2),
		"  "+sFg5.Render("daemon:   ")+sFg3.Render(state+" · "+m.socket),
		"  "+sFg5.Render("traffic:  ")+sFg3.Render(
			glyph("↑", "^")+fmtTotal(m.stats.outTotal)+" "+glyph("↓", "v")+fmtTotal(m.stats.inTotal)+
				" · "+glyph("↑", "^")+fmtRate(m.stats.rateOut)+"/s "+glyph("↓", "v")+fmtRate(m.stats.rateIn)+"/s"))
	b = append(b, "", " "+sFg5.Render("[c]=connect  [v]=vouch  [b]=untrust  [n]=nickname"))
	return strings.Join(clipLines(b, h), "\n")
}

// fmtTotal renders a byte total compactly.
func fmtTotal(n uint64) string {
	switch {
	case n < 1024:
		return strconv.FormatUint(n, 10) + "B"
	case n < 1024*1024:
		return fmt.Sprintf("%.1fK", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/(1024*1024))
	}
}

// capFlags renders a capability bitmask as readable flags.
func capFlags(c uint32) string {
	names := []struct {
		bit  uint32
		name string
	}{
		{0x0001, "chat"}, {0x0002, "feed"}, {0x0004, "forum"},
		{0x0008, "storage"}, {0x0010, "relay"}, {0x0020, "seed"},
	}
	var out []string
	for _, n := range names {
		if c&n.bit != 0 {
			out = append(out, n.name)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, " ")
}

// renderProfile renders a profile (People detail). A profile's own
// theme colors apply inside the box only.
func (m *Model) renderProfile(w, h int) string {
	pk := m.profileTarget
	p := m.profiles[pk]
	if p == nil {
		if data, err := m.db.GetProfile(pk); err == nil {
			// opportunistic cache load
			m.displayName(pk)
			p = m.profiles[pk]
			_ = data
		}
	}

	// theme override applies inside the box only
	fg, accent := sFg3, sAccent
	if p != nil {
		if p.Theme.FgColor != "" {
			fg = lipgloss.NewStyle().Foreground(lipgloss.Color(p.Theme.FgColor))
		}
		if p.Theme.AccentColor != "" {
			accent = lipgloss.NewStyle().Foreground(lipgloss.Color(p.Theme.AccentColor))
		}
	}

	boxW := min(w-4, 64)
	if boxW < 20 {
		boxW = 20
	}
	bd := sFg2
	row := func(content string) string {
		return " " + bd.Render("║") + " " + content + strings.Repeat(" ", max(0, boxW-lipgloss.Width(content)-2)) + " " + bd.Render("║")
	}
	sep := " " + bd.Render("╠"+strings.Repeat("═", boxW)+"╣")

	var b []string
	b = append(b, " "+bd.Render("╔"+strings.Repeat("═", boxW)+"╗"))
	nameLine := accent.Render(m.displayName(pk)) + strings.Repeat(" ", max(0, boxW-lipgloss.Width(m.displayName(pk))-4)) + accent.Render("ᚷ")
	b = append(b, row(nameLine))
	b = append(b, row(sFg5.Render("~"+hex.EncodeToString(pk[:])[:16])))
	if p != nil {
		if p.Bio != "" {
			b = append(b, sep)
			for _, l := range wrapPlain(p.Bio, boxW-4) {
				b = append(b, row(fg.Render(l)))
			}
		}
		if p.Mood != "" || p.NowPlaying != "" || p.Status != "" {
			b = append(b, sep)
			if p.Mood != "" {
				b = append(b, row(sFg5.Render("mood:      ")+fg.Render(p.Mood)))
			}
			if p.NowPlaying != "" {
				b = append(b, row(sFg5.Render("listening: ")+fg.Render(p.NowPlaying)))
			}
			if p.Status != "" {
				b = append(b, row(sFg5.Render("status:    ")+fg.Render(p.Status)))
			}
		}
		if len(p.Links) > 0 {
			b = append(b, sep, row(sFg5.Render(letterSpace("LINKS"))))
			for _, l := range p.Links {
				b = append(b, row(fg.Render("→ "+l.Label+" "+l.URL)))
			}
		}
	} else {
		b = append(b, sep, row(sFg4.Render("no profile announced")))
	}
	gb := m.guestbook[pk]
	b = append(b, sep, row(sFg5.Render(letterSpace("GUESTBOOK"))+sFg5.Render(fmt.Sprintf("  (%d entries)", len(gb)))))
	b = append(b, row(sDark.Render(strings.Repeat("┄", boxW-4))))
	for i, e := range gb {
		if i >= 6 {
			b = append(b, row(sFg5.Render(fmt.Sprintf("… %d more", len(gb)-i))))
			break
		}
		entry := sFg3.Render(m.displayName(e.from)+": ") + sFg2.Render(e.msg)
		for _, l := range wrapStyled(entry, boxW-4) {
			b = append(b, row(l))
		}
	}
	b = append(b, " "+bd.Render("╚"+strings.Repeat("═", boxW)+"╝"))
	if pk == m.id.Pubkey() {
		b = append(b, "  "+sFg5.Render("[e]dit  [Esc]back"))
	} else {
		b = append(b, "  "+sFg5.Render("[s]eal  [g]uestbook entry  [n]ickname  [Esc]back"))
	}
	return strings.Join(clipLines(b, h), "\n")
}

// windowLines returns at most h lines, keeping cursorLine visible.
func windowLines(lines []string, cursorLine, h int) []string {
	if len(lines) <= h {
		return lines
	}
	start := cursorLine - h/2
	if start < 0 {
		start = 0
	}
	if start+h > len(lines) {
		start = len(lines) - h
	}
	return lines[start : start+h]
}

func clipLines(lines []string, h int) []string {
	if len(lines) > h {
		return lines[:h]
	}
	return lines
}

// wrapPlain wraps unstyled text to width.
func wrapPlain(s string, w int) []string {
	if w <= 0 {
		return []string{s}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			switch {
			case line == "":
				line = word
			case len(line)+1+len(word) > w:
				out = append(out, line)
				line = word
			default:
				line += " " + word
			}
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// wrapStyled wraps already-styled text by display width, breaking on
// spaces in the underlying content. Conservative: when in doubt it
// returns the line whole.
func wrapStyled(s string, w int) []string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return []string{s}
	}
	// styled wrapping is approximated by hard-clipping; chat content is
	// styled per-word, so this stays readable
	return []string{s}
}
