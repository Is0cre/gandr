package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gandr-net/gandr/pkg/clientdb"
	"github.com/gandr-net/gandr/pkg/crypto"
	"github.com/gandr-net/gandr/pkg/identity"
	"github.com/gandr-net/gandr/pkg/ipc"
	"github.com/gandr-net/gandr/pkg/proto"
)

// ChannelID derives a channel id from its name. Channels are
// rendezvous points, not access control: knowing the name is knowing
// the channel.
func ChannelID(name string) [32]byte {
	return sha256.Sum256([]byte("gandr-channel:" + name))
}

// Tab identifies a top-level view.
type Tab int

// Tabs in display order, organized around intent, not transport:
// Messages (chat + sealed), People (contacts + profiles + identity),
// Feed, Forum, Network (peers + diagnostics), Settings.
const (
	TabMessages Tab = iota
	TabPeople
	TabFeed
	TabForum
	TabNetwork
	TabSettings
	tabCount
)

// msgView selects the Messages tab subview.
const (
	msgViewChat = iota
	msgViewSealed
)

// Overlay identifies the active overlay, if any. Overlays render over
// the content area only — never over the header or tabs.
type Overlay int

// Overlay kinds.
const (
	OverlayNone Overlay = iota
	OverlayHelp
	OverlayQuit
	OverlayNick
	OverlaySeal
	OverlayGuestbook
	OverlayPostNew
	OverlayThreadNew
	OverlayProfileEdit
	OverlayPeerConnect
)

// settings sections, in display order.
const (
	secAppearance = iota
	secNetwork
	secIdentity
	secNotifications
	secStorage
	secKeybindings
	secAdvanced
	secAbout
	secCount
)

var settingsSections = [secCount]string{
	"Appearance", "Network", "Identity", "Notifications",
	"Storage", "Keybindings", "Advanced", "About",
}

// message is one chat/reply line.
type message struct {
	id      [32]byte
	sender  [32]byte
	content string
	at      time.Time
}

// person is a known correspondent.
type person struct {
	pubkey   [32]byte
	lastSeen time.Time
}

// feedPost is one feed entry.
type feedPost struct {
	id      [32]byte
	sender  [32]byte
	content string
	at      time.Time
}

// forumThread is one forum thread.
type forumThread struct {
	id       [32]byte
	sender   [32]byte
	title    string
	category string
	content  string
	at       time.Time
	read     bool
}

// gbEntry is one guestbook entry on a profile.
type gbEntry struct {
	from [32]byte
	msg  string
	at   time.Time
}

// Bubble Tea messages.
type (
	incomingMsg   struct{ env *proto.Envelope }
	deliveredMsg  struct{ env *proto.Envelope }
	peerUpdateMsg struct{ peers []ipc.PeerInfo }
	daemonGoneMsg struct{}
	reconnectMsg  struct{ cli *ipc.Client }
	retryFailMsg  struct{}
	retryTickMsg  struct{}
	pulseTickMsg  struct{}
	statusMsg     string
)

// Model is the root Bubble Tea model.
type Model struct {
	cli    *ipc.Client
	db     *clientdb.DB
	id     *identity.Identity
	socket string // for reconnect

	width, height int
	tab           Tab
	showSidebar   bool
	overlay       Overlay

	// gate is the first-run entry banner shown before the main app.
	gateActive bool
	gateBtn    int  // 0 = enter, 1 = gtfo
	gateView   bool // re-viewing from Settings: any key returns
	gateScroll int

	// messages subview: chat or the sealed inbox
	msgView int

	// people: selection 0 is own identity, 1..n are m.people
	peopleDetail bool

	// settings
	settingsSec  int  // selected section
	settingsIn   bool // inside a section
	settingsItem int  // selection inside a section (theme picker, …)

	// traffic stats (local byte counters only — no identities, no content)
	stats trafficStats

	// mouse hit zones, recorded during View
	ui uiGeometry

	// chat
	channels     []clientdb.Channel
	channelSel   int
	messages     map[[32]byte][]message
	msgSel       int
	follow       bool // selection pinned to newest message
	chatUnread   map[[32]byte]int
	input        string
	inputFocused bool

	// people
	people    []person
	peopleSel int

	// feed
	posts        []feedPost
	postSel      int
	postExpanded bool
	feedUnread   int

	// forum
	threads     []forumThread
	threadSel   int
	openThread  int // index into threads, -1 closed
	forumUnread int

	// replies shared by feed and forum, keyed by parent content id hex
	replies map[string][]message

	// sealed
	sealedMsgs   []clientdb.SealedMessage
	sealedSel    int
	sealedOpen   map[[32]byte]bool // revealed in this session
	sealedUnread int

	// peers
	peerList []ipc.PeerInfo
	peerSel  int

	// profile
	profileTarget [32]byte
	profiles      map[[32]byte]*proto.ProfilePayload
	guestbook     map[[32]byte][]gbEntry

	// overlay state
	ovFields   []string
	ovLabels   []string
	ovField    int
	ovTarget   [32]byte
	ovDeniable bool

	// connection
	connected  bool
	retryDelay int // seconds, doubles to 30
	retryIn    int
	pulse      bool

	status   string
	quitting bool
}

