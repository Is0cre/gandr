package tui

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gandr-net/gandr/pkg/crypto"
)

// TestDumpView writes a rendered frame to a file for manual layout
// inspection. Runs only when GANDR_DUMP_VIEW is set.
func TestDumpView(t *testing.T) {
	path := os.Getenv("GANDR_DUMP_VIEW")
	if path == "" {
		t.Skip("set GANDR_DUMP_VIEW=<path> to dump a frame")
	}
	m := testModel(t)
	ch := ChannelID("general")
	m.db.JoinChannel(ch, "general")
	m.channels, _ = m.db.ListChannels()
	_, priv, _ := crypto.GenerateIdentity()
	m.handleIncoming(chatEnvelope(t, priv, ch, "finishing gandrd first. pkg/crypto tests passing"))
	m.handleIncoming(chatEnvelope(t, priv, ch, "check the `sensor data` when you can @byte_me"))
	m.trackPerson([32]byte{0xAB})
	m.people = append(m.people, person{pubkey: [32]byte{0xCD}, lastSeen: time.Now().Add(-time.Hour)})
	m.width, m.height = 120, 42
	if v := os.Getenv("GANDR_DUMP_W"); v != "" {
		fmt.Sscanf(v, "%d", &m.width)
	}
	if v := os.Getenv("GANDR_DUMP_H"); v != "" {
		fmt.Sscanf(v, "%d", &m.height)
	}
	if v := os.Getenv("GANDR_DUMP_TAB"); v != "" {
		var tb int
		fmt.Sscanf(v, "%d", &tb)
		m.tab = Tab(tb)
	}
	if os.Getenv("GANDR_DUMP_GATE") == "1" {
		m.gateActive = true
	}

	out := m.View()
	if err := os.WriteFile(path, []byte(out+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
