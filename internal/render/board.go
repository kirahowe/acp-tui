package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/kirahowe/acp-tui/internal/core"
)

// The board view of the workspace pane draws the cards relay sent. relay
// has already made every text safe: hidden characters made visible, and
// outside text defanged and flagged outsider. The board draws that text
// as it came, under an "untrusted" mark where it is outside text. It
// still escapes anything that does not print, and defangs a link left
// live in outside text, so a mistake upstream cannot reach the terminal. It never writes a hyperlink and never
// fetches anything.

// cardLine is a piece of card text on one line, safe to print.
func cardLine(t string, outsider bool) string {
	return strings.ReplaceAll(cardText(t, outsider), "\n", `\n`)
}

// cardText is card text that may run over several lines, safe to print.
// relay has defanged outside text already, so it shows as sent; only a
// link still live in it, which relay would never send, is defanged here.
func cardText(t string, outsider bool) string {
	t = core.EscapeNonPrinting(strings.ReplaceAll(strings.ReplaceAll(t, "\r\n", "\n"), "\t", "    "))
	if outsider && live(t) {
		t = Defang(t)
	}
	return t
}

// cardLines is cardText wrapped to width, keeping its line breaks.
func cardLines(t string, outsider bool, width int) []string {
	var out []string
	for _, l := range strings.Split(cardText(t, outsider), "\n") {
		if l == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrap(l, width)...)
	}
	return out
}

// marked is a card text with the untrusted mark in front when it is
// outside text.
func marked(t core.CardText) string {
	s := cardLine(t.Text, t.Outsider)
	if t.Outsider {
		return untrustedMark() + " " + s
	}
	return s
}

// cardTitle is the card's title, or a stand-in when it is empty.
func cardTitle(c core.Card) string {
	if strings.TrimSpace(c.Title.Text) == "" {
		return "(untitled)"
	}
	return marked(c.Title)
}

// cardMeta is the faint line under a card's title: kind, progress, and
// what it is.
func cardMeta(b core.Board, c core.Card) string {
	words := []string{cardLine(c.Kind, true)}
	switch c.Kind {
	case core.CardChecklist, core.CardTracker:
		words = append(words, fmt.Sprintf("%d of %d done", c.Done, c.Total))
	}
	if c.Complete {
		words = append(words, "complete")
	}
	words = append(words, "card "+strconv.FormatInt(c.ID, 10))
	if c.Owner != "" {
		words = append(words, cardLine(c.Owner, true))
	}
	words = append(words, labelWords(c.Labels)...)
	if b.Sending == c.ID {
		words = append(words, "sending…")
	}
	return strings.Join(words, " · ")
}

// cardBlock is a card's rows in the board's list.
func cardBlock(b core.Board, c core.Card, selected bool, width int) []string {
	marker, st := "  ", style(0)
	if selected {
		marker, st = "▸ ", bold
	}
	title := cardTitle(c)
	if !c.Title.Outsider {
		title = styled(st, title)
	}
	out := hang(marker, title, width)
	return append(out, hang("  ", styled(faint, cardMeta(b, c)), width)...)
}

// cellGlyph is how a tracker cell shows: x done, - skipped, · open.
func cellGlyph(state string) string {
	switch state {
	case core.CellDone:
		return "x"
	case core.CellSkip:
		return "-"
	case core.CellOpen:
		return "·"
	}
	return "?"
}

// grid is a tracker row's cells, the selected one in reverse video when
// sel is its column's index.
func grid(c core.Card, row int, sel int) string {
	var b strings.Builder
	for i, col := range c.Columns {
		if i > 0 {
			b.WriteByte(' ')
		}
		g := cellGlyph(c.CellState(row, col.ID))
		if i == sel {
			g = styled(reverse, g)
		}
		b.WriteString(g)
	}
	return b.String()
}

// cardBody is a card's detail: its title, then its items, rows or text.
// selA and selB are the body rows of the selected line.
func cardBody(b core.Board, c core.Card, focus bool, width int) (out []string, selA, selB int) {
	line := core.BoardLine(b, c)
	col := core.BoardCol(b, c)
	marker := func(i int) string {
		if focus && i == line {
			return "▸ "
		}
		return "  "
	}
	add := func(i int, lines []string) {
		if i == line {
			selA = len(out)
		}
		out = append(out, lines...)
		if i == line {
			selB = len(out)
		}
	}
	title := cardTitle(c)
	if !c.Title.Outsider {
		title = styled(bold, title)
	}
	add(0, append(hang(marker(0), title, width), hang("  ", styled(faint, cardMeta(b, c)), width)...))
	if c.Labels.Contaminated {
		out = append(out, untrustedNote(width)...)
	}
	out = append(out, "")
	switch c.Kind {
	case core.CardChecklist:
		if len(c.Items) == 0 {
			out = append(out, styled(faint, "  no items yet"))
		}
		for i, it := range c.Items {
			box := "[ ] "
			if it.Done {
				box = "[x] "
			}
			add(i+1, hang(marker(i+1)+box, strconv.Itoa(it.ID)+" "+marked(it.CardText), width))
		}
	case core.CardTracker:
		out = append(out, trackerLines(c, col, width)...)
		aligned := true
		nameW := 0
		for _, r := range c.Rows {
			if r.Outsider {
				aligned = false
			}
			nameW = max(nameW, ansi.StringWidth(strconv.Itoa(r.ID)+" "+cardLine(r.Text, false)))
		}
		gridW := max(2*len(c.Columns)-1, 0)
		aligned = aligned && 2+nameW+2+gridW <= width
		if len(c.Rows) == 0 {
			out = append(out, styled(faint, "  no rows yet"))
		}
		for i, r := range c.Rows {
			sel := -1
			if focus && i+1 == line {
				sel = col
			}
			name := strconv.Itoa(r.ID) + " " + marked(r.CardText)
			if aligned {
				pad := strings.Repeat(" ", nameW-ansi.StringWidth(name))
				add(i+1, []string{marker(i+1) + name + pad + "  " + grid(c, r.ID, sel)})
				continue
			}
			lines := hang(marker(i+1), name, width)
			lines = append(lines, hang("    ", grid(c, r.ID, sel), width)...)
			add(i+1, lines)
		}
	case core.CardNote:
		if c.Note.Outsider {
			out = append(out, "  "+untrustedMark())
		}
		for _, l := range cardLines(c.Note.Text, c.Note.Outsider, max(width-2, 1)) {
			out = append(out, "  "+l)
		}
	default:
		// A kind this client does not know shows relay's text form.
		def := c.Labels.Contaminated
		for _, l := range cardLines(c.Text, def, max(width-2, 1)) {
			out = append(out, "  "+l)
		}
	}
	return out, selA, selB
}

