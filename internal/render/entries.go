package render

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/kirahowe/acp-tui/internal/core"
)

// Entries renders transcript entries. prev is the entry just before es,
// already shown, or nil at the start; it decides the spacing above the
// first one. now picks the spinner frame for a running tool.
func Entries(prev *core.Entry, es []core.Entry, width int, now time.Time) []string {
	return entriesAs(prev, es, expand{}, width, now)
}

// expand says which kinds of block show in full, as in the transcript
// view.
type expand struct{ tools, thinking bool }

func entriesAs(prev *core.Entry, es []core.Entry, x expand, width int, now time.Time) []string {
	var out []string
	for i := range es {
		lines := entryAs(es[i], x, width, now)
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

// blankBetween says whether a blank line separates two entries. Every
// entry stands apart, as markdown blocks do, except a note, which sits
// directly under the reply or tool calls it is about.
func blankBetween(prev, e core.Entry) bool {
	return e.Kind != core.Note || prev.Kind == core.User || prev.Kind == core.Banner
}

func entry(e core.Entry, width int, now time.Time) []string {
	return entryAs(e, expand{}, width, now)
}

func entryAs(e core.Entry, x expand, width int, now time.Time) []string {
	if e.Defang {
		e.Defang = false
		return defangLines(entryAs(defanged(e), x, width, now))
	}
	switch e.Kind {
	case core.User:
		return userLines(e.Text, width)
	case core.Agent:
		return Markdown(e.Text, width)
	case core.Tool:
		if x.tools {
			return detailLines(e.Calls, width, now)
		}
		return []string{runLine(e.Calls, width, now)}
	case core.Thought:
		return thoughtLines(e, x.thinking, width)
	case core.Banner:
		return bannerLines(e, width)
	case core.Request:
		return requestLines(e, width)
	}
	text := e.Text
	if e.Detail != "" {
		text += " · " + e.Detail
	}
	return wrap(styled(faint, oneLine(text)), width)
}

// userLines shows the user's text as typed, after a faint ›, in the box
// it was typed in, so the user's turns stand apart from the agent's.
func userLines(text string, width int) []string {
	inner := InputWidth(width)
	var out []string
	for _, line := range strings.Split(sanitize(text), "\n") {
		out = append(out, wrap(line, inner-2)...)
	}
	return box("", indent(out, styled(faint, "›")+" ", "  ", inner), width)
}

// thoughtLines is the agent's thinking: in full, faint and in italics,
// or one faint line that says it is there.
func thoughtLines(e core.Entry, full bool, width int) []string {
	if !full {
		word := "thought"
		if !e.Final {
			word = "thinking"
		}
		return []string{styled(faint, truncate("  ∴ "+word, width))}
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(sanitize(e.Text)), "\n") {
		out = append(out, wrap(styled(faint|italic, line), max(width-4, 1))...)
	}
	return indent(out, styled(faint, "  ∴ "), "    ", width)
}

// runLine is a run of tool calls on one line: a glyph for the run, then
// the tools by name, in order, with a count when one was called more
// than once. A failure shows, and is not faint: a lone failed call says
// why, else how many failed.
func runLine(calls []core.Call, width int, now time.Time) string {
	var names []string
	count := map[string]int{}
	failed, stopped, running, pending := 0, 0, 0, 0
	for _, c := range calls {
		name := label(c)
		if count[name] == 0 {
			names = append(names, name)
		}
		count[name]++
		switch c.Status {
		case core.Failed:
			failed++
		case core.Stopped:
			stopped++
		case core.Running:
			running++
		case core.Pending:
			pending++
		}
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n
		if count[n] > 1 {
			parts[i] += fmt.Sprintf(" ×%d", count[n])
		}
	}
	glyph, st := "✓", faint
	switch {
	case running > 0:
		glyph = spinner(now)
	case pending > 0:
		glyph = "○"
	case failed > 0:
		glyph, st = "✗", 0
	}
	line := "  " + glyph + " " + strings.Join(parts, " · ")
	// What went wrong outlasts the names when the line is cut: a lone
	// failed call's reason may be cut, but a count is kept whole.
	var tail string
	switch {
	case failed == 1 && len(calls) == 1:
		if why := firstLine(calls[0].Output); why != "" {
			line += " · " + why
		}
	case failed > 0:
		tail = fmt.Sprintf(" · %d failed", failed)
	}
	if stopped > 0 {
		tail += " · stopped"
	}
	if room := width - ansi.StringWidth(tail); room >= minInner {
		return styled(st, truncate(line, room)+tail)
	}
	return styled(st, truncate(line+tail, width))
}

// label names a call on screen: the tool's name, else its title, else
// "tool".
func label(c core.Call) string {
	if l := strings.TrimSpace(oneLine(c.Label())); l != "" {
		return l
	}
	return "tool"
}

// detailOutput is how many lines of a call's output show in full.
const detailOutput = 8

// detailLines shows each call of a run in full: a glyph and the tool's
// name, then its arguments one per line, then the start of its output.
func detailLines(calls []core.Call, width int, now time.Time) []string {
	var out []string
	for _, c := range calls {
		glyph := "✓"
		switch c.Status {
		case core.Running:
			glyph = spinner(now)
		case core.Pending:
			glyph = "○"
		case core.Failed:
			glyph = "✗"
		case core.Stopped:
			glyph = "○"
		}
		head := "  " + glyph + " " + label(c)
		if c.Status == core.Stopped {
			head += " · stopped"
		}
		out = append(out, truncate(head, width))
		for _, a := range args(c.Input) {
			out = append(out, styled(faint, truncate("      "+a, width)))
		}
		lines := strings.Split(strings.TrimRight(sanitize(c.Output), "\n"), "\n")
		if c.Output == "" {
			continue
		}
		// A failure's output is not faint: it says what went wrong.
		text := faint
		if c.Status == core.Failed {
			text = 0
		}
		for i, l := range lines {
			if i == detailOutput {
				out = append(out, styled(faint, truncate(fmt.Sprintf("      … %d more lines", len(lines)-i), width)))
				break
			}
			lead := "      "
			if i == 0 {
				lead = styled(faint, "    ⎿ ")
			}
			out = append(out, lead+styled(text, truncate(expandTabs(l), width-6)))
		}
	}
	return out
}

// args shows a call's JSON arguments one per line, as key: value, in the
// order sent. Other JSON shows as it is; text that is not JSON, as text.
func args(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(input), &obj) != nil {
		return []string{oneLine(input)}
	}
	var out []string
	dec := json.NewDecoder(strings.NewReader(input))
	dec.Token() // {
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			break
		}
		val := string(v)
		var str string
		if json.Unmarshal(v, &str) == nil {
			val = str
		}
		out = append(out, oneLine(key)+": "+oneLine(val))
	}
	return out
}

// firstLine is the first line of s with text on it.
func firstLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return oneLine(line)
		}
	}
	return ""
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner is the frame for now: it moves every 100 ms while the TUI is
// alive, whatever the agent is doing.
func spinner(now time.Time) string {
	n := int64(len(spinnerFrames))
	return spinnerFrames[((now.UnixMilli()/100)%n+n)%n] // a time before 1970 is negative
}
