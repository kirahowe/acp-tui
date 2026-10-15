package render

import (
	"strings"
	"time"

	"github.com/kirahowe/acp-tui/internal/core"
)

// Entries renders transcript entries. prev is the entry just before es,
// already shown, or nil at the start; it decides the spacing above the
// first one. now picks the spinner frame for a running tool.
func Entries(prev *core.Entry, es []core.Entry, width int, now time.Time) []string {
	var out []string
	for i := range es {
		lines := entry(es[i], width, now)
		if len(lines) == 0 {
			continue
		}
		if prev != nil && blankBetween(*prev, es[i]) {
			out = append(out, "")
		}
		out = append(out, lines...)
		prev = &es[i]
	}
	return out
}

// blankBetween says whether a blank line separates two entries. A user
// message stands apart; an agent message is separated from what came
// before it, except tool lines and notes, which sit directly above it.
// Two blocks of one message are separated as markdown blocks are.
func blankBetween(prev, e core.Entry) bool {
	switch {
	case prev.Kind == core.User || e.Kind == core.User:
		return true
	case e.Kind == core.Agent:
		return prev.Kind == core.Agent
	}
	return false
}

func entry(e core.Entry, width int, now time.Time) []string {
	switch e.Kind {
	case core.User:
		return userLines(e.Text, width)
	case core.Agent:
		return Markdown(e.Text, width)
	case core.Tool:
		return []string{toolLine(e, width, now)}
	}
	text := e.Text
	if e.Detail != "" {
		text += " · " + e.Detail
	}
	return wrap(styled(faint, oneLine(text)), width)
}

// userLines shows the user's text as typed, after a faint ›.
func userLines(text string, width int) []string {
	var out []string
	for _, line := range strings.Split(sanitize(text), "\n") {
		out = append(out, wrap(line, width-2)...)
	}
	return indent(out, styled(faint, "›")+" ", "  ", width)
}

// toolLine is one line per call: a glyph, the title and, for a failed or
// stopped call, why. A failed call is not faint, so it stands out.
func toolLine(e core.Entry, width int, now time.Time) string {
	glyph, detail, st := "○", "", faint
	switch e.Status {
	case core.Running:
		glyph = spinner(now)
	case core.OK:
		glyph = "✓"
	case core.Failed:
		glyph, detail, st = "✗", e.Detail, 0
	case core.Stopped:
		detail = "stopped"
	}
	line := "  " + glyph + " " + oneLine(e.Title)
	if detail != "" {
		line += " · " + oneLine(detail)
	}
	return styled(st, truncate(line, width))
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner is the frame for now: it moves every 100 ms while the TUI is
// alive, whatever the agent is doing.
func spinner(now time.Time) string {
	n := int64(len(spinnerFrames))
	return spinnerFrames[((now.UnixMilli()/100)%n+n)%n] // a time before 1970 is negative
}
