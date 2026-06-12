package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleKey is the keyboard dispatcher: the entry gate first, then
// overlays, then global keys, then per-tab keys.
func (m *Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := key.String()

	if k == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}

	if m.gateActive {
		return m.handleGateKey(key)
	}

	if m.overlay != OverlayNone {
		return m.handleOverlayKey(key)
	}

	// disconnected: r retries immediately
	if !m.connected && k == "r" && !m.typing() {
		m.retryIn = 0
		return m, m.attemptReconnect()
	}

	// global navigation
	switch k {
	case "tab":
		m.switchTab(Tab((int(m.tab) + 1) % int(tabCount)))
		return m, nil
	case "shift+tab":
		m.switchTab(Tab((int(m.tab) + int(tabCount) - 1) % int(tabCount)))
		return m, nil
	case "\\":
		m.showSidebar = !m.showSidebar
		return m, nil
	case "?":
		if !m.typing() {
			m.overlay = OverlayHelp
			return m, nil
		}
	case "q":
		if !m.typing() {
			m.overlay = OverlayQuit
			return m, nil
		}
	case "1", "2", "3", "4", "5", "6":
		if !m.typing() {
			m.switchTab(Tab(int(k[0] - '1')))
			return m, nil
		}
	}

	switch m.tab {
	case TabMessages:
		if m.msgView == msgViewSealed {
			return m.handleSealedKey(key)
		}
		return m.handleChatKey(key)
	case TabPeople:
		return m.handlePeopleKey(key)
	case TabFeed:
		return m.handleFeedKey(key)
	case TabForum:
		return m.handleForumKey(key)
	case TabNetwork:
		return m.handlePeersKey(key)
	case TabSettings:
		return m.handleSettingsKey(key)
	}
	return m, nil
}

// typing reports whether printable keys currently belong to the chat
// input line.
func (m *Model) typing() bool {
	return m.tab == TabMessages && m.msgView == msgViewChat &&
		m.inputFocused && m.input != ""
}

// switchTab changes tabs, clearing the unread badge of the target.
func (m *Model) switchTab(t Tab) {
	m.tab = t
	switch t {
	case TabFeed:
		m.feedUnread = 0
	case TabForum:
		m.forumUnread = 0
	case TabMessages:
		if m.msgView == msgViewChat {
			if ch, ok := m.activeChannel(); ok {
				delete(m.chatUnread, ch.ID)
			}
		}
	}
}

// --- entry gate ---

// handleGateKey drives the first-run banner: two buttons, one choice.
func (m *Model) handleGateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.gateView {
		// re-viewing from Settings: any key returns
		m.gateActive = false
		m.gateView = false
		return m, nil
	}
	switch key.String() {
	case "j", "down":
		m.gateScroll++
	case "k", "up":
		m.gateScroll--
		if m.gateScroll < 0 {
			m.gateScroll = 0
		}
	case "left", "h", "shift+tab":
		m.gateBtn = 0
	case "right", "l", "tab":
		m.gateBtn = 1
	case "enter":
		if m.gateBtn == 1 {
			m.quitting = true
			return m, tea.Quit
		}
		// acceptance is stored locally, never transmitted
		m.db.SetSetting("entry_accepted", "1")
		m.gateActive = false
	case "q", "esc":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// --- chat ---

func (m *Model) handleChatKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := key.String()

	if m.inputFocused {
		switch k {
		case "enter":
			line := strings.TrimSpace(m.input)
			m.input = ""
			if line == "" {
				return m, nil
			}
			if strings.HasPrefix(line, "/") {
				return m.runCommand(line)
			}
			return m, m.sendChat(line)
		case "esc":
			if m.input != "" {
				m.input = ""
			} else {
				m.inputFocused = false
			}
			return m, nil
		case "backspace":
			m.input = trimLastRune(m.input)
			return m, nil
		case "up":
			m.moveChatSel(-1)
			return m, nil
		case "down":
			m.moveChatSel(1)
			return m, nil
		case " ", "space":
			m.input += " "
			return m, nil
		default:
			if key.Type == tea.KeyRunes {
				m.input += string(key.Runes)
			}
			return m, nil
		}
	}

	// message list mode
	switch k {
	case "j", "down":
		m.moveChatSel(1)
	case "k", "up":
		m.moveChatSel(-1)
	case "g":
		m.msgSel = 0
		m.follow = false
	case "G":
		m.chatSelEnd()
	case "i":
		m.openSealedView()
	case "/", "enter":
		m.inputFocused = true
	case "esc":
		m.inputFocused = true
	case "n":
		if pk, ok := m.chatCursorSender(); ok {
			return m.openNickOverlay(pk)
		}
	case "p":
		if pk, ok := m.chatCursorSender(); ok {
			m.showProfile(pk)
		}
	case "s":
		if pk, ok := m.chatCursorSender(); ok {
			return m.openSealOverlay(pk)
		}
	case "r":
		if msg, ok := m.chatCursor(); ok {
			m.inputFocused = true
			m.input = "@" + m.displayName(msg.sender) + " "
		}
	default:
		if key.Type == tea.KeyRunes {
			m.inputFocused = true
			m.input += string(key.Runes)
		}
	}
	return m, nil
}

