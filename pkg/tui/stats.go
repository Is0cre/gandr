package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gandr-net/gandr/pkg/proto"
)

// trafficStats is the client's local traffic gauge: envelope byte
// counts over the IPC socket, sampled into rates for the header
// widget. Totals only — it records nothing about senders, recipients,
// or content, and it never leaves the process.
type trafficStats struct {
	inTotal, outTotal uint64
	lastIn, lastOut   uint64
	rateIn, rateOut   float64 // bytes/sec over the last sample
	history           []float64
}

// statsInterval is the sampling period for rate calculation.
const statsInterval = 2 * time.Second

// sparkWidth is how many samples the header sparkline keeps.
const sparkWidth = 12

type statsTickMsg struct{}

func statsTick() tea.Cmd {
	return tea.Tick(statsInterval, func(time.Time) tea.Msg { return statsTickMsg{} })
}

// envelopeSize approximates one envelope's wire size: header,
// payload, signature.
func envelopeSize(env *proto.Envelope) uint64 {
	return uint64(len(env.Payload)) + proto.MinMessageSize
}

func (t *trafficStats) countIn(env *proto.Envelope)  { t.inTotal += envelopeSize(env) }
func (t *trafficStats) countOut(env *proto.Envelope) { t.outTotal += envelopeSize(env) }

// sample converts the deltas since the last tick into rates and rolls
// the sparkline history forward.
func (t *trafficStats) sample() {
	secs := statsInterval.Seconds()
	t.rateIn = float64(t.inTotal-t.lastIn) / secs
	t.rateOut = float64(t.outTotal-t.lastOut) / secs
	t.lastIn, t.lastOut = t.inTotal, t.outTotal
	t.history = append(t.history, t.rateIn+t.rateOut)
	if len(t.history) > sparkWidth {
		t.history = t.history[len(t.history)-sparkWidth:]
	}
}

// fmtRate renders a byte rate compactly: 0, 482B, 2.3K, 1.1M.
func fmtRate(bps float64) string {
	switch {
	case bps < 1:
		return "0"
	case bps < 1024:
		return fmt.Sprintf("%.0fB", bps)
	case bps < 1024*1024:
		return fmt.Sprintf("%.1fK", bps/1024)
	default:
		return fmt.Sprintf("%.1fM", bps/(1024*1024))
	}
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// sparkline renders the rate history as a small bar series, scaled to
// its own maximum. ASCII mode degrades to dots and bars.
func sparkline(history []float64) string {
	if len(history) == 0 {
		return ""
	}
	maxRate := 0.0
	for _, v := range history {
		if v > maxRate {
			maxRate = v
		}
	}
	var b strings.Builder
	for _, v := range history {
		idx := 0
		if maxRate > 0 {
			idx = int(v / maxRate * float64(len(sparkRunes)-1))
		}
		if asciiOnly() {
			if idx > len(sparkRunes)/2 {
				b.WriteRune('|')
			} else if idx > 0 {
				b.WriteRune('.')
			} else {
				b.WriteRune('_')
			}
			continue
		}
		b.WriteRune(sparkRunes[idx])
	}
	return b.String()
}
