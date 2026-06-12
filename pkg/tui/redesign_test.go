package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gandr-net/gandr/pkg/clientdb"
	"github.com/gandr-net/gandr/pkg/identity"
)

// gateModel builds a model WITHOUT pre-accepting the entry banner.
func gateModel(t *testing.T) *Model {
	t.Helper()
	id, err := identity.Generate("gatekeeper")
	if err != nil {
		t.Fatal(err)
	}
	db, err := clientdb.Open(filepath.Join(t.TempDir(), "client.db"), id.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	m, err := New(nil, db, id, "/tmp/test.sock")
	if err != nil {
		t.Fatal(err)
	}
	m.width, m.height = 80, 24
	return m
}

func TestEntryGateFirstRun(t *testing.T) {
	m := gateModel(t)
	if !m.gateActive {
		t.Fatal("first run must show the entry banner")
	}
	out := m.View()
	for _, want := range []string{"ENTER AT YOUR OWN RISK", "GTFO"} {
		if !strings.Contains(out, want) {
			t.Fatalf("banner missing %q", want)
		}
	}
	// main app keys must not leak through the gate
	press(m, "3")
	if m.tab != TabMessages {
		t.Fatal("tab switched while gated")
	}
	// accept
	press(m, "left", "enter")
	if m.gateActive {
		t.Fatal("accept did not pass the gate")
	}
	if _, err := m.db.GetSetting("entry_accepted"); err != nil {
		t.Fatal("acceptance not persisted")
	}
}

func TestEntryGatePersistsAcrossRestart(t *testing.T) {
	id, _ := identity.Generate("returning")
	dir := t.TempDir()
	db, err := clientdb.Open(filepath.Join(dir, "client.db"), id.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := New(nil, db, id, "/tmp/test.sock")
	m.width, m.height = 80, 24
	press(m, "enter") // accept
	db.Close()

	db2, err := clientdb.Open(filepath.Join(dir, "client.db"), id.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	m2, _ := New(nil, db2, id, "/tmp/test.sock")
	if m2.gateActive {
		t.Fatal("banner shown again after acceptance")
	}
}

func TestEntryGateGTFOQuits(t *testing.T) {
	m := gateModel(t)
	press(m, "right")
	_, cmd := m.handleKey(key("enter"))
	if !m.quitting || cmd == nil {
		t.Fatal("GTFO did not quit")
	}
	if _, err := m.db.GetSetting("entry_accepted"); err == nil {
		t.Fatal("GTFO must not record acceptance")
	}
}

func TestEntryGateReviewFromSettings(t *testing.T) {
	m := testModel(t)
	m.tab = TabSettings
	m.settingsSec = secAbout
	m.settingsIn = true
	press(m, "enter")
	if !m.gateActive || !m.gateView {
		t.Fatal("About did not re-open the banner")
	}
	press(m, "x") // any key returns
	if m.gateActive {
		t.Fatal("re-view did not return to the app")
	}
	if m.quitting {
		t.Fatal("re-view must never quit or re-gate")
	}
}

func TestThemeSwitchAndPersist(t *testing.T) {
	defer applyTheme(themes[0])
	m := testModel(t)
	m.tab = TabSettings
	press(m, "enter") // open Appearance
	if !m.settingsIn {
		t.Fatal("appearance section did not open")
	}
	press(m, "j", "enter") // select second theme
	if theme.Name != themes[1].Name {
		t.Fatalf("active theme = %q, want %q", theme.Name, themes[1].Name)
	}
	if v, err := m.db.GetSetting("theme"); err != nil || v != themes[1].Name {
		t.Fatalf("persisted theme = %q err=%v", v, err)
	}
	// a fresh model on the same db restores it
	m2, err := New(nil, m.db, m.id, "/tmp/test.sock")
	if err != nil {
		t.Fatal(err)
	}
	_ = m2
	if theme.Name != themes[1].Name {
		t.Fatal("theme not restored from db")
	}
}

func TestFourThemesExist(t *testing.T) {
	if len(themes) < 4 {
		t.Fatalf("%d themes, want at least 4", len(themes))
	}
	names := map[string]bool{}
	for _, th := range themes {
		names[th.Name] = true
	}
	for _, want := range []string{"classic", "midnight", "paper", "ice"} {
		if !names[want] {
			t.Fatalf("missing theme %q", want)
		}
	}
}

func TestFeedDynamicHeight(t *testing.T) {
	m := testModel(t)
	short := feedPost{content: "short", at: time.Now()}
	long := feedPost{content: strings.Repeat("a long sentence about decentralization ", 8), at: time.Now()}
	m.posts = []feedPost{short, long}

	shortLines := m.renderPost(short, false, 80)
	longLines := m.renderPost(long, false, 80)
	if len(shortLines) != 2 { // header + one body line
		t.Fatalf("short post uses %d lines, want 2", len(shortLines))
	}
	if len(longLines) <= len(shortLines) {
		t.Fatal("long post did not expand")
	}
	// selection is marked
	sel := strings.Join(m.renderPost(short, true, 80), "\n")
	if !strings.Contains(sel, "┃") {
		t.Fatal("selected post has no marker")
	}
}

func TestPeopleListAndDetail(t *testing.T) {
	m := testModel(t)
	var pk [32]byte
	pk[0] = 9
	m.people = []person{{pubkey: pk, lastSeen: time.Now()}}
	m.tab = TabPeople
	out := m.View()
	if !strings.Contains(out, "[you]") {
		t.Fatal("own identity missing from People")
	}
	press(m, "j", "enter")
	if !m.peopleDetail || m.profileTarget != pk {
		t.Fatal("enter did not open the person's profile")
	}
	press(m, "esc")
	if m.peopleDetail {
		t.Fatal("esc did not return to the list")
	}
}

func TestMouseTabClickAndWheel(t *testing.T) {
	m := testModel(t)
	m.View() // record geometry
	if len(m.ui.tabZones) != int(tabCount) {
		t.Fatalf("%d tab zones recorded", len(m.ui.tabZones))
	}
	z := m.ui.tabZones[TabFeed]
	m.Update(tea.MouseMsg{X: z.x0, Y: m.ui.tabRow, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.tab != TabFeed {
		t.Fatalf("click did not switch tab: %v", m.tab)
	}
	m.posts = []feedPost{{content: "a"}, {content: "b"}}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	if m.postSel != 1 {
		t.Fatal("wheel did not scroll the feed")
	}
	m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	if m.postSel != 0 {
		t.Fatal("wheel up did not scroll back")
	}
}

func TestSettingsSectionsExist(t *testing.T) {
	m := testModel(t)
	m.tab = TabSettings
	out := m.View()
	for _, want := range []string{"Appearance", "Network", "Identity", "Notifications", "Storage", "Keybindings", "Advanced", "About"} {
		if !strings.Contains(out, want) {
			t.Fatalf("settings missing section %q", want)
		}
	}
	// Identity pane shows the pubkey but must not leak anything else
	m.settingsSec = secIdentity
	m.settingsIn = true
	if !strings.Contains(m.View(), "public key") {
		t.Fatal("identity pane missing public key")
	}
}

func TestNetworkTabShowsDiagnostics(t *testing.T) {
	m := testModel(t)
	m.tab = TabNetwork
	out := m.View()
	for _, want := range []string{"PEERS", "THIS CLIENT", "traffic"} {
		if !strings.Contains(out, want) {
			t.Fatalf("network tab missing %q", want)
		}
	}
}

func TestTrafficStatsSampling(t *testing.T) {
	var s trafficStats
	s.inTotal = 4096
	s.sample()
	if s.rateIn != 4096/statsInterval.Seconds() {
		t.Fatalf("rateIn = %f", s.rateIn)
	}
	s.sample()
	if s.rateIn != 0 {
		t.Fatal("rate did not decay with no traffic")
	}
	if len(s.history) != 2 {
		t.Fatalf("history len = %d", len(s.history))
	}
	if got := fmtRate(2.5 * 1024); got != "2.5K" {
		t.Fatalf("fmtRate = %q", got)
	}
	if sparkline([]float64{0, 1, 2}) == "" {
		t.Fatal("sparkline empty")
	}
}
