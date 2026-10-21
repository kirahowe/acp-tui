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
	// Print is the lines that have scrolled off the top of the frame, to
	// go into scrollback; after them the transcript resumes at PrintTo.
	Print   []string
	PrintTo core.Cursor
	// TooSmall says the window cannot show the request on offer whole with
	// its keys and the status line; the frame says so, and the keys that
	// approve are off.
	TooSmall bool
	// DetailMax is how far the pane's detail view can scroll as drawn.
	DetailMax int
}

// View lays out the live part of the screen. It always fills height-1
// rows, so the input box sits at the bottom of the terminal and the frame
// never changes height (Bubble Tea redraws the whole frame when it does).
// From the top: the transcript not yet in scrollback, queued messages,
// the input box, and the status line. input is the text area as already
// drawn, InputWidth(width) wide. While a permission request waits, it
// takes the input box's place.
//
// The transcript is bottom-aligned, with blank rows above it while it is
// short. Final lines that no longer fit go to Print. Lines that do not
// fit but cannot be printed yet, because an entry above them may still
// change, are cut, and a faint "… N lines above" line takes their place.
// So are lines hidden only because the input box has grown: they come
// back when it shrinks.
//
// With a workspace pane (see pane.go) the frame is the same, except that
// on a wide screen the pane stands to the right of it, and on a narrow one,
// while the pane has focus, the pane takes the transcript's place. The
// lines that go to scrollback are the transcript's own either way.
//
// With s.Help the list of keys takes the transcript's place, unless a
// permission request waits.
func View(s core.State, input, hint string, now time.Time, width, height int) Frame {
	help := func(w int) func(room int) []string {
		if !s.Help || len(s.Asks) > 0 {
			return nil
		}
		return func(room int) []string { return HelpLines(w, room) }
	}
	if Side(width) {
		f := view(s, input, hint, now, MainWidth(width), height, help(MainWidth(width)), false)
		rows := max(height-1, 1)
		pane, mx := paneRender(s, now, PaneWidth(width), rows)
		f.Content = strings.Join(joinColumns(strings.Split(f.Content, "\n"), pane, MainWidth(width)), "\n")
		f.DetailMax = mx
		return f
	}
	if h := help(width); h != nil {
		return view(s, input, hint, now, width, height, h, true)
	}
	if s.Pane.Focus {
		mx := 0
		f := view(s, input, hint, now, width, height, func(room int) []string {
			var lines []string
			lines, mx = paneRender(s, now, width, room)
			return lines
		}, true)
		f.DetailMax = mx
		return f
	}
	return view(s, input, hint, now, width, height, nil, true)
}

// view lays out the frame at width. over, when set, draws the rows
// the transcript would have had; paneHint says the status line names the
// pane's key.
func view(s core.State, input, hint string, now time.Time, width, height int, over func(room int) []string, paneHint bool) Frame {
	lines, at := transcript(s, width, now)

	var bottom []string
	for _, q := range s.Queue {
		first, _, more := strings.Cut(strings.TrimSpace(q), "\n")
		first = oneLine(first)
		if more {
			first += "…"
		}
		bottom = append(bottom, styled(faint, truncate("queued › "+first, width)))
	}
	inputLines := strings.Split(input, "\n")
	inputAt, x := 0, 0
	if len(s.Asks) > 0 {
		// The request takes the input box's place: every key is spoken for
		// while it waits, and the room is better spent on the request. Its
		// input shows in what is left of the screen after the status line,
		// the blank row above and the request's own frame.
		bottom = append(bottom, "")
		bottom = append(bottom, permissionBox(s, now, width, height-1-len(bottom)-1)...)
		inputLines = []string{""}
		inputAt = -1
	} else {
		bottom = append(bottom, "") // the input box stands apart from the text above
		inputAt = len(bottom)
		if boxed(width) {
			inputAt++ // the top border
			x = 2
		}
		bottom = append(bottom, box("", inputLines, width)...)
	}
	st := core.Status(s, now)
	st.PaneHint = paneHint && st.PaneItems > 0 // with nothing to see, the status line stays as it was
	bottom = append(bottom, StatusText(st, hint, now, width))

	rows := max(height-1, 1)
	if len(s.Asks) > 0 && len(bottom) > rows {
		lines := wrap(styled(bold, "Window too small to show this request."), width)
		for _, l := range wrap("Make it larger. Allowing is off until then; esc still cancels.", width) {
			lines = append(lines, styled(faint, l))
		}
		for len(lines) < rows {
			lines = append([]string{""}, lines...)
		}
		return Frame{PrintTo: s.Printed, Content: strings.Join(lines[len(lines)-rows:], "\n"), TooSmall: true}
	}
	room := rows - len(bottom)
	if over != nil {
		// Nothing goes to scrollback while the pane covers the transcript.
		lines = over(max(room, 0))
		at = make([]place, len(lines))
	}
	// Lines go to scrollback only once they would not fit with a one-row
	// input, so typing a long message does not push the transcript away.
	f := Frame{PrintTo: s.Printed}
	if over := len(lines) - max(room+len(inputLines)-1, 0); over > 0 {
		n := 0
		for n < over && at[n].final {
			n++
		}
		if n > 0 {
			f.Print = lines[:n]
			f.PrintTo = at[n-1].next
			lines = lines[n:]
		}
	}

	live := lines
	if len(live) > room {
		cut := len(live) - room + 1
		if room > 0 {
			live = append([]string{styled(faint, truncate(fmt.Sprintf("… %d lines above", cut), width))}, live[cut:]...)
		} else {
			live = nil
		}
	}
	pad := make([]string, max(room-len(live), 0))
	all := append(append(pad, live...), bottom...)
	y := len(pad) + len(live) + inputAt
	if len(all) > rows {
		y -= len(all) - rows
		all = all[len(all)-rows:]
	}
	f.Content = strings.Join(all, "\n")
	f.InputX, f.InputY = x, max(y, 0)
	return f
}

// place is where the line after a transcript line starts, and whether
// the line is final: its entry and every entry before it are.
type place struct {
	next  core.Cursor
	final bool
}

// transcript renders the entries from s.Printed on, one place per line.
// An entry's lines include the blank line above it, if any. After a
// resize the cursor can point past the end of an entry; the rest of that
// entry is then taken as printed.
func transcript(s core.State, width int, now time.Time) ([]string, []place) {
	var lines []string
	var at []place
	var prev *core.Entry
	final := true // no entry so far can still change, even one with no lines
	// The entry above decides the blank line between: the last one that
	// shows, as in Entries.
	for i := min(s.Printed.Entry, len(s.Entries)) - 1; i >= 0; i-- {
		if len(entry(s.Entries[i], width, now)) > 0 {
			prev = &s.Entries[i]
			break
		}
	}
	for i := s.Printed.Entry; i < len(s.Entries); i++ {
		el := Entries(prev, s.Entries[i:i+1], width, now)
		final = final && s.Entries[i].Final
		if len(el) == 0 {
			continue
		}
		prev = &s.Entries[i]
		from := 0
		if i == s.Printed.Entry {
			from = min(s.Printed.Line, len(el))
		}
		for j := from; j < len(el); j++ {
			next := core.Cursor{Entry: i, Line: j + 1}
			if j == len(el)-1 {
				next = core.Cursor{Entry: i + 1}
			}
			lines = append(lines, el[j])
			at = append(at, place{next, final})
		}
	}
	return lines, at
}
