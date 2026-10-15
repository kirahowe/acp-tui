package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/kirahowe/acp-tui/internal/core"
)

// Frame is the live part of the screen, below the scrollback.
type Frame struct {
	Content string
	// InputX and InputY are where the input's first row starts, in
	// columns and rows from the frame's top left, for the cursor.
	InputX, InputY int
}

// View lays out the live part of the screen: entries not yet printed,
// queued messages, the input box, and the status line under it. input is
// the text area as already drawn, InputWidth(width) wide. The frame is at
// most height-1 rows; when the entries do not fit, their top is cut and a
// faint "… N lines above" line takes its place.
func View(s core.State, input, hint string, now time.Time, width, height int) Frame {
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
	bottom = append(bottom, "") // the input box stands apart from the text above
	inputLines := strings.Split(input, "\n")
	inputAt := len(bottom)
	x := 0
	if boxed(width) {
		inputAt++ // the top border
		x = 2
	}
	bottom = append(bottom, box("", inputLines, width)...)
	bottom = append(bottom, StatusText(core.Status(s, now), hint, now, width))

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
	y := len(live) + inputAt
	if len(all) > rows {
		y -= len(all) - rows
		all = all[len(all)-rows:]
	}
	return Frame{Content: strings.Join(all, "\n"), InputX: x, InputY: max(y, 0)}
}
