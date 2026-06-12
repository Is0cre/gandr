package tui

import (
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gandr-net/gandr/pkg/clientdb"
	"github.com/gandr-net/gandr/pkg/crypto"
	"github.com/gandr-net/gandr/pkg/identity"
	"github.com/gandr-net/gandr/pkg/proto"
)

// testModel builds a Model with a real clientdb and identity but no
// daemon connection; tests avoid paths that hit the nil client.
func testModel(t *testing.T) *Model {
	t.Helper()
	id, err := identity.Generate("byte_me")
	if err != nil {
		t.Fatal(err)
	}
	db, err := clientdb.Open(filepath.Join(t.TempDir(), "client.db"), id.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// accept the entry banner up front; the gate has its own tests
	if err := db.SetSetting("entry_accepted", "1"); err != nil {
		t.Fatal(err)
	}
	m, err := New(nil, db, id, "/tmp/test.sock")
	if err != nil {
		t.Fatal(err)
	}
	m.width, m.height = 120, 40
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func press(m *Model, keys ...string) {
	for _, k := range keys {
		m.Update(key(k))
	}
}

func typeText(m *Model, s string) {
	for _, r := range s {
		if r == ' ' {
			m.Update(tea.KeyMsg{Type: tea.KeySpace})
		} else {
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
}

func chatEnvelope(t *testing.T, priv []byte, channel [32]byte, content string) *proto.Envelope {
	t.Helper()
	data, err := proto.EncodePayload(&proto.ChatPayload{ChannelID: channel, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.NewEnvelope(priv, proto.MsgChat, channel, data)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestTabSwitching(t *testing.T) {
	m := testModel(t)
	if m.tab != TabMessages {
		t.Fatal("must start on messages")
	}
	press(m, "tab")
	if m.tab != TabPeople {
		t.Fatal("tab key did not advance")
	}
	press(m, "shift+tab")
	if m.tab != TabMessages {
		t.Fatal("shift+tab did not go back")
	}
	// number keys (input empty so they switch, not type)
	press(m, "5")
	if m.tab != TabNetwork {
		t.Fatalf("tab = %v after '5'", m.tab)
	}
	press(m, "6")
	if m.tab != TabSettings {
		t.Fatal("'6' should open settings")
	}
	press(m, "1")
	if m.tab != TabMessages {
		t.Fatal("'1' should return to messages")
	}
	// but digits type into a non-empty input
	typeText(m, "code")
	press(m, "4")
	if m.tab != TabMessages || m.input != "code4" {
		t.Fatalf("digit typed into input: tab=%v input=%q", m.tab, m.input)
	}
}

func TestSidebarToggle(t *testing.T) {
	m := testModel(t)
	if !m.showSidebar {
		t.Fatal("sidebar visible by default")
	}
	press(m, "\\")
	if m.showSidebar {
		t.Fatal("backslash did not hide sidebar")
	}
}

func TestChatSendAndEcho(t *testing.T) {
	m := testModel(t)
	ch := ChannelID("general")
	m.db.JoinChannel(ch, "general")
	m.channels, _ = m.db.ListChannels()

	typeText(m, "hello gandr")
	if m.input != "hello gandr" {
		t.Fatalf("input = %q", m.input)
	}
	// sending requires the IPC client; capture the local echo only
	_, cmd := m.handleChatKey(key("enter"))
	if cmd == nil {
		t.Fatal("send produced no command")
	}
	msgs := m.messages[ch]
	if len(msgs) != 1 || msgs[0].content != "hello gandr" || msgs[0].sender != m.id.Pubkey() {
		t.Fatalf("local echo missing: %+v", msgs)
	}
	if m.input != "" {
		t.Fatal("input not cleared after send")
	}
}

func TestIncomingChatUnreadBadges(t *testing.T) {
	m := testModel(t)
	chA, chB := ChannelID("a"), ChannelID("b")
	m.db.JoinChannel(chA, "a")
	m.db.JoinChannel(chB, "b")
	m.channels, _ = m.db.ListChannels()
	m.channelSel = 0

	_, priv, _ := crypto.GenerateIdentity()
	m.handleIncoming(chatEnvelope(t, priv, chA, "active channel"))
	m.handleIncoming(chatEnvelope(t, priv, chB, "other channel"))

	if m.chatUnread[chA] != 0 {
		t.Fatal("active channel counted as unread")
	}
	if m.chatUnread[chB] != 1 {
		t.Fatal("inactive channel not counted as unread")
	}
	if m.tabBadge(TabMessages) != 1 {
		t.Fatalf("messages tab badge = %d", m.tabBadge(TabMessages))
	}
	if len(m.people) != 1 {
		t.Fatal("sender not tracked")
	}
}

func TestFeedPostsAndReplies(t *testing.T) {
	m := testModel(t)
	_, priv, _ := crypto.GenerateIdentity()

	data, _ := proto.EncodePayload(&proto.PostPayload{Content: "first post"})
	post, err := proto.NewEnvelope(priv, proto.MsgPost, proto.Broadcast, data)
	if err != nil {
		t.Fatal(err)
	}
	m.handleIncoming(post)
	if len(m.posts) != 1 || m.posts[0].content != "first post" {
		t.Fatalf("posts: %+v", m.posts)
	}
	if m.feedUnread != 1 {
		t.Fatal("feed unread not bumped")
	}

	parent := post.ContentID()
	rdata, _ := proto.EncodePayload(&proto.ReplyPayload{ParentHash: hex.EncodeToString(parent[:]), Content: "a reply"})
	reply, err := proto.NewEnvelope(priv, proto.MsgReply, proto.Broadcast, rdata)
	if err != nil {
		t.Fatal(err)
	}
	m.handleIncoming(reply)
	if len(m.replies[hex.EncodeToString(parent[:])]) != 1 {
		t.Fatal("reply not threaded under parent")
	}
	// switching to the feed clears the badge
	press(m, "3")
	if m.feedUnread != 0 {
		t.Fatal("feed badge not cleared on visit")
	}
}

func TestForumThreadFlow(t *testing.T) {
	m := testModel(t)
	_, priv, _ := crypto.GenerateIdentity()
	data, _ := proto.EncodePayload(&proto.ThreadPayload{Title: "routing at scale", Category: "tech", Content: "body"})
	th, err := proto.NewEnvelope(priv, proto.MsgThread, proto.Broadcast, data)
	if err != nil {
		t.Fatal(err)
	}
	m.handleIncoming(th)
	if len(m.threads) != 1 || m.threads[0].read {
		t.Fatal("thread not recorded as unread")
	}
	press(m, "4")
	press(m, "enter")
	if m.openThread != 0 || !m.threads[0].read {
		t.Fatal("thread did not open / mark read")
	}
	press(m, "esc")
	if m.openThread >= 0 {
		t.Fatal("esc did not close thread")
	}
}

func TestSealedFlow(t *testing.T) {
	m := testModel(t)
	_, senderPriv, _ := crypto.GenerateIdentity()
	box, err := crypto.Seal(senderPriv, m.id.PublicKey, proto.MsgChat, []byte("for your eyes"), true)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := proto.EncodePayload(&proto.SealedPayload{
		EphemeralPubkey: box.EphemeralPubkey, Nonce: box.Nonce,
		Deniable: box.Deniable, Ciphertext: box.Ciphertext,
	})
	env, err := proto.NewEnvelope(senderPriv, proto.MsgSealed, m.id.Pubkey(), payload)
	if err != nil {
		t.Fatal(err)
	}
	m.handleIncoming(env)
	if len(m.sealedMsgs) != 1 || m.sealedUnread != 1 {
		t.Fatalf("sealed inbox: %d msgs, %d unread", len(m.sealedMsgs), m.sealedUnread)
	}
	// content hidden until opened: sealed inbox is a Messages subview
	press(m, "esc", "i")
	if m.tab != TabMessages || m.msgView != msgViewSealed {
		t.Fatalf("sealed view not open: tab=%v view=%d", m.tab, m.msgView)
	}
	out := m.View()
	if strings.Contains(out, "for your eyes") {
		t.Fatal("sealed content visible before opening")
	}
	press(m, "enter")
	if m.sealedUnread != 0 {
		t.Fatal("opening did not clear unread")
	}
	out = m.View()
	if !strings.Contains(out, "for your eyes") {
		t.Fatal("opened sealed content not displayed")
	}
	// persisted decrypted
	stored, _ := m.db.ListSealed()
	if len(stored) != 1 || string(stored[0].Data) != "for your eyes" {
		t.Fatal("sealed message not persisted")
	}
}

func TestSealedForSomeoneElseIgnored(t *testing.T) {
	m := testModel(t)
	_, senderPriv, _ := crypto.GenerateIdentity()
	otherPub, _, _ := crypto.GenerateIdentity()
	box, err := crypto.Seal(senderPriv, otherPub, proto.MsgChat, []byte("not yours"), false)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := proto.EncodePayload(&proto.SealedPayload{
		EphemeralPubkey: box.EphemeralPubkey, Nonce: box.Nonce,
		Deniable: box.Deniable, Ciphertext: box.Ciphertext,
	})
	var otherPk [32]byte
	copy(otherPk[:], otherPub)
	env, err := proto.NewEnvelope(senderPriv, proto.MsgSealed, otherPk, payload)
	if err != nil {
		t.Fatal(err)
	}
	m.handleIncoming(env)
	if len(m.sealedMsgs) != 0 {
		t.Fatal("foreign sealed message accepted")
	}
}

func TestNicknameOverlayFlow(t *testing.T) {
	m := testModel(t)
	ch := ChannelID("general")
	m.db.JoinChannel(ch, "general")
	m.channels, _ = m.db.ListChannels()
	_, priv, _ := crypto.GenerateIdentity()
	env := chatEnvelope(t, priv, ch, "hello")
	m.handleIncoming(env)

	// leave input mode, select the message, open nickname overlay
	press(m, "esc", "n")
	if m.overlay != OverlayNick {
		t.Fatalf("overlay = %v", m.overlay)
	}
	typeText(m, "old friend")
	press(m, "tab")
	typeText(m, "met at defcon")
	press(m, "enter")
	if m.overlay != OverlayNone {
		t.Fatal("overlay did not close")
	}
	n, err := m.db.GetNickname(env.Sender)
	if err != nil || n.Name != "old friend" || n.Note != "met at defcon" {
		t.Fatalf("nickname: %+v err %v", n, err)
	}
	if m.displayName(env.Sender) != "old friend" {
		t.Fatal("nickname not used for display")
	}
}

func TestDisplayNamePriority(t *testing.T) {
	m := testModel(t)
	var pk [32]byte
	pk[0] = 0xAB
	if got := m.displayName(pk); got != "~ab00" {
		t.Fatalf("fallback = %q, want ~ab00", got)
	}
	m.profiles[pk] = &proto.ProfilePayload{DisplayName: "announced"}
	if m.displayName(pk) != "announced" {
		t.Fatal("announced name not used")
	}
	m.db.SetNickname(clientdb.Nickname{Pubkey: pk, Name: "petname", TrustHint: proto.TrustVouched})
	if m.displayName(pk) != "petname" {
		t.Fatal("nickname must win")
	}
}

func TestTrustDisplay(t *testing.T) {
	if trustBadge(0.8, false) == "" || !strings.Contains(trustBadge(0.8, false), "vouched") {
		t.Fatal("vouched badge wrong")
	}
	if !strings.Contains(trustBadge(0.45, false), "trusted") {
		t.Fatal("trusted badge wrong")
	}
	if !strings.Contains(trustBadge(0.2, false), "neutral") {
		t.Fatal("neutral badge wrong")
	}
	if !strings.Contains(trustBadge(0.05, false), "new") {
		t.Fatal("new badge wrong")
	}
	if !strings.Contains(trustBadge(0.9, true), "you") {
		t.Fatal("you badge wrong")
	}
	bar := trustBar(0.5, 10)
	if !strings.Contains(bar, "█████") {
		t.Fatal("bar not half filled")
	}
}

func TestQuitConfirm(t *testing.T) {
	m := testModel(t)
	press(m, "esc") // leave input focus
	press(m, "q")
	if m.overlay != OverlayQuit {
		t.Fatal("no quit confirmation")
	}
	press(m, "x")
	if m.overlay != OverlayNone || m.quitting {
		t.Fatal("quit not cancelled")
	}
	press(m, "q", "y")
	if !m.quitting {
		t.Fatal("quit not confirmed")
	}
}

func TestReconnectStateMachine(t *testing.T) {
	m := testModel(t)
	m.Update(daemonGoneMsg{})
	if m.connected {
		t.Fatal("still connected after daemon gone")
	}
	if m.retryIn != 1 {
		t.Fatalf("retryIn = %d", m.retryIn)
	}
	// failure backs off exponentially, capped at 30
	for _, want := range []int{2, 4, 8, 16, 30, 30} {
		m.Update(retryFailMsg{})
		if m.retryIn != want {
			t.Fatalf("retryIn = %d, want %d", m.retryIn, want)
		}
	}
	// pulse toggles while disconnected
	before := m.pulse
	m.Update(pulseTickMsg{})
	if m.pulse == before {
		t.Fatal("pulse did not toggle")
	}
	// compose state survives the outage
	m.input = "draft survives"
	m.Update(daemonGoneMsg{})
	if m.input != "draft survives" {
		t.Fatal("compose state lost on disconnect")
	}
}

func TestViewRendersAllSizesAndTabs(t *testing.T) {
	m := testModel(t)
	ch := ChannelID("general")
	m.db.JoinChannel(ch, "general")
	m.channels, _ = m.db.ListChannels()
	_, priv, _ := crypto.GenerateIdentity()
	m.handleIncoming(chatEnvelope(t, priv, ch, "a message with `code` and @byte_me"))

	sizes := [][2]int{{120, 40}, {80, 24}, {40, 20}}
	for _, size := range sizes {
		m.width, m.height = size[0], size[1]
		for tab := TabMessages; tab < tabCount; tab++ {
			m.tab = tab
			if out := m.View(); out == "" {
				t.Fatalf("empty view: tab %d at %dx%d", tab, size[0], size[1])
			}
		}
	}
	// the compact header carries the mark and connection state; the
	// big logo must NOT be in the main view at any size
	m.width, m.height = 120, 40
	m.tab = TabMessages
	out := m.View()
	if !strings.Contains(out, "ᚷ") {
		t.Fatal("rune mark missing from header")
	}
	if !strings.Contains(out, "connected") {
		t.Fatal("connection state missing from header")
	}
	if strings.Contains(out, "██████╗") {
		t.Fatal("big logo leaked into the main view")
	}
	m.width = 50
	if !strings.Contains(m.View(), "ᚷ") {
		t.Fatal("mini header missing")
	}
	// below minimum: warning
	m.width = 38
	if !strings.Contains(m.View(), "terminal too narrow") {
		t.Fatal("narrow warning missing")
	}
}

func TestOverlayRendersOverContent(t *testing.T) {
	m := testModel(t)
	press(m, "esc")
	m.Update(key("?"))
	if m.overlay != OverlayHelp {
		t.Fatal("help overlay not open")
	}
	if !strings.Contains(m.View(), "HELP") {
		t.Fatal("help overlay not rendered")
	}
	press(m, "esc")
	if m.overlay != OverlayNone {
		t.Fatal("esc did not close help")
	}
}

func TestConnectionLostBanner(t *testing.T) {
	m := testModel(t)
	m.Update(daemonGoneMsg{})
	out := m.View()
	if !strings.Contains(out, "CONNECTION LOST") {
		t.Fatal("connection lost banner missing")
	}
	if !strings.Contains(out, "retry") {
		t.Fatal("retry hint missing")
	}
}

func TestBlockedSenderDropped(t *testing.T) {
	m := testModel(t)
	ch := ChannelID("general")
	m.db.JoinChannel(ch, "general")
	m.channels, _ = m.db.ListChannels()
	_, priv, _ := crypto.GenerateIdentity()
	env := chatEnvelope(t, priv, ch, "spam")
	m.db.Block(env.Sender, "")
	m.handleIncoming(env)
	if len(m.messages[ch]) != 0 {
		t.Fatal("blocked sender's message shown")
	}
}

func TestDeletePropagatesToViews(t *testing.T) {
	m := testModel(t)
	_, priv, _ := crypto.GenerateIdentity()
	data, _ := proto.EncodePayload(&proto.PostPayload{Content: "soon gone"})
	post, err := proto.NewEnvelope(priv, proto.MsgPost, proto.Broadcast, data)
	if err != nil {
		t.Fatal(err)
	}
	m.handleIncoming(post)
	hash := post.ContentID()
	ddata, _ := proto.EncodePayload(&proto.DeletePayload{TargetHash: hex.EncodeToString(hash[:])})
	del, err := proto.NewEnvelope(priv, proto.MsgDelete, proto.Broadcast, ddata)
	if err != nil {
		t.Fatal(err)
	}
	m.handleIncoming(del)
	if len(m.posts) != 0 {
		t.Fatal("deleted post still shown")
	}
}

func TestGuestbookCollected(t *testing.T) {
	m := testModel(t)
	_, priv, _ := crypto.GenerateIdentity()
	target := m.id.Pubkey()
	data, _ := proto.EncodePayload(&proto.GuestbookPayload{TargetPubkey: target, Message: "miss you, come home"})
	env, err := proto.NewEnvelope(priv, proto.MsgGuestbook, target, data)
	if err != nil {
		t.Fatal(err)
	}
	m.handleIncoming(env)
	if len(m.guestbook[target]) != 1 {
		t.Fatal("guestbook entry not collected")
	}
	m.showProfile(target)
	if m.tab != TabPeople || !m.peopleDetail {
		t.Fatal("showProfile did not open People detail")
	}
	if !strings.Contains(m.View(), "miss you, come home") {
		t.Fatal("guestbook entry not rendered on profile")
	}
}

func TestChannelIDDeterministic(t *testing.T) {
	if ChannelID("general") != ChannelID("general") || ChannelID("a") == ChannelID("b") {
		t.Fatal("channel id derivation broken")
	}
}

func TestSealComposeDeniableToggle(t *testing.T) {
	m := testModel(t)
	var pk [32]byte
	pk[0] = 1
	m.openSealOverlay(pk)
	if m.ovDeniable {
		t.Fatal("deniable on by default")
	}
	press(m, "ctrl+d")
	if !m.ovDeniable {
		t.Fatal("ctrl+d did not toggle deniable")
	}
	typeText(m, "d") // 'd' must type, not toggle
	if m.ovFields[0] != "d" || !m.ovDeniable {
		t.Fatalf("'d' mishandled: field=%q deniable=%v", m.ovFields[0], m.ovDeniable)
	}
}

func TestPeopleOnlineIndicator(t *testing.T) {
	m := testModel(t)
	var pk [32]byte
	pk[0] = 7
	m.people = []person{{pubkey: pk, lastSeen: time.Now()}}
	m.width, m.height = 120, 40
	out := m.View()
	if !strings.Contains(out, "●") {
		t.Fatal("online indicator missing")
	}
	m.people[0].lastSeen = time.Now().Add(-time.Hour)
	if !strings.Contains(m.View(), "○") {
		t.Fatal("offline indicator missing")
	}
}