// openSealedView switches the Messages tab to the sealed inbox.
func (m *Model) openSealedView() {
	m.tab = TabMessages
	m.msgView = msgViewSealed
}

// showProfile opens a person's profile in the People tab.
func (m *Model) showProfile(pk [32]byte) {
	m.profileTarget = pk
	m.peopleDetail = true
	m.switchTab(TabPeople)
}

func (m *Model) moveChatSel(delta int) {
	ch, ok := m.activeChannel()
	if !ok {
		return
	}
	last := len(m.messages[ch.ID]) - 1
	m.msgSel = clamp(m.msgSel+delta, 0, last)
	m.follow = m.msgSel == last
}

func (m *Model) chatSelEnd() {
	ch, ok := m.activeChannel()
	if !ok {
		return
	}
	m.msgSel = len(m.messages[ch.ID]) - 1
	m.follow = true
}

func (m *Model) chatCursor() (message, bool) {
	ch, ok := m.activeChannel()
	if !ok {
		return message{}, false
	}
	msgs := m.messages[ch.ID]
	if m.msgSel < 0 || m.msgSel >= len(msgs) {
		return message{}, false
	}
	return msgs[m.msgSel], true
}

func (m *Model) chatCursorSender() ([32]byte, bool) {
	msg, ok := m.chatCursor()
	return msg.sender, ok
}

// --- people ---

// peopleCount is the People list length: own identity plus everyone.
func (m *Model) peopleCount() int { return len(m.people) + 1 }

// personAt maps a People list index to a pubkey; 0 is you.
func (m *Model) personAt(i int) [32]byte {
	if i <= 0 {
		return m.id.Pubkey()
	}
	if i-1 < len(m.people) {
		return m.people[i-1].pubkey
	}
	return m.id.Pubkey()
}

func (m *Model) handlePeopleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.peopleDetail {
		return m.handleProfileKey(key)
	}
	switch key.String() {
	case "j", "down":
		m.peopleSel = clamp(m.peopleSel+1, 0, m.peopleCount()-1)
	case "k", "up":
		m.peopleSel = clamp(m.peopleSel-1, 0, m.peopleCount()-1)
	case "g":
		m.peopleSel = 0
	case "G":
		m.peopleSel = m.peopleCount() - 1
	case "enter", "p":
		m.profileTarget = m.personAt(m.peopleSel)
		m.peopleDetail = true
	case "n":
		if m.peopleSel > 0 {
			return m.openNickOverlay(m.personAt(m.peopleSel))
		}
	case "s":
		if m.peopleSel > 0 {
			return m.openSealOverlay(m.personAt(m.peopleSel))
		}
	case "b":
		if m.peopleSel > 0 {
			pk := m.personAt(m.peopleSel)
			if blocked, _ := m.db.IsBlocked(pk); blocked {
				m.db.Unblock(pk)
				return m, statusCmd("unblocked")
			}
			m.db.Block(pk, "")
			return m, statusCmd("blocked locally")
		}
	case "e":
		if m.peopleSel == 0 {
			m.profileTarget = m.id.Pubkey()
			m.peopleDetail = true
			return m.handleProfileKey(key)
		}
	}
	return m, nil
}

