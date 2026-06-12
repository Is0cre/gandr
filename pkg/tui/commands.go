package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gandr-net/gandr/pkg/clientdb"
	"github.com/gandr-net/gandr/pkg/proto"
)

// runCommand executes a /command typed in the chat input.
func (m *Model) runCommand(line string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(line)
	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "/help":
		m.overlay = OverlayHelp
		return m, nil

	case "/join":
		if len(args) < 1 {
			return m, statusCmd("usage: /join <channel-name>")
		}
		name := strings.TrimPrefix(args[0], "#")
		id := ChannelID(name)
		if err := m.db.JoinChannel(id, name); err != nil {
			return m, statusCmd("join failed: " + err.Error())
		}
		m.channels, _ = m.db.ListChannels()
		for i, c := range m.channels {
			if c.ID == id {
				m.channelSel = i
			}
		}
		m.chatSelEnd()
		cli := m.cli
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := cli.Subscribe(ctx, id); err != nil {
				return statusMsg("subscribe failed: " + err.Error())
			}
			return statusMsg("joined #" + name)
		}

	case "/leave":
		ch, ok := m.activeChannel()
		if !ok {
			return m, statusCmd("no channel selected")
		}
		m.db.LeaveChannel(ch.ID)
		m.channels, _ = m.db.ListChannels()
		m.channelSel = clamp(m.channelSel, 0, len(m.channels)-1)
		cli := m.cli
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cli.Unsubscribe(ctx, ch.ID)
			return statusMsg("left #" + ch.Name)
		}

	case "/nick":
		if len(args) < 2 {
			return m, statusCmd("usage: /nick <pubkey|nickname> <new_name>")
		}
		pk, ok := m.resolveTarget(args[0])
		if !ok {
			return m, statusCmd("unknown person: " + args[0])
		}
		existing, _ := m.db.GetNickname(pk)
		if existing.TrustHint == 0 {
			existing.TrustHint = proto.TrustNeutral
		}
		name := strings.Join(args[1:], " ")
		if err := m.db.SetNickname(clientdb.Nickname{
			Pubkey: pk, Name: name, Note: existing.Note, TrustHint: existing.TrustHint,
		}); err != nil {
			return m, statusCmd("nick failed: " + err.Error())
		}
		return m, statusCmd("nickname set: " + name)

	case "/note":
		if len(args) < 2 {
			return m, statusCmd("usage: /note <pubkey|nickname> <text>")
		}
		pk, ok := m.resolveTarget(args[0])
		if !ok {
			return m, statusCmd("unknown person: " + args[0])
		}
		existing, err := m.db.GetNickname(pk)
		if err != nil {
			existing = clientdb.Nickname{Pubkey: pk, Name: m.displayName(pk), TrustHint: proto.TrustNeutral}
		}
		existing.Note = strings.Join(args[1:], " ")
		if err := m.db.SetNickname(existing); err != nil {
			return m, statusCmd("note failed: " + err.Error())
		}
		return m, statusCmd("note saved")

	case "/block":
		if len(args) < 1 {
			return m, statusCmd("usage: /block <pubkey|nickname>")
		}
		pk, ok := m.resolveTarget(args[0])
		if !ok {
			return m, statusCmd("unknown person: " + args[0])
		}
		if err := m.db.Block(pk, strings.Join(args[1:], " ")); err != nil {
			return m, statusCmd("block failed: " + err.Error())
		}
		return m, statusCmd("blocked locally")

	case "/unblock":
		if len(args) < 1 {
			return m, statusCmd("usage: /unblock <pubkey|nickname>")
		}
		pk, ok := m.resolveTarget(args[0])
		if !ok {
			return m, statusCmd("unknown person: " + args[0])
		}
		if err := m.db.Unblock(pk); err != nil {
			return m, statusCmd("unblock failed: " + err.Error())
		}
		return m, statusCmd("unblocked")

	case "/trust":
		if len(args) < 2 {
			return m, statusCmd("usage: /trust <pubkey|nickname> <untrusted|neutral|trusted|vouched>")
		}
		pk, ok := m.resolveTarget(args[0])
		if !ok {
			return m, statusCmd("unknown peer: " + args[0])
		}
		var level uint8
		switch args[1] {
		case "untrusted":
			level = proto.TrustUntrusted
		case "neutral":
			level = proto.TrustNeutral
		case "trusted":
			level = proto.TrustTrusted
		case "vouched":
			level = proto.TrustVouched
		default:
			return m, statusCmd("unknown trust level " + args[1])
		}
		return m, m.setPeerTrust(pk, level, "trust set: "+args[1])

	case "/connect":
		if len(args) < 1 {
			return m, statusCmd("usage: /connect <hex node key>")
		}
		m.openCompose(OverlayPeerConnect, []string{"yggdrasil node key (hex)"}, [32]byte{})
		m.ovFields[0] = args[0]
		return m.submitOverlay()

	case "/seal":
		if len(args) < 1 {
			return m, statusCmd("usage: /seal <pubkey|nickname>")
		}
		pk, ok := m.resolveTarget(args[0])
		if !ok {
			return m, statusCmd("unknown person: " + args[0])
		}
		return m.openSealOverlay(pk)

	case "/sealed":
		m.openSealedView()
		return m, nil

	case "/profile":
		if len(args) == 0 {
			m.showProfile(m.id.Pubkey())
		} else {
			pk, ok := m.resolveTarget(args[0])
			if !ok {
				return m, statusCmd("unknown person: " + args[0])
			}
			m.showProfile(pk)
		}
		return m, nil

	case "/people":
		m.peopleDetail = false
		m.switchTab(TabPeople)
		return m, nil

	case "/peers":
		m.switchTab(TabNetwork)
		return m, requestPeers(m.cli)

	case "/set":
		return m.runSet(args)

	default:
		return m, statusCmd("unknown command " + cmd + " — /help")
	}
}

// runSet updates and republishes the local profile.
func (m *Model) runSet(args []string) (tea.Model, tea.Cmd) {
	if len(args) < 2 {
		return m, statusCmd("usage: /set <name|status|mood|np|bio|theme> <value>")
	}
	p := m.ownProfile()
	value := strings.Join(args[1:], " ")
	switch args[0] {
	case "name":
		p.DisplayName = value
	case "status":
		p.Status = value
	case "mood":
		p.Mood = value
	case "np":
		p.NowPlaying = value
	case "bio":
		p.Bio = value
	case "theme":
		if len(args) < 3 {
			return m, statusCmd("usage: /set theme <bg|fg|accent|font|layout> <value>")
		}
		switch args[1] {
		case "bg":
			p.Theme.BgColor = args[2]
		case "fg":
			p.Theme.FgColor = args[2]
		case "accent":
			p.Theme.AccentColor = args[2]
		case "font":
			p.Theme.Font = args[2]
		case "layout":
			p.Theme.Layout = args[2]
		default:
			return m, statusCmd("unknown theme field " + args[1])
		}
	default:
		return m, statusCmd("unknown field " + args[0])
	}
	p.UpdatedAt = time.Now().Unix()
	if err := p.Validate(); err != nil {
		return m, statusCmd(err.Error())
	}
	m.profiles[m.id.Pubkey()] = p
	return m, m.sendPayload(proto.MsgProfile, proto.Broadcast, p, "profile updated")
}
