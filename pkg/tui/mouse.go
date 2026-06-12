package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleMouse maps optional mouse input onto existing navigation.
// Everything here has a keyboard equivalent — terminals without mouse
// reporting (or users who never touch one) lose nothing.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.gateActive || m.overlay != OverlayNone {
		return m, nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollBy(-1)
		return m, nil
	case tea.MouseButtonWheelDown:
		m.scrollBy(1)
		return m, nil
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	// tab bar
	if msg.Y == m.ui.tabRow {
		for _, z := range m.ui.tabZones {
			if msg.X >= z.x0 && msg.X <= z.x1 {
				m.switchTab(z.tab)
				return m, nil
			}
		}
		return m, nil
	}

	// sidebar items
	if m.ui.sidebarW > 0 && msg.X < m.ui.sidebarW && msg.Y >= m.ui.bodyTop {
		for _, r := range m.ui.sideRows {
			if r.y != msg.Y {
				continue
			}
			switch r.kind {
			case sideChannel:
				if r.index < len(m.channels) {
					m.channelSel = r.index
					m.tab = TabMessages
					m.msgView = msgViewChat
					m.chatSelEnd()
					delete(m.chatUnread, m.channels[r.index].ID)
				}
			case sideSealed:
				m.openSealedView()
			case sidePerson:
				m.peopleSel = r.index + 1
				m.peopleDetail = false
				m.switchTab(TabPeople)
			}
			return m, nil
		}
	}
	return m, nil
}

// scrollBy moves the current view's selection — the same motion j/k
// produce.
func (m *Model) scrollBy(delta int) {
	switch m.tab {
	case TabMessages:
		if m.msgView == msgViewSealed {
			m.sealedSel = clamp(m.sealedSel+delta, 0, len(m.sealedMsgs)-1)
		} else {
			m.moveChatSel(delta)
		}
	case TabPeople:
		if !m.peopleDetail {
			m.peopleSel = clamp(m.peopleSel+delta, 0, m.peopleCount()-1)
		}
	case TabFeed:
		m.postSel = clamp(m.postSel+delta, 0, len(m.posts)-1)
	case TabForum:
		if m.openThread < 0 {
			m.threadSel = clamp(m.threadSel+delta, 0, len(m.threads)-1)
		}
	case TabNetwork:
		m.peerSel = clamp(m.peerSel+delta, 0, len(m.peerList)-1)
	case TabSettings:
		if m.settingsIn && m.settingsSec == secAppearance {
			m.settingsItem = clamp(m.settingsItem+delta, 0, len(themes)-1)
		} else if !m.settingsIn {
			m.settingsSec = clamp(m.settingsSec+delta, 0, secCount-1)
		}
	}
}
