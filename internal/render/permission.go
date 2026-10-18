package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/kirahowe/acp-tui/internal/core"
)

// permissionBody is how many lines of a request's input show before the
// rest is left for the key that shows it all.
const permissionBody = 10

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
// (cut to the room there is, at most permissionBody lines), the options
// with the highlighted one marked, and the keys. Nothing the agent wrote
// is dropped or cut, only wrapped: control and format characters show as
// \u escapes (see core.FaithfulLines). room is how many rows the box may
// take. A request not yet armed shows its options faint.
func permissionBox(s core.State, now time.Time, width, room int) []string {
	p := s.Asks[0]
	inner := InputWidth(width)
	title := core.EscapeLabel(p.Label())
	if t := core.EscapeLabel(p.Title); p.Name != "" && t != "" && t != title {
		title += " · " + t
	}
	var lines []string
	for _, l := range wrapAll([]string{title}, inner) {
		lines = append(lines, styled(bold, l))
	}
	armed := now.Sub(p.Since) >= core.ArmDelay
	var opts []string
	for i, o := range p.Options {
		mark, st := "  ", style(0)
		if i == s.Pick {
			mark, st = "▸ ", bold
		}
		if !armed {
			st |= faint
		}
		text := core.EscapeLabel(o.Name)
		if k := optionKey(o.Kind); k != "" {
			text += " (" + k + ")"
		}
		for j, l := range wrapAll([]string{text}, inner-2) {
			lead := "  "
			if j == 0 {
				lead = mark
			}
			opts = append(opts, styled(st, lead+l))
		}
	}
	hint := "↑↓ enter choose · v see all · esc cancel"
	if len(p.Options) == 0 {
		hint = "nothing to choose · v see all · esc cancel"
	}
	if n := len(s.Asks) - 1; n > 0 {
		hint += fmt.Sprintf(" · %d more waiting", n)
	}
	budget := max(room-2-len(lines)-len(opts)-1, 1)
	// Relay's preview of what the call will do goes above the input, cut
	// the same way. It is relay's text, not the model's, but it can carry
	// untrusted words, so its links defang once the session is
	// contaminated.
	if p.Preview != "" {
		def := p.Labels.Contaminated || s.Labels.Contaminated
		pv := textLines(p.Preview, def, inner)
		show := min(permissionBody, max(budget/2, 1))
		for i, l := range pv {
			if i == show-1 && len(pv) > show {
				lines = append(lines, styled(faint, truncate(fmt.Sprintf("… %d more lines · v to see all", len(pv)-i), inner)))
				show = i + 1
				break
			}
			lines = append(lines, l)
		}
		lines = append(lines, newDomainsLine(s, def, inner, p.Preview)...)
		budget = max(budget-show-1, 1)
	}
	body := min(permissionBody, budget)
	in := wrapAll(core.FaithfulLines(p.Input), inner)
	if len(in) == 0 {
		in = []string{"(no input)"}
	}
	for i, l := range in {
		if i == body-1 && len(in) > body {
			lines = append(lines, styled(faint, truncate(fmt.Sprintf("… %d more lines · v to see all", len(in)-i), inner)))
			break
		}
		lines = append(lines, styled(faint, l))
	}
	lines = append(lines, opts...)
	lines = append(lines, styled(faint, truncate(hint, inner)))
	return box("permission needed", lines, width)
}

// requestLines shows a request's whole input in the transcript, escaped
// but never cut.
func requestLines(e core.Entry, width int) []string {
	var out []string
	for i, l := range wrapAll([]string{core.EscapeLabel(e.Text)}, max(width-4, 1)) {
		lead := "    "
		if i == 0 {
			lead = styled(faint, "  ? ")
		}
		out = append(out, lead+l)
	}
	if e.Preview != "" {
		for _, l := range textLines(e.Preview, e.PreviewDefang, max(width-6, 1)) {
			out = append(out, "      "+l)
		}
	}
	for _, l := range wrapAll(core.FaithfulLines(e.Detail), max(width-6, 1)) {
		out = append(out, styled(faint, "      "+l))
	}
	return out
}