// New assembles the client model.
func New(cli *ipc.Client, db *clientdb.DB, id *identity.Identity, socket string) (*Model, error) {
	channels, err := db.ListChannels()
	if err != nil {
		return nil, err
	}
	if v, err := db.GetSetting("theme"); err == nil {
		if t, ok := themeByName(v); ok {
			applyTheme(t)
		}
	}
	m := &Model{
		cli:           cli,
		db:            db,
		id:            id,
		socket:        socket,
		tab:           TabMessages,
		showSidebar:   true,
		channels:      channels,
		messages:      make(map[[32]byte][]message),
		chatUnread:    make(map[[32]byte]int),
		replies:       make(map[string][]message),
		sealedOpen:    make(map[[32]byte]bool),
		profiles:      make(map[[32]byte]*proto.ProfilePayload),
		guestbook:     make(map[[32]byte][]gbEntry),
		profileTarget: id.Pubkey(),
		openThread:    -1,
		inputFocused:  true,
		follow:        true,
		connected:     true,
		retryDelay:    1,
	}
	if sealed, err := db.ListSealed(); err == nil {
		m.sealedMsgs = sealed
	}
	if n, err := db.UnreadSealedCount(); err == nil {
		m.sealedUnread = n
	}
	// the entry banner gates the first run of each local profile;
	// acceptance is stored locally, never transmitted
	if _, err := db.GetSetting("entry_accepted"); err != nil {
		m.gateActive = true
	}
	return m, nil
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(append(m.clientCmds(m.cli), statsTick())...)
}

// clientCmds returns the listener commands bound to one ipc client.
func (m *Model) clientCmds(cli *ipc.Client) []tea.Cmd {
	return []tea.Cmd{
		waitIncoming(cli), waitDelivered(cli), waitPeers(cli), waitGone(cli),
		m.subscribeAll(cli), requestPeers(cli),
	}
}

func waitIncoming(cli *ipc.Client) tea.Cmd {
	return func() tea.Msg {
		env, ok := <-cli.Incoming()
		if !ok {
			return daemonGoneMsg{}
		}
		return incomingMsg{env}
	}
}

func waitDelivered(cli *ipc.Client) tea.Cmd {
	return func() tea.Msg {
		env, ok := <-cli.Delivered()
		if !ok {
			return daemonGoneMsg{}
		}
		return deliveredMsg{env}
	}
}

func waitPeers(cli *ipc.Client) tea.Cmd {
	return func() tea.Msg {
		peers, ok := <-cli.PeerUpdates()
		if !ok {
			return daemonGoneMsg{}
		}
		return peerUpdateMsg{peers}
	}
}

func waitGone(cli *ipc.Client) tea.Cmd {
	return func() tea.Msg {
		<-cli.Done()
		return daemonGoneMsg{}
	}
}

func requestPeers(cli *ipc.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		peers, err := cli.PeerList(ctx)
		if err != nil {
			return nil
		}
		return peerUpdateMsg{peers}
	}
}