// --- feed ---

func (m *Model) handleFeedKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "j", "down":
		m.postSel = clamp(m.postSel+1, 0, len(m.posts)-1)
	case "k", "up":
		m.postSel = clamp(m.postSel-1, 0, len(m.posts)-1)
	case "g":
		m.postSel = 0
	case "G":
		m.postSel = clamp(len(m.posts)-1, 0, len(m.posts)-1)
	case "enter":
		m.postExpanded = !m.postExpanded
	case "esc":
		m.postExpanded = false
	case "n":
		m.openCompose(OverlayPostNew, []string{"post"}, [32]byte{})
	case "r":
		if m.postSel < len(m.posts) {
			m.ovTarget = m.posts[m.postSel].id
			m.openCompose(OverlayPostNew, []string{"reply"}, m.posts[m.postSel].id)
		}
	case "p":
		if m.postSel < len(m.posts) {
			m.showProfile(m.posts[m.postSel].sender)
		}
	case "s":
		if m.postSel < len(m.posts) {
			return m.openSealOverlay(m.posts[m.postSel].sender)
		}
	}
	return m, nil
}

// --- forum ---

func (m *Model) handleForumKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.openThread >= 0 {
		switch key.String() {
		case "esc":
			m.openThread = -1
		case "r":
			if m.openThread < len(m.threads) {
				m.openCompose(OverlayPostNew, []string{"reply"}, m.threads[m.openThread].id)
			}
		case "n":
			if m.openThread < len(m.threads) {
				return m.openNickOverlay(m.threads[m.openThread].sender)
			}
		}
		return m, nil
	}
	switch key.String() {
	case "j", "down":
		m.threadSel = clamp(m.threadSel+1, 0, len(m.threads)-1)
	case "k", "up":
		m.threadSel = clamp(m.threadSel-1, 0, len(m.threads)-1)
	case "g":
		m.threadSel = 0
	case "G":
		m.threadSel = clamp(len(m.threads)-1, 0, len(m.threads)-1)
	case "enter":
		if m.threadSel < len(m.threads) {
			m.openThread = m.threadSel
			m.threads[m.threadSel].read = true
		}
	case "n":
		m.openCompose(OverlayThreadNew, []string{"title", "category", "body"}, [32]byte{})
	case "p":
		if m.threadSel < len(m.threads) {
			m.showProfile(m.threads[m.threadSel].sender)
		}
	}
	return m, nil
}

// --- sealed (Messages subview) ---

func (m *Model) handleSealedKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "i":
		m.msgView = msgViewChat
	case "j", "down":
		m.sealedSel = clamp(m.sealedSel+1, 0, len(m.sealedMsgs)-1)
	case "k", "up":
		m.sealedSel = clamp(m.sealedSel-1, 0, len(m.sealedMsgs)-1)
	case "enter", "s":
		if m.sealedSel < len(m.sealedMsgs) {
			sm := m.sealedMsgs[m.sealedSel]
			if !m.sealedOpen[sm.MsgHash] {
				m.sealedOpen[sm.MsgHash] = true
				if !sm.Read {
					m.db.MarkSealedRead(sm.MsgHash)
					m.sealedMsgs[m.sealedSel].Read = true
					if m.sealedUnread > 0 {
						m.sealedUnread--
					}
				}
			} else if key.String() == "s" {
				return m.openSealOverlay(sm.Sender)
			}
		}
	case "d":
		if m.sealedSel < len(m.sealedMsgs) {
			_, cmd := m.openSealOverlay(m.sealedMsgs[m.sealedSel].Sender)
			m.ovDeniable = true
			return m, cmd
		}
	case "n":
		if m.sealedSel < len(m.sealedMsgs) {
			return m.openNickOverlay(m.sealedMsgs[m.sealedSel].Sender)
		}
	case "p":
		if m.sealedSel < len(m.sealedMsgs) {
			m.showProfile(m.sealedMsgs[m.sealedSel].Sender)
		}
	}
	return m, nil
}

