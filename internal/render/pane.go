package render

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/kirahowe/acp-tui/internal/core"
)

// The workspace pane is drawn from data relay sent: displays, proposals,
// and what relay answered about the references in the latest reply. Model
// text is never shown as it is, only searched for references. Everything
// that prints goes through core.EscapeLabel or core.EscapeNonPrinting, so
// no control, bidi or format character reaches the terminal, and text from
// an item labelled contaminated sits under an "untrusted" mark with its
// URLs defanged. The pane never writes a hyperlink.

// SideMinWidth is the narrowest drawing width that gets a side pane. The
// shell draws one column short of the screen, so this is a 120-column
// screen. Anything narrower shows the pane as an overlay.
const SideMinWidth = 119

// PaneKey is the key that focuses the pane, as the hints name it.
const PaneKey = "ctrl+x"

// Side reports whether the pane stands beside the transcript at width.
func Side(width int) bool { return width >= SideMinWidth }

// PaneWidth is how wide the side pane is at width.
func PaneWidth(width int) int { return min(max(width/3, 36), 52) }

// gap is the columns between the transcript and the side pane: a space,
// a rule, a space.
const gap = 3

// MainWidth is the width the transcript and the input box are drawn at:
// all of width, or, beside the pane, what is left of it.
func MainWidth(width int) int {
	if Side(width) {
		return width - PaneWidth(width) - gap
	}
	return width
}

// joinColumns puts the pane to the right of the transcript's lines. Each
// left line is padded to left columns.
func joinColumns(lines, pane []string, left int) []string {
	n := max(len(lines), len(pane))
	out := make([]string, n)
	for i := range out {
		l, r := "", ""
		if i < len(lines) {
			l = lines[i]
		}
		if i < len(pane) {
			r = pane[i]
		}
		if w := ansi.StringWidth(l); w < left {
			l += strings.Repeat(" ", left-w)
		}
		out[i] = l + sgr(0) + " " + styled(faint, "│") + " " + r
	}
	return out
}

func untrustedMark() string { return styled(bold|reverse, " untrusted ") }

// line is text on one line, safe to print.
func paneLine(text string, defang bool) string {
	t := core.EscapeLabel(text)
	if defang {
		t = Defang(t)
	}
	return t
}

// paneText is multi-line text, safe to print. Newlines stay.
func paneText(text string, defang bool) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\t", "    ")
	t := core.EscapeNonPrinting(text)
	if defang {
		t = Defang(t)
	}
	return t
}

// textLines wraps paneText to width, keeping its line breaks.
func textLines(text string, defang bool, width int) []string {
	var out []string
	for _, l := range strings.Split(paneText(text, defang), "\n") {
		if l == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrap(l, width)...)
	}
	return out
}

// hang wraps text to width less the lead, which is first on the first
// line and the same width of blanks on the rest.
func hang(first, text string, width int) []string {
	lead := ansi.StringWidth(first)
	rest := strings.Repeat(" ", lead)
	var out []string
	for i, l := range wrap(text, max(width-lead, 1)) {
		if i == 0 {
			out = append(out, first+l)
		} else {
			out = append(out, rest+l)
		}
	}
	return out
}

func labelWords(l core.Labels) []string {
	var w []string
	if l.Private {
		w = append(w, "private")
	}
	if l.Contaminated {
		w = append(w, "contaminated")
	}
	return w
}

// hostText is a host as it shows: defanged where the text is.
func hostText(h string, defang bool) string {
	if defang {
		return strings.ReplaceAll(h, ".", "[.]")
	}
	return h
}

// newDomainsLine names the hosts in texts that no approved or trusted item
// has shown, or nothing.
func newDomainsLine(s core.State, defang bool, width int, texts ...string) []string {
	hosts := s.Pane.NewHosts(texts...)
	if len(hosts) == 0 {
		return nil
	}
	for i, h := range hosts {
		hosts[i] = hostText(paneLine(h, false), defang)
	}
	return hang("  ", styled(bold, "new domain: ")+strings.Join(hosts, ", "), width)
}

// newDomainTag marks a link whose host is new. It goes in front of the
// link, where a line break cannot split it.
func newDomainTag(s core.State, texts ...string) string {
	if len(s.Pane.NewHosts(texts...)) > 0 {
		return styled(bold, "[new domain]") + " "
	}
	return ""
}

// statusWord is a proposal's status.
func statusWord(it core.Item, waitingForYou bool) string {
	st := paneLine(it.Status, false)
	switch {
	case it.Status == core.StatusWaitingProposal && waitingForYou:
		return styled(bold, "waiting for you")
	case st == "":
		return "unknown"
	}
	return st
}