// subscribeAll (re)subscribes every joined channel.
func (m *Model) subscribeAll(cli *ipc.Client) tea.Cmd {
	ids := make([][32]byte, 0, len(m.channels))
	for _, c := range m.channels {
		ids = append(ids, c.ID)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, id := range ids {
			cli.Subscribe(ctx, id)
		}
		return nil
	}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case incomingMsg:
		m.stats.countIn(msg.env)
		cmd := m.handleIncoming(msg.env)
		return m, tea.Batch(waitIncoming(m.cli), cmd)
	case deliveredMsg:
		m.stats.countIn(msg.env)
		return m, waitDelivered(m.cli)
	case statsTickMsg:
		m.stats.sample()
		return m, statsTick()
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case peerUpdateMsg:
		m.peerList = msg.peers
		sort.Slice(m.peerList, func(i, j int) bool {
			return m.peerList[i].Trust > m.peerList[j].Trust
		})
		m.peerSel = clamp(m.peerSel, 0, len(m.peerList)-1)
		return m, waitPeers(m.cli)
	case daemonGoneMsg:
		if !m.connected {
			return m, nil
		}
		m.connected = false
		m.retryDelay = 1
		m.retryIn = 1
		return m, tea.Batch(retryTick(), pulseTick())
	case retryTickMsg:
		if m.connected {
			return m, nil
		}
		m.retryIn--
		if m.retryIn > 0 {
			return m, retryTick()
		}
		return m, m.attemptReconnect()
	case retryFailMsg:
		m.retryDelay *= 2
		if m.retryDelay > 30 {
			m.retryDelay = 30
		}
		m.retryIn = m.retryDelay
		return m, retryTick()
	case reconnectMsg:
		m.cli = msg.cli
		m.connected = true
		m.retryDelay = 1
		m.status = "reconnected"
		return m, tea.Batch(m.clientCmds(msg.cli)...)
	case pulseTickMsg:
		if m.connected {
			return m, nil
		}
		m.pulse = !m.pulse
		return m, pulseTick()
	case statusMsg:
		m.status = string(msg)
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func retryTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return retryTickMsg{} })
}

func pulseTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return pulseTickMsg{} })
}

// attemptReconnect tries one fresh IPC dial. Compose state is
// untouched; only the connection is replaced.
func (m *Model) attemptReconnect() tea.Cmd {
	socket := m.socket
	return func() tea.Msg {
		cli, err := ipc.Dial(socket)
		if err != nil {
			return retryFailMsg{}
		}
		return reconnectMsg{cli}
	}
}

// handleIncoming routes one pushed envelope into client state.
func (m *Model) handleIncoming(env *proto.Envelope) tea.Cmd {
	if blocked, _ := m.db.IsBlocked(env.Sender); blocked {
		return nil
	}
	switch env.Type {
	case proto.MsgChat:
		chat := &proto.ChatPayload{}
		if proto.DecodePayload(env.Payload, chat) != nil {
			return nil
		}
		m.trackPerson(env.Sender)
		m.messages[chat.ChannelID] = append(m.messages[chat.ChannelID], message{
			id: env.ContentID(), sender: env.Sender, content: chat.Content,
			at: time.Unix(0, env.Timestamp),
		})
		active, ok := m.activeChannel()
		if m.tab == TabMessages && m.msgView == msgViewChat && ok && active.ID == chat.ChannelID {
			if m.follow {
				m.msgSel = len(m.messages[chat.ChannelID]) - 1
			}
		} else {
			m.chatUnread[chat.ChannelID]++
		}
	case proto.MsgPost:
		post := &proto.PostPayload{}
		if proto.DecodePayload(env.Payload, post) != nil {
			return nil
		}
		m.trackPerson(env.Sender)
		if post.ReplyTo != "" {
			m.replies[post.ReplyTo] = append(m.replies[post.ReplyTo], message{
				id: env.ContentID(), sender: env.Sender, content: post.Content,
				at: time.Unix(0, env.Timestamp),
			})
			return nil
		}
		m.posts = append([]feedPost{{
			id: env.ContentID(), sender: env.Sender, content: post.Content,
			at: time.Unix(0, env.Timestamp),
		}}, m.posts...)
		if m.tab != TabFeed {
			m.feedUnread++
		}
	case proto.MsgThread:
		th := &proto.ThreadPayload{}
		if proto.DecodePayload(env.Payload, th) != nil {
			return nil
		}
		m.trackPerson(env.Sender)
		m.threads = append([]forumThread{{
			id: env.ContentID(), sender: env.Sender, title: th.Title,
			category: th.Category, content: th.Content,
			at: time.Unix(0, env.Timestamp),
		}}, m.threads...)
		if m.tab != TabForum {
			m.forumUnread++
		}
	case proto.MsgReply:
		rp := &proto.ReplyPayload{}
		if proto.DecodePayload(env.Payload, rp) != nil {
			return nil
		}
		m.trackPerson(env.Sender)
		m.replies[rp.ParentHash] = append(m.replies[rp.ParentHash], message{
			id: env.ContentID(), sender: env.Sender, content: rp.Content,
			at: time.Unix(0, env.Timestamp),
		})
	case proto.MsgProfile:
		p := &proto.ProfilePayload{}
		if proto.DecodePayload(env.Payload, p) != nil {
			return nil
		}
		m.profiles[env.Sender] = p
		hash := env.ContentID()
		m.db.CacheProfile(env.Sender, env.Payload, hex.EncodeToString(hash[:]))
	case proto.MsgGuestbook:
		gb := &proto.GuestbookPayload{}
		if proto.DecodePayload(env.Payload, gb) != nil {
			return nil
		}
		m.guestbook[gb.TargetPubkey] = append(m.guestbook[gb.TargetPubkey], gbEntry{
			from: env.Sender, msg: gb.Message, at: time.Unix(0, env.Timestamp),
		})
	case proto.MsgStatus:
		m.trackPerson(env.Sender)
	case proto.MsgSealed:
		if env.Recipient != m.id.Pubkey() {
			return nil
		}
		return m.openSealed(env)
	case proto.MsgDelete:
		m.applyDelete(env)
	}
	return nil
}