// trackerLines names a tracker's columns, the selected one in reverse
// video.
func trackerLines(c core.Card, sel int, width int) []string {
	if len(c.Columns) == 0 {
		return []string{styled(faint, "  no columns yet")}
	}
	var parts []string
	for i, col := range c.Columns {
		p := strconv.Itoa(col.ID) + " " + marked(col.CardText)
		if i == sel {
			p = styled(reverse, strconv.Itoa(col.ID)) + " " + marked(col.CardText)
		}
		parts = append(parts, p)
	}
	return append(hang("  ", styled(faint, "columns: ")+strings.Join(parts, styled(faint, " · ")), width), "")
}

// cellLine names the selected tracker cell and its state.
func cellLine(b core.Board, c core.Card, width int) []string {
	line, col := core.BoardLine(b, c), core.BoardCol(b, c)
	if c.Kind != core.CardTracker || line == 0 || col < 0 {
		return nil
	}
	r, k := c.Rows[line-1], c.Columns[col]
	st := c.CellState(r.ID, k.ID)
	return hang("", styled(faint, "cell: ")+cardLine(r.Text, r.Outsider)+styled(faint, " × ")+
		cardLine(k.Text, k.Outsider)+styled(faint, " · ")+cardLine(st, true), width)
}

// boardParts lays out the board view.
func boardParts(s core.State, width int) parts {
	p := s.Pane
	b := p.Board
	var pr parts
	title := styled(bold, "board")
	if b.Loaded {
		title += styled(faint, fmt.Sprintf(" (%d)", len(b.Cards)))
	}
	keyHint := styled(faint, PaneKey)
	if p.Focus {
		keyHint = styled(bold, PaneKey)
	}
	pad := width - ansi.StringWidth(title) - ansi.StringWidth(keyHint)
	pr.head = []string{title + strings.Repeat(" ", max(pad, 1)) + keyHint}
	hint := func(s string) []string { return wrap(styled(faint, s), width) }

	c, sel := core.BoardCard(b)
	switch {
	case b.Unavailable:
		pr.body = []string{styled(faint, "This agent has no board.")}
	case !b.Loaded && b.Asked:
		pr.body = []string{styled(faint, "loading…")}
	case !b.Loaded:
		pr.body = []string{styled(faint, "not loaded: r to load")}
	case b.Detail && sel >= 0:
		pr.body, pr.selA, pr.selB = cardBody(b, c, p.Focus, width)
	case len(b.Cards) == 0:
		pr.body = []string{styled(faint, "no cards")}
	default:
		for i, x := range b.Cards {
			if i > 0 {
				pr.body = append(pr.body, "")
			}
			if i == sel {
				pr.selA = len(pr.body)
			}
			pr.body = append(pr.body, cardBlock(b, x, p.Focus && i == sel, width)...)
			if i == sel {
				pr.selB = len(pr.body)
			}
		}
	}

	switch {
	case !p.Focus:
		pr.foot = hint(PaneKey + " to focus")
	case b.Input != nil:
		in := b.Input
		pr.foot = hang("", styled(bold, cardLine(in.Prompt, false)+" › ")+cardLine(in.Text, false)+styled(reverse, " "), width)
		pr.foot = append(pr.foot, hint("enter send · esc cancel")...)
	case b.Confirm != 0:
		pr.foot = wrap(styled(bold, fmt.Sprintf("Archive card %d?", b.Confirm))+styled(faint, " y archive · any other key cancels"), width)
	case b.Detail && sel >= 0:
		pr.foot = cellLine(b, c, width)
		switch c.Kind {
		case core.CardChecklist:
			pr.foot = append(pr.foot, hint("↑↓ select · x tick · a add · e edit · X archive · esc back")...)
		case core.CardTracker:
			pr.foot = append(pr.foot, hint("↑↓←→ select · x mark · a row · A column · e rename · E rename column · X archive · esc back")...)
		default:
			pr.foot = append(pr.foot, hint("e retitle · X archive · esc back")...)
		}
	case len(b.Cards) > 0:
		pr.foot = hint("↑↓ select · enter open · X archive · b workspace · esc back")
	default:
		pr.foot = hint("r reload · b workspace · esc back")
	}
	if b.Message != "" {
		msg := wrap(styled(bold, cardLine(b.Message, false)), width)
		if b.Failure != "" {
			msg = append(msg, hang("  ", cardLine(b.Failure, true), width)...)
		}
		pr.foot = append(msg, pr.foot...)
	}
	return pr
}
