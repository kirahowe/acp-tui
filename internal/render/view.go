package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/kirahowe/acp-tui/internal/core"
)

// View is the live part of the screen, below the scrollback: entries not
// yet printed, queued messages, the status line, and the input box. input
// is the input box as already drawn, one row per line. The view is at
// most height-1 rows; when the entries do not fit, their top is cut and a
// faint "… N lines above" line takes its place.
func View(s core.State, input, hint string, now time.Time, width, height int) string {
	var prev *core.Entry
	if s.Printed > 0 {
		prev = &s.Entries[s.Printed-1]
	}
	live := Entries(prev, s.Entries[s.Printed:], width, now)

	var bottom []string
	for _, q := range s.Queue {
		first, _, more := strings.Cut(strings.TrimSpace(q), "\n")
		first = oneLine(first)
		if more {
			first += "…"
		}
		bottom = append(bottom, styled(faint, truncate("queued › "+first, width)))
	}
	bottom = append(bottom, StatusText(core.Status(s, now), hint, now, width))
	if input != "" {
		bottom = append(bottom, strings.Split(input, "\n")...)
	}

	rows := max(height-1, 1)
	room := rows - len(bottom)
	if len(live) > room {
		cut := len(live) - room + 1
		if room > 0 {
			live = append([]string{styled(faint, truncate(fmt.Sprintf("… %d lines above", cut), width))}, live[cut:]...)
		} else {
			live = nil
		}
	}
	all := append(live, bottom...)
	if len(all) > rows {
		all = all[len(all)-rows:]
	}
	return strings.Join(all, "\n")
}