// applyDelete removes deleted content from local views. The daemon
// already validated authorship before relaying.
func (m *Model) applyDelete(env *proto.Envelope) {
	del := &proto.DeletePayload{}
	if proto.DecodePayload(env.Payload, del) != nil {
		return
	}
	target, err := hex.DecodeString(del.TargetHash)
	if err != nil || len(target) != 32 {
		return
	}
	var hash [32]byte
	copy(hash[:], target)
	for ch, msgs := range m.messages {
		for i, msg := range msgs {
			if msg.id == hash {
				m.messages[ch] = append(msgs[:i], msgs[i+1:]...)
				break
			}
		}
	}
	for i, p := range m.posts {
		if p.id == hash {
			m.posts = append(m.posts[:i], m.posts[i+1:]...)
			break
		}
	}
	for i, t := range m.threads {
		if t.id == hash {
			m.threads = append(m.threads[:i], m.threads[i+1:]...)
			break
		}
	}
}

// openSealed decrypts a sealed message addressed to us, stores it, and
// acknowledges delivery.
func (m *Model) openSealed(env *proto.Envelope) tea.Cmd {
	sp := &proto.SealedPayload{}
	if proto.DecodePayload(env.Payload, sp) != nil {
		return nil
	}
	box := &crypto.SealedBox{
		EphemeralPubkey: sp.EphemeralPubkey, Nonce: sp.Nonce,
		Deniable: sp.Deniable, Ciphertext: sp.Ciphertext,
	}
	content, err := crypto.Open(m.id.PrivateKey, box)
	if err != nil {
		return nil // not for this identity, or corrupt: silence
	}
	if blocked, _ := m.db.IsBlocked(content.SenderPubkey); blocked {
		return nil
	}
	sm := clientdb.SealedMessage{
		MsgHash: env.ContentID(), Data: content.Content,
		Sender: content.SenderPubkey,
	}
	m.db.PutSealed(sm)
	sm.ReceivedAt = time.Now().Unix()
	m.sealedMsgs = append([]clientdb.SealedMessage{sm}, m.sealedMsgs...)
	m.sealedUnread++
	m.trackPerson(content.SenderPubkey)
	hash := env.ContentID()
	return m.sendPayload(proto.MsgSealedAck, env.Sender, &proto.SealedAckPayload{MessageHash: hash}, "")
}

// trackPerson notes a correspondent in the people list.
func (m *Model) trackPerson(pk [32]byte) {
	if pk == m.id.Pubkey() {
		return
	}
	for i := range m.people {
		if m.people[i].pubkey == pk {
			m.people[i].lastSeen = time.Now()
			return
		}
	}
	m.people = append(m.people, person{pubkey: pk, lastSeen: time.Now()})
	sort.Slice(m.people, func(i, j int) bool {
		return m.displayName(m.people[i].pubkey) < m.displayName(m.people[j].pubkey)
	})
}