// --- network (peers) ---

func (m *Model) handlePeersKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "j", "down":
		m.peerSel = clamp(m.peerSel+1, 0, len(m.peerList)-1)
	case "k", "up":
		m.peerSel = clamp(m.peerSel-1, 0, len(m.peerList)-1)
	case "c":
		m.openCompose(OverlayPeerConnect, []string{"yggdrasil node key (hex)"}, [32]byte{})
	case "v":
		if m.peerSel < len(m.peerList) {
			return m, m.setPeerTrust(m.peerList[m.peerSel].Identity, 0x03, "vouched for peer")
		}
	case "b":
		if m.peerSel < len(m.peerList) {
			return m, m.setPeerTrust(m.peerList[m.peerSel].Identity, 0x00, "peer set to untrusted")
		}
	case "n":
		if m.peerSel < len(m.peerList) {
			return m.openNickOverlay(m.peerList[m.peerSel].Identity)
		}
	}
	return m, nil
}

// --- profile (People detail) ---

func (m *Model) handleProfileKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "s":
		if m.profileTarget != m.id.Pubkey() {
			return m.openSealOverlay(m.profileTarget)
		}
	case "g":
		if m.profileTarget != m.id.Pubkey() {
			m.openCompose(OverlayGuestbook, []string{"guestbook entry"}, m.profileTarget)
		}
	case "n":
		if m.profileTarget != m.id.Pubkey() {
			return m.openNickOverlay(m.profileTarget)
		}
	case "e":
		if m.profileTarget == m.id.Pubkey() {
			p := m.ownProfile()
			m.overlay = OverlayProfileEdit
			m.ovLabels = []string{"name", "bio", "status", "mood", "listening"}
			m.ovFields = []string{p.DisplayName, p.Bio, p.Status, p.Mood, p.NowPlaying}
			m.ovField = 0
		}
	case "esc":
		m.peopleDetail = false
		m.profileTarget = m.id.Pubkey()
	}
	return m, nil
}

// --- settings ---

func (m *Model) handleSettingsKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.settingsIn {
		switch key.String() {
		case "j", "down":
			m.settingsSec = clamp(m.settingsSec+1, 0, secCount-1)
		case "k", "up":
			m.settingsSec = clamp(m.settingsSec-1, 0, secCount-1)
		case "enter", "l", "right":
			m.settingsIn = true
			m.settingsItem = 0
			if m.settingsSec == secAppearance {
				for i, t := range themes {
					if t.Name == theme.Name {
						m.settingsItem = i
					}
				}
			}
		}
		return m, nil
	}

	// inside a section
	switch key.String() {
	case "esc", "h", "left":
		m.settingsIn = false
		return m, nil
	}
	switch m.settingsSec {
	case secAppearance:
		switch key.String() {
		case "j", "down":
			m.settingsItem = clamp(m.settingsItem+1, 0, len(themes)-1)
		case "k", "up":
			m.settingsItem = clamp(m.settingsItem-1, 0, len(themes)-1)
		case "enter":
			t := themes[m.settingsItem]
			applyTheme(t)
			if err := m.db.SetSetting("theme", t.Name); err != nil {
				return m, statusCmd("theme save failed: " + err.Error())
			}
			return m, statusCmd("theme: " + t.Name)
		}
	case secAbout:
		if key.String() == "enter" {
			// re-view the entry banner, read-only
			m.gateActive = true
			m.gateView = true
		}
	case secAdvanced:
		if key.String() == "enter" && m.settingsItem == 0 {
			m.gateActive = true
			m.gateView = true
		}
	}
	return m, nil
}

func trimLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}
