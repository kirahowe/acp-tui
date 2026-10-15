package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kirahowe/acp-tui/internal/core"
)

// permissionBody is how many lines of a request's input show before the
// rest is left for the key that shows it all.
const permissionBody = 10

// pretty is a call's JSON arguments indented, one line per member, or the
// text as it is when it is not JSON. It is sanitized: nothing the agent
// sends can move the cursor.
func pretty(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	var b bytes.Buffer
	if json.Indent(&b, []byte(input), "", "  ") != nil {
		b.Reset()
		b.WriteString(input)
	}
	return strings.Split(strings.TrimRight(sanitize(b.String()), "\n"), "\n")
}

// wrapAll wraps each line, keeping its leading spaces on the lines it
// breaks into.
func wrapAll(lines []string, width int) []string {
	var out []string
	for _, l := range lines {
		lead := len(l) - len(strings.TrimLeft(l, " "))
		for i, w := range hardwrap(l, max(width-lead, 1)) {
			if i > 0 || lead > 0 {
				w = strings.Repeat(" ", lead) + w
			}
			out = append(out, w)
		}
	}
	return out
}

// optionKey is the key that picks an option outright, or "".
func optionKey(k core.OptionKind) string {
	switch k {
	case core.AllowOnce:
		return "y"
	case core.RejectOnce:
		return "n"
	}
	return ""
}

// permissionBox draws the request on offer: the tool and its input
// (cut to body lines), the options with the highlighted one marked, and
// the keys. A request not yet armed shows its options faint.
func permissionBox(s core.State, now time.Time, width, body int) []string {
	p := s.Asks[0]
	inner := InputWidth(width)
	title := oneLine(p.Label())
	if t := oneLine(p.Title); p.Name != "" && t != "" && t != oneLine(p.Name) {
		title += " · " + t
	}
	lines := []string{styled(bold, truncate(title, inner))}
	in := wrapAll(pretty(p.Input), inner)
	if len(in) == 0 {
		in = []string{"(no input)"}
	}
	for i, l := range in {
		if i == body-1 && len(in) > body {
			lines = append(lines, styled(faint, truncate(fmt.Sprintf("… %d more lines · v to see all", len(in)-i), inner)))
			break
		}
		lines = append(lines, styled(faint, truncate(l, inner)))
	}
	armed := now.Sub(p.Since) >= core.ArmDelay
	for i, o := range p.Options {
		mark, st := "  ", style(0)
		if i == s.Pick {
			mark, st = "▸ ", bold
		}
		if !armed {
			st |= faint
		}
		text := oneLine(o.Name)
		if k := optionKey(o.Kind); k != "" {
			text += " (" + k + ")"
		}
		lines = append(lines, styled(st, truncate(mark+text, inner)))
	}
	hint := "↑↓ enter choose · v see all · esc cancel"
	if n := len(s.Asks) - 1; n > 0 {
		hint += fmt.Sprintf(" · %d more waiting", n)
	}
	lines = append(lines, styled(faint, truncate(hint, inner)))
	return box("permission needed", lines, width)
}

// requestLines shows a request's whole input in the transcript.
func requestLines(e core.Entry, width int) []string {
	head := styled(faint, "  ? ") + truncate(oneLine(e.Text), width-4)
	out := []string{head}
	for _, l := range wrapAll(pretty(e.Detail), max(width-6, 1)) {
		out = append(out, styled(faint, "      "+l))
	}
	return out
}