// displayName resolves: local nickname, then announced DisplayName,
// then truncated pubkey "~ab3f".
func (m *Model) displayName(pk [32]byte) string {
	if n, err := m.db.GetNickname(pk); err == nil && n.Name != "" {
		return n.Name
	}
	if p, ok := m.profiles[pk]; ok && p.DisplayName != "" {
		return p.DisplayName
	}
	if data, err := m.db.GetProfile(pk); err == nil {
		p := &proto.ProfilePayload{}
		if proto.DecodePayload(data, p) == nil {
			m.profiles[pk] = p
			if p.DisplayName != "" {
				return p.DisplayName
			}
		}
	}
	if pk == m.id.Pubkey() && m.id.DisplayName != "" {
		return m.id.DisplayName
	}
	return "~" + hex.EncodeToString(pk[:])[:4]
}

// senderScore maps a sender to a display trust score: [you] handled by
// caller; nicknamed people use their TrustHint; unknowns are new.
func (m *Model) senderScore(pk [32]byte) float64 {
	if n, err := m.db.GetNickname(pk); err == nil {
		return trustScore(n.TrustHint)
	}
	return 0.05
}

// resolveTarget turns a typed reference (nickname or hex pubkey) into
// a pubkey.
func (m *Model) resolveTarget(ref string) ([32]byte, bool) {
	var pk [32]byte
	if nicks, err := m.db.ListNicknames(); err == nil {
		for _, n := range nicks {
			if strings.EqualFold(n.Name, ref) {
				return n.Pubkey, true
			}
		}
	}
	if b, err := hex.DecodeString(strings.TrimPrefix(ref, "~")); err == nil && len(b) == 32 {
		copy(pk[:], b)
		return pk, true
	}
	return pk, false
}

// activeChannel returns the selected channel.
func (m *Model) activeChannel() (clientdb.Channel, bool) {
	if len(m.channels) == 0 || m.channelSel >= len(m.channels) {
		return clientdb.Channel{}, false
	}
	return m.channels[m.channelSel], true
}

// sendPayload signs and submits one payload to the daemon.
func (m *Model) sendPayload(msgType uint8, recipient [32]byte, payload proto.Payload, okStatus string) tea.Cmd {
	data, err := proto.EncodePayload(payload)
	if err != nil {
		return statusCmd("invalid message: " + err.Error())
	}
	env, err := proto.NewEnvelope(m.id.PrivateKey, msgType, recipient, data)
	if err != nil {
		return statusCmd("signing failed: " + err.Error())
	}
	m.stats.countOut(env)
	cli := m.cli
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cli.Send(ctx, env); err != nil {
			return statusMsg("send failed: " + err.Error())
		}
		if okStatus != "" {
			return statusMsg(okStatus)
		}
		return nil
	}
}

func statusCmd(s string) tea.Cmd {
	return func() tea.Msg { return statusMsg(s) }
}

// sendChat sends the input line to the active channel, with local echo.
func (m *Model) sendChat(content string) tea.Cmd {
	ch, ok := m.activeChannel()
	if !ok {
		return statusCmd("no channel joined — /join <name>")
	}
	payload := &proto.ChatPayload{ChannelID: ch.ID, Content: content}
	data, err := proto.EncodePayload(payload)
	if err != nil {
		return statusCmd(err.Error())
	}
	env, err := proto.NewEnvelope(m.id.PrivateKey, proto.MsgChat, ch.ID, data)
	if err != nil {
		return statusCmd(err.Error())
	}
	m.messages[ch.ID] = append(m.messages[ch.ID], message{
		id: env.ContentID(), sender: m.id.Pubkey(), content: content, at: time.Now(),
	})
	if m.follow {
		m.msgSel = len(m.messages[ch.ID]) - 1
	}
	m.stats.countOut(env)
	cli := m.cli
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cli.Send(ctx, env); err != nil {
			return statusMsg("send failed: " + err.Error())
		}
		return nil
	}
}

// ownProfile returns a copy of our profile, or a fresh one.
func (m *Model) ownProfile() *proto.ProfilePayload {
	if p, ok := m.profiles[m.id.Pubkey()]; ok {
		cp := *p
		return &cp
	}
	return &proto.ProfilePayload{DisplayName: m.id.DisplayName}
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
