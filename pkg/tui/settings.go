package tui

import (
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Version is stamped by the client binary for the About section.
var Version = "dev"

// renderSettings renders the Settings tab: a section list, and the
// selected section's pane. Sections that cannot be edited yet are
// read-only by design; nothing here ever touches the network, and
// there is no telemetry to configure because there is no telemetry.
func (m *Model) renderSettings(w, h int) string {
	var b []string
	b = append(b, " "+divider("SETTINGS", w-2), "")

	if !m.settingsIn {
		for i, name := range settingsSections {
			summary := m.sectionSummary(i)
			line := " " + sFg3.Render(padRight(name, 16))
			if summary != "" {
				line += sFg5.Render(summary)
			}
			if i == m.settingsSec {
				line = sFg1.Render("▶") + line
			} else {
				line = " " + line
			}
			b = append(b, line)
		}
		b = append(b, "", " "+sFg5.Render("j/k=select  Enter=open"))
		return strings.Join(clipLines(b, h), "\n")
	}

	b = append(b, " "+sFg4.Render("Settings "+glyph("→", ">")+" ")+sFg1.Render(settingsSections[m.settingsSec]), "")
	b = append(b, m.sectionPane(w)...)
	b = append(b, "", " "+sFg5.Render("Esc=back"))
	return strings.Join(clipLines(b, h), "\n")
}

// sectionSummary is the one-line value shown next to a section name.
func (m *Model) sectionSummary(sec int) string {
	switch sec {
	case secAppearance:
		return "theme: " + theme.Name
	case secNetwork:
		state := "connected"
		if !m.connected {
			state = "offline"
		}
		return state + " · " + strconv.Itoa(len(m.peerList)) + " peers"
	case secIdentity:
		return m.displayName(m.id.Pubkey())
	case secAbout:
		return "gandr " + Version
	}
	return ""
}

// sectionPane renders the body of the open section.
func (m *Model) sectionPane(w int) []string {
	switch m.settingsSec {
	case secAppearance:
		return m.appearancePane()
	case secNetwork:
		return m.networkPane()
	case secIdentity:
		return m.identityPane()
	case secNotifications:
		return []string{
			"  " + sFg3.Render("no notification system yet."),
			"  " + sFg5.Render("when one exists it will be local: no push services,"),
			"  " + sFg5.Render("no telemetry, no analytics. ever."),
		}
	case secStorage:
		return m.storagePane()
	case secKeybindings:
		return m.keybindingsPane()
	case secAdvanced:
		return []string{
			"  " + sFg1.Render("▶ ") + sFg3.Render("re-read the entry manifesto") + sFg5.Render("  Enter"),
			"",
			"  " + sFg5.Render("protocol and daemon parameters live in the daemon's"),
			"  " + sFg5.Render("config file, deliberately out of reach of this UI."),
		}
	case secAbout:
		return m.aboutPane(w)
	}
	return nil
}

func (m *Model) appearancePane() []string {
	var b []string
	b = append(b, "  "+sFg5.Render("theme — local display choice, stored encrypted, never transmitted"), "")
	for i, t := range themes {
		swatch := lipgloss.NewStyle().Foreground(t.Fg1).Render("██") +
			lipgloss.NewStyle().Foreground(t.Fg3).Render("██") +
			lipgloss.NewStyle().Foreground(t.Accent).Render("██")
		line := " " + sFg3.Render(padRight(t.Name, 10)) + " " + swatch
		if t.Name == theme.Name {
			line += sFg1.Render("  active")
		}
		if i == m.settingsItem {
			line = sFg1.Render("▶") + line
		} else {
			line = " " + line
		}
		b = append(b, line)
	}
	b = append(b, "", "  "+sFg5.Render("NO_COLOR and 16-color terminals degrade automatically"))
	return b
}

func (m *Model) networkPane() []string {
	state := "connected"
	if !m.connected {
		state = "offline"
	}
	return []string{
		"  " + sFg5.Render("daemon socket: ") + sFg3.Render(m.socket),
		"  " + sFg5.Render("connection:    ") + sFg3.Render(state),
		"  " + sFg5.Render("peers:         ") + sFg3.Render(strconv.Itoa(len(m.peerList))),
		"  " + sFg5.Render("traffic:       ") + sFg3.Render(
			glyph("↑", "^")+fmtTotal(m.stats.outTotal)+" "+glyph("↓", "v")+fmtTotal(m.stats.inTotal)),
		"",
		"  " + sFg5.Render("transport settings belong to gandrd's config file;"),
		"  " + sFg5.Render("the Network tab shows live peer diagnostics."),
	}
}

func (m *Model) identityPane() []string {
	pk := m.id.Pubkey()
	return []string{
		"  " + sFg5.Render("display name: ") + sFg3.Render(m.displayName(pk)),
		"  " + sFg5.Render("public key:   ") + sFg3.Render(hex.EncodeToString(pk[:])),
		"",
		"  " + sFg5.Render("your identity is this key. it lives only in your"),
		"  " + sFg5.Render("encrypted keyfile — the daemon never sees it."),
		"",
		"  " + sFg5.Render("edit your profile: People "+glyph("→", ">")+" you "+glyph("→", ">")+" e"),
	}
}

func (m *Model) storagePane() []string {
	channels := strconv.Itoa(len(m.channels))
	sealed := strconv.Itoa(len(m.sealedMsgs))
	return []string{
		"  " + sFg5.Render("channels joined:  ") + sFg3.Render(channels),
		"  " + sFg5.Render("sealed messages:  ") + sFg3.Render(sealed),
		"",
		"  " + sFg5.Render("nicknames, notes, blocks, and sealed messages live in"),
		"  " + sFg5.Render("the local encrypted database. they never leave it."),
	}
}

func (m *Model) keybindingsPane() []string {
	rows := [][2]string{
		{"1-6 / Tab", "switch tabs"},
		{"j k g G", "move / top / bottom"},
		{"Enter", "open / send"},
		{"i", "sealed inbox (in Messages)"},
		{"n p s r", "nickname · profile · seal · reply"},
		{"\\", "toggle sidebar"},
		{"?", "help overlay"},
		{"q", "quit"},
	}
	var b []string
	for _, r := range rows {
		b = append(b, "  "+sFg3.Render(padRight(r[0], 12))+sFg5.Render(r[1]))
	}
	b = append(b, "", "  "+sFg5.Render("rebinding is not supported yet"))
	return b
}

func (m *Model) aboutPane(w int) []string {
	var b []string
	b = append(b, strings.Split(renderLogoArt(w), "\n")...)
	b = append(b, "",
		lipgloss.PlaceHorizontal(w, lipgloss.Center, sFg3.Render("gandr "+Version)),
		lipgloss.PlaceHorizontal(w, lipgloss.Center, sFg5.Render("a federated, censorship-resistant communication network")),
		"",
		lipgloss.PlaceHorizontal(w, lipgloss.Center, sFg5.Render("Enter = re-read the entry manifesto")),
	)
	return b
}