// isCurrent says whether the item is the permission request on offer.
func isCurrent(s core.State, it core.Item) bool {
	if it.Kind != core.ProposalItem || it.Status != core.StatusWaitingProposal || len(s.Asks) == 0 {
		return false
	}
	a := s.Asks[0]
	if it.AskID != 0 {
		return it.AskID == a.ID
	}
	return it.CallID != "" && it.CallID == a.CallID
}

func itemTitle(it core.Item, defang bool) string {
	if it.Kind == core.ProposalItem {
		t := it.Tool
		if strings.TrimSpace(t) == "" {
			t = it.Title
		}
		if strings.TrimSpace(t) == "" {
			t = "tool call"
		}
		return paneLine(t, defang)
	}
	if t := paneLine(it.Title, defang); strings.TrimSpace(t) != "" {
		return t
	}
	return "(untitled)"
}

// firstText is the first line of text with something on it.
func firstText(text string) string {
	for l := range strings.Lines(text) {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// listBlock is an item's rows in the list.
func listBlock(s core.State, it core.Item, selected bool, width int) []string {
	def := it.Labels.Contaminated
	marker := "  "
	st := style(0)
	if selected {
		marker, st = "▸ ", bold
	}
	head := ""
	if def {
		head = untrustedMark() + " "
	}
	var out []string
	switch it.Kind {
	case core.ProposalItem:
		cur := isCurrent(s, it)
		out = hang(marker, head+styled(st, itemTitle(it, def))+styled(faint, " · ")+statusWord(it, cur), width)
		meta := "proposal"
		for _, w := range labelWords(it.Labels) {
			meta += " · " + w
		}
		out = append(out, styled(faint, "  "+meta))
		if it.Preview != "" {
			n := 0
			for _, l := range textLines(it.Preview, true, max(width-4, 1)) {
				if n == 3 {
					out = append(out, styled(faint, "    …"))
					break
				}
				out = append(out, styled(faint, "    "+l))
				n++
			}
		} else {
			for i, l := range core.FaithfulLines(it.Input) {
				if i == 2 {
					out = append(out, styled(faint, "    …"))
					break
				}
				out = append(out, styled(faint, "    "+truncate(paneLine2(l, def), max(width-4, 1))))
			}
		}
	default:
		out = hang(marker, head+styled(st, itemTitle(it, def)), width)
		meta := "shown"
		for _, w := range labelWords(it.Labels) {
			meta += " · " + w
		}
		if !def {
			meta += " · trusted"
		}
		out = append(out, styled(faint, "  "+meta))
		if l := firstText(it.Text); l != "" {
			out = append(out, styled(faint, "  "+truncate(oneLinePane(l, def), max(width-2, 1))))
		}
		for i, src := range it.Sources {
			if i == 3 {
				out = append(out, styled(faint, fmt.Sprintf("  + %d more sources", len(it.Sources)-i)))
				break
			}
			out = append(out, sourceLines(s, src, def, width)...)
		}
	}
	return out
}

// paneLine2 prints a line of already-escaped text, defanged if asked.
func paneLine2(l string, defang bool) string {
	if defang {
		return Defang(l)
	}
	return l
}

// oneLinePane is text on one line, safe to print.
func oneLinePane(text string, defang bool) string {
	return strings.Join(strings.Fields(paneText(text, defang)), " ")
}

// sourceLines is one source of a shown item.
func sourceLines(s core.State, src core.Source, def bool, width int) []string {
	text := paneLine(src.Title, def)
	if text == "" {
		text = "(untitled)"
	}
	text = newDomainTag(s, src.Ref) + text + styled(faint, " · ") + paneLine(src.Ref, def)
	return hang("  ↳ ", text, width)
}

// citationBlock is one reference of the latest reply, as relay answered.
func citationBlock(s core.State, ref string, width int) []string {
	c := s.Pane.Cite(ref)
	def := c.Labels.Contaminated || s.Labels.Contaminated
	name := paneLine(ref, def)
	var text string
	switch c.Status {
	case core.CiteOK:
		head := ""
		if c.Labels.Contaminated {
			head = untrustedMark() + " "
		}
		text = head + paneLine(c.Title, def) + styled(faint, " · "+name)
	case core.CiteMissing:
		text = styled(reverse, " missing ") + " " + name
	case core.CiteExternal:
		text = name
		if c.Target != "" {
			text = newDomainTag(s, c.Target, ref) + text + styled(faint, " → ") + paneLine(c.Target, def)
		}
	default:
		text = styled(faint, "unchecked ") + name
	}
	return hang("  ", text, width)
}

// commandLines shows a command to run in a terminal, hard-wrapped and
// escaped. A command with characters that do not print is shown escaped
// and flagged, since pasting it would not run what is shown.
func commandLines(cmd string, width int) []string {
	var out []string
	shown := core.EscapeNonPrinting(cmd)
	unusual := shown != cmd || strings.Contains(cmd, "\n") || live(cmd)
	if unusual {
		shown = Defang(core.EscapeLabel(cmd))
	}
	for _, l := range hardwrap(shown, max(width-2, 1)) {
		out = append(out, "  "+styled(bold, l))
	}
	if unusual {
		out = append(out, styled(faint, "  it holds characters that do not print: do not paste it"))
	}
	return out
}

// parts are the pieces of a pane, top to bottom: a header, a body that
// may be longer than the room, and a footer. selA and selB are the body
// rows of the selected item, kept in view; scrolls says the body scrolls
// by Pane.Scroll.
type parts struct {
	head, body, foot []string
	selA, selB       int
	scrolls          bool
}

func paneParts(s core.State, now time.Time, width int) parts {
	p := s.Pane
	items := core.PaneItems(s)
	var pr parts
	title := styled(bold, "workspace") + styled(faint, fmt.Sprintf(" (%d)", len(items)))
	keyHint := styled(faint, PaneKey)
	if p.Focus {
		keyHint = styled(bold, PaneKey)
	}
	pad := width - ansi.StringWidth(title) - ansi.StringWidth(keyHint)
	pr.head = []string{title + strings.Repeat(" ", max(pad, 1)) + keyHint}

	hint := func(s string) []string { return wrap(styled(faint, s), width) }
	sel := core.SelectedIndex(s)
	switch {
	case p.Trust != nil:
		pr.body = trustBody(s, width)
		armed := now.Sub(p.Trust.Since) >= core.ArmDelay
		switch {
		case p.Trust.Sent:
			pr.foot = hint("trusting…")
		case armed:
			pr.foot = wrap(styled(bold, "y trust")+styled(faint, " · any other key cancels"), width)
		default:
			pr.foot = hint("y trust (wait a moment) · any other key cancels")
		}
		return pr
	case p.Detail && sel >= 0:
		pr.body = detailBody(s, items[sel], width)
		pr.scrolls = true
		pr.foot = hint("↑↓ scroll · t trust · esc back")
	default:
		for i, it := range items {
			if i > 0 {
				pr.body = append(pr.body, "")
			}
			if i == sel {
				pr.selA = len(pr.body)
			}
			pr.body = append(pr.body, listBlock(s, it, p.Focus && i == sel, width)...)
			if i == sel {
				pr.selB = len(pr.body)
			}
		}
		if len(items) == 0 {
			pr.body = append(pr.body, styled(faint, "nothing yet"))
		}
		if refs := core.LatestRefs(s); len(refs) > 0 {
			pr.body = append(pr.body, "", styled(faint, fmt.Sprintf("cited in the latest reply (%d)", len(refs))))
			for _, ref := range refs {
				pr.body = append(pr.body, citationBlock(s, ref, width)...)
			}
		}
		if p.Focus {
			pr.foot = hint("↑↓ select · enter open · t trust · esc back")
		} else {
			pr.foot = hint(PaneKey + " to focus")
		}
	}
	if p.Message != "" {
		pr.foot = append(wrap(styled(bold, paneLine(p.Message, false)), width), pr.foot...)
		if p.Failure != "" {
			pr.foot = append(hang("  ", paneLine(p.Failure, true), width), pr.foot...)
		}
		if p.Command != "" {
			pr.foot = append(commandLines(p.Command, width), pr.foot...)
		}
	}
	return pr
}

// labelsLine is the faint line of an item's labels.
func labelsLine(it core.Item) string {
	w := labelWords(it.Labels)
	if !it.Labels.Contaminated && it.Kind == core.ShownItem {
		w = append(w, "trusted")
	}
	if len(w) == 0 {
		return ""
	}
	return styled(faint, "  "+strings.Join(w, " · "))
}

// untrustedNote is the line under the mark that says why.
func untrustedNote(width int) []string {
	return hang("  ", untrustedMark()+styled(faint, " outside text: links are defanged"), width)
}

func detailBody(s core.State, it core.Item, width int) []string {
	def := it.Labels.Contaminated
	var out []string
	out = append(out, hang("", styled(bold, itemTitle(it, def)), width)...)
	if def {
		out = append(out, untrustedNote(width)...)
	}
	if l := labelsLine(it); l != "" {
		out = append(out, l)
	}
	inner := max(width-2, 1)
	switch it.Kind {
	case core.ProposalItem:
		out = append(out, styled(faint, "  proposal · ")+statusWord(it, isCurrent(s, it)))
		if it.Preview != "" {
			out = append(out, "", styled(faint, "preview"))
			// A preview is a service's text: its links always defang.
			for _, l := range textLines(it.Preview, true, inner) {
				out = append(out, "  "+l)
			}
		}
		out = append(out, "", styled(faint, "input"))
		in := core.FaithfulLines(it.Input)
		if len(in) == 0 {
			in = []string{"(no input)"}
		}
		for _, l := range wrapAll(in, inner) {
			out = append(out, "  "+paneLine2(l, def))
		}
		for _, l := range fixedLines(it.FixedArgs, def, inner) {
			out = append(out, "  "+l)
		}
		out = append(out, newDomainsLine(s, def || it.Preview != "", width, it.Input, it.Preview)...)
	default:
		var ids []string
		for _, id := range []string{it.Ref, it.Path} {
			if id != "" {
				ids = append(ids, paneLine(id, def))
			}
		}
		if it.Version != "" {
			ids = append(ids, "v"+paneLine(it.Version, def))
		}
		if len(ids) > 0 {
			out = append(out, hang("  ", styled(faint, strings.Join(ids, " · ")), width)...)
		}
		out = append(out, "")
		for _, l := range textLines(it.Text, def, inner) {
			out = append(out, "  "+l)
		}
		if len(it.Sources) > 0 {
			out = append(out, "", styled(faint, "sources"))
			for _, src := range it.Sources {
				out = append(out, sourceLines(s, src, def, width)...)
			}
		}
		texts := []string{it.Text}
		for _, src := range it.Sources {
			texts = append(texts, src.Ref)
		}
		out = append(out, newDomainsLine(s, def, width, texts...)...)
		if !it.CanTrust && def && it.TrustCommand != "" {
			out = append(out, "", styled(faint, "to trust it, run in a terminal"))
			out = append(out, commandLines(it.TrustCommand, width)...)
		}
	}
	return out
}

// trustBody is the confirmation: exactly what will be trusted.
func trustBody(s core.State, width int) []string {
	it := s.Pane.Trust.Item
	inner := max(width-2, 1)
	out := wrap(styled(bold, "Trust this?")+" It clears contaminated from what it says now.", width)
	out = append(out, "", untrustedMark()+" "+itemTitle(it, true))
	var ids []string
	for _, id := range []string{it.Path, "v" + it.Version} {
		ids = append(ids, paneLine(id, true))
	}
	if len(it.SHA256) >= 12 {
		ids = append(ids, "sha256 "+paneLine(it.SHA256[:12], true))
	}
	out = append(out, hang("", styled(faint, strings.Join(ids, " · ")), width)...)
	out = append(out, "")
	for _, l := range textLines(it.Text, true, inner) {
		out = append(out, "  "+l)
	}
	out = append(out, newDomainsLine(s, true, width, it.Text)...)
	return out
}

// paneRender draws the pane exactly height lines tall and no wider than
// width, and says how far its detail view can scroll.
func paneRender(s core.State, now time.Time, width, height int) ([]string, int) {
	height = max(height, 0)
	width = max(width, 1)
	pr := paneParts(s, now, width)
	avail := height - len(pr.head) - len(pr.foot)
	var all []string
	maxScroll := 0
	if avail <= 0 {
		all = append(append([]string(nil), pr.head...), pr.foot...)
	} else {
		maxScroll = max(len(pr.body)-avail, 0)
		start := 0
		switch {
		case pr.scrolls:
			start = min(max(s.Pane.Scroll, 0), maxScroll)
		case len(pr.body) > avail:
			start = max(pr.selB-avail, 0)
			if pr.selA < start {
				start = pr.selA
			}
			start = min(start, maxScroll)
		}
		end := min(start+avail, len(pr.body))
		win := slices.Clone(pr.body[start:end])
		if avail >= 3 {
			if start > 0 {
				win[0] = styled(faint, fmt.Sprintf("↑ %d above", start))
			}
			if end < len(pr.body) {
				win[len(win)-1] = styled(faint, fmt.Sprintf("↓ %d below", len(pr.body)-end+1))
			}
		}
		for len(win) < avail {
			win = append(win, "")
		}
		all = append(append(append([]string(nil), pr.head...), win...), pr.foot...)
	}
	all = all[:min(len(all), height)]
	for i, l := range all {
		all[i] = truncate(l, width)
	}
	for len(all) < height {
		all = append(all, "")
	}
	if !pr.scrolls {
		maxScroll = 0
	}
	return selfContained(all), maxScroll
}

// PaneLines is the pane drawn at width and height.
func PaneLines(s core.State, now time.Time, width, height int) []string {
	lines, _ := paneRender(s, now, width, height)
	return lines
}
