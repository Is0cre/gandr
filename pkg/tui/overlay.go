package tui

import (
	"context"
	"encoding/hex"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gandr-net/gandr/pkg/clientdb"
	"github.com/gandr-net/gandr/pkg/crypto"
	"github.com/gandr-net/gandr/pkg/proto"
)

// openNickOverlay opens the nickname quick-set overlay, prefilled.
func (m *Model) openNickOverlay(pk [32]byte) (tea.Model, tea.Cmd) {
	m.overlay = OverlayNick
	m.ovTarget = pk
	m.ovLabels = []string{"nickname", "note"}
	m.ovFields = []string{"", ""}
	m.ovField = 0
	if n, err := m.db.GetNickname(pk); err == nil {
		m.ovFields[0], m.ovFields[1] = n.Name, n.Note
	}
	return m, nil
}

// openSealOverlay opens the sealed composer to a recipient.
func (m *Model) openSealOverlay(pk [32]byte) (tea.Model, tea.Cmd) {
	m.overlay = OverlaySeal
	m.ovTarget = pk
	m.ovLabels = []string{"message"}
	m.ovFields = []string{""}
	m.ovField = 0
	m.ovDeniable = false
	return m, nil
}

// openCompose opens a generic multi-field compose overlay.
func (m *Model) openCompose(kind Overlay, labels []string, target [32]byte) {
	m.overlay = kind
	m.ovTarget = target
	m.ovLabels = labels
	m.ovFields = make([]string, len(labels))
	m.ovField = 0
}

func (m *Model) closeOverlay() {
	m.overlay = OverlayNone
	m.ovFields = nil
	m.ovLabels = nil
	m.ovField = 0
	m.ovDeniable = false
}

// handleOverlayKey routes keys while an overlay is active.
func (m *Model) handleOverlayKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := key.String()

	switch m.overlay {
	case OverlayHelp:
		if k == "esc" || k == "q" || k == "?" {
			m.closeOverlay()
		}
		return m, nil
	case OverlayQuit:
		if k == "y" || k == "Y" {
			m.quitting = true
			return m, tea.Quit
		}
		m.closeOverlay()
		return m, nil
	}

	// field editors
	switch k {
	case "esc":
		m.closeOverlay()
		return m, nil
	case "tab", "down":
		if len(m.ovFields) > 1 {
			m.ovField = (m.ovField + 1) % len(m.ovFields)
		}
		return m, nil
	case "shift+tab", "up":
		if len(m.ovFields) > 1 {
			m.ovField = (m.ovField + len(m.ovFields) - 1) % len(m.ovFields)
		}
		return m, nil
	case "backspace":
		m.ovFields[m.ovField] = trimLastRune(m.ovFields[m.ovField])
		return m, nil
	case "ctrl+d":
		if m.overlay == OverlaySeal {
			m.ovDeniable = !m.ovDeniable
		}
		return m, nil
	case "enter":
		if m.ovField < len(m.ovFields)-1 {
			m.ovField++
			return m, nil
		}
		return m.submitOverlay()
	case " ", "space":
		m.ovFields[m.ovField] += " "
		return m, nil
	default:
		if key.Type == tea.KeyRunes {
			m.ovFields[m.ovField] += string(key.Runes)
		}
		return m, nil
	}
}

// submitOverlay executes the overlay's action.
func (m *Model) submitOverlay() (tea.Model, tea.Cmd) {
	kind := m.overlay
	fields := m.ovFields
	target := m.ovTarget
	deniable := m.ovDeniable
	m.closeOverlay()

	switch kind {
	case OverlayNick:
		name := strings.TrimSpace(fields[0])
		note := strings.TrimSpace(fields[1])
		if name == "" {
			m.db.DeleteNickname(target)
			return m, statusCmd("nickname cleared")
		}
		existing, _ := m.db.GetNickname(target)
		if existing.TrustHint == 0 {
			existing.TrustHint = proto.TrustNeutral
		}
		if err := m.db.SetNickname(clientdb.Nickname{
			Pubkey: target, Name: name, Note: note, TrustHint: existing.TrustHint,
		}); err != nil {
			return m, statusCmd("nickname save failed: " + err.Error())
		}
		return m, statusCmd("nickname set: " + name)

	case OverlaySeal:
		content := strings.TrimSpace(fields[0])
		if content == "" {
			return m, nil
		}
		return m, m.sendSealed(target, content, deniable)

	case OverlayGuestbook:
		msg := strings.TrimSpace(fields[0])
		if msg == "" {
			return m, nil
		}
		payload := &proto.GuestbookPayload{TargetPubkey: target, Message: msg}
		m.guestbook[target] = append(m.guestbook[target], gbEntry{
			from: m.id.Pubkey(), msg: msg, at: time.Now(),
		})
		return m, m.sendPayload(proto.MsgGuestbook, target, payload, "guestbook entry signed")

	case OverlayPostNew:
		content := strings.TrimSpace(fields[0])
		if content == "" {
			return m, nil
		}
		payload := &proto.PostPayload{Content: content}
		if target != ([32]byte{}) {
			payload.ReplyTo = hex.EncodeToString(target[:])
		}
		if payload.ReplyTo == "" {
			// local echo onto the feed
			data, err := proto.EncodePayload(payload)
			if err != nil {
				return m, statusCmd(err.Error())
			}
			env, err := proto.NewEnvelope(m.id.PrivateKey, proto.MsgPost, proto.Broadcast, data)
			if err != nil {
				return m, statusCmd(err.Error())
			}
			m.posts = append([]feedPost{{
				id: env.ContentID(), sender: m.id.Pubkey(), content: content, at: time.Now(),
			}}, m.posts...)
			m.stats.countOut(env)
			cli := m.cli
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if err := cli.Send(ctx, env); err != nil {
					return statusMsg("send failed: " + err.Error())
				}
				return statusMsg("posted")
			}
		}
		m.replies[payload.ReplyTo] = append(m.replies[payload.ReplyTo], message{
			sender: m.id.Pubkey(), content: content, at: time.Now(),
		})
		return m, m.sendPayload(proto.MsgPost, proto.Broadcast, payload, "reply sent")

	case OverlayThreadNew:
		title := strings.TrimSpace(fields[0])
		category := strings.TrimSpace(fields[1])
		body := strings.TrimSpace(fields[2])
		if title == "" || body == "" {
			return m, statusCmd("thread needs a title and a body")
		}
		if category == "" {
			category = "general"
		}
		payload := &proto.ThreadPayload{Title: title, Category: category, Content: body}
		data, err := proto.EncodePayload(payload)
		if err != nil {
			return m, statusCmd(err.Error())
		}
		env, err := proto.NewEnvelope(m.id.PrivateKey, proto.MsgThread, proto.Broadcast, data)
		if err != nil {
			return m, statusCmd(err.Error())
		}
		m.threads = append([]forumThread{{
			id: env.ContentID(), sender: m.id.Pubkey(), title: title,
			category: category, content: body, at: time.Now(), read: true,
		}}, m.threads...)
		m.stats.countOut(env)
		cli := m.cli
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := cli.Send(ctx, env); err != nil {
				return statusMsg("send failed: " + err.Error())
			}
			return statusMsg("thread posted")
		}

	case OverlayProfileEdit:
		p := m.ownProfile()
		p.DisplayName = strings.TrimSpace(fields[0])
		p.Bio = strings.TrimSpace(fields[1])
		p.Status = strings.TrimSpace(fields[2])
		p.Mood = strings.TrimSpace(fields[3])
		p.NowPlaying = strings.TrimSpace(fields[4])
		p.UpdatedAt = time.Now().Unix()
		if err := p.Validate(); err != nil {
			return m, statusCmd(err.Error())
		}
		m.profiles[m.id.Pubkey()] = p
		return m, m.sendPayload(proto.MsgProfile, proto.Broadcast, p, "profile updated")

	case OverlayPeerConnect:
		keyHex := strings.TrimSpace(fields[0])
		b, err := hex.DecodeString(keyHex)
		if err != nil || len(b) != 32 {
			return m, statusCmd("not a 64-char hex node key")
		}
		var key [32]byte
		copy(key[:], b)
		cli := m.cli
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := cli.Connect(ctx, key); err != nil {
				return statusMsg("connect failed: " + err.Error())
			}
			return statusMsg("federation attempt queued")
		}
	}
	return m, nil
}

// sendSealed seals content to a recipient and submits it.
func (m *Model) sendSealed(target [32]byte, content string, deniable bool) tea.Cmd {
	box, err := crypto.Seal(m.id.PrivateKey, target[:], proto.MsgChat, []byte(content), deniable)
	if err != nil {
		return statusCmd("seal failed: " + err.Error())
	}
	payload := &proto.SealedPayload{
		EphemeralPubkey: box.EphemeralPubkey,
		Nonce:           box.Nonce,
		Deniable:        box.Deniable,
		Ciphertext:      box.Ciphertext,
	}
	mode := "signed"
	if deniable {
		mode = "deniable"
	}
	return m.sendPayload(proto.MsgSealed, target, payload,
		"sealed ("+mode+") to "+m.displayName(target))
}

// setPeerTrust issues an IPC trust change.
func (m *Model) setPeerTrust(id [32]byte, level uint8, okStatus string) tea.Cmd {
	cli := m.cli
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := cli.SetTrust(ctx, id, level); err != nil {
			return statusMsg("trust change failed: " + err.Error())
		}
		return statusMsg(okStatus)
	}
}
