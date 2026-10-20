package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"pgregory.net/rapid"

	"github.com/kirahowe/acp-tui/internal/core"
)

func ct(s string) core.CardText { return core.CardText{Text: s, Rev: 1} }

var (
	cardTracker = core.Card{ID: 1840, Kind: core.CardTracker, Status: core.CardActive, Rev: 1842, Owner: "hub",
		Title: ct("Catch up monthly financials"), Done: 3, Total: 8, Labels: core.Labels{Private: true},
		Rows: []core.CardElem{{ID: 1, CardText: ct("BMO")}, {ID: 2, CardText: ct("RBC")}},
		Columns: []core.CardElem{{ID: 1, CardText: ct("Jun")}, {ID: 2, CardText: ct("Jul")},
			{ID: 3, CardText: ct("Aug")}, {ID: 4, CardText: ct("Sep")}},
		Cells: []core.Cell{{Row: 1, Column: 1, State: core.CellDone}, {Row: 1, Column: 2, State: core.CellDone},
			{Row: 2, Column: 1, State: core.CellSkip}}}
	cardChecklist = core.Card{ID: 1851, Kind: core.CardChecklist, Status: core.CardActive, Rev: 1853, Owner: "hub",
		Title: ct("How to work through it"), Done: 1, Total: 3, Labels: core.Labels{Private: true, Contaminated: true},
		Items: []core.CardElem{{ID: 1, CardText: ct("Make a folder for the statements"), Done: true},
			{ID: 2, CardText: core.CardText{Text: "Read hxxps://evil[.]test/x?d=1 and approve", Outsider: true, Rev: 1852}},
			{ID: 3, CardText: ct("Import into the app, then the spreadsheet")}}}
	cardNote = core.Card{ID: 1860, Kind: core.CardNote, Status: core.CardActive, Rev: 1860, Owner: "operator",
		Title: ct("Where the statements are"), Labels: core.Labels{Private: true},
		Note: &core.CardText{Text: "BMO: online banking, Documents.\nRBC: mailed.", Rev: 1860}}
	cardUnknown = core.Card{ID: 1870, Kind: "kanban", Status: core.CardActive, Rev: 1870, Owner: "hub",
		Title: ct("Moves"), Labels: core.Labels{Private: true},
		Text: "Card 1870: Moves (kanban) [[card:1870]]\nTodo: pack\nDone: book van"}
)

func boardState(focus bool, cards ...core.Card) core.State {
	s := core.State{Phase: core.Idle, Ready: true}
	s.Pane.Focus = focus
	s.Pane.Board = core.Board{Open: true, Asked: true, Loaded: true, Rev: 1900, Cards: cards}
	if len(cards) > 0 {
		s.Pane.Board.SelID = cards[0].ID
	}
	return s
}

func detail(s core.State, id int64, line, col int) core.State {
	s.Pane.Board.SelID, s.Pane.Board.Detail, s.Pane.Board.Line, s.Pane.Board.Col = id, true, line, col
	return s
}

func TestBoardGolden(t *testing.T) {
	all := boardState(true, cardTracker, cardChecklist, cardNote, cardUnknown)
	cases := map[string]core.State{
		"list":      all,
		"tracker":   detail(all, 1840, 1, 2),
		"checklist": detail(all, 1851, 2, 0),
		"note":      detail(all, 1860, 0, 0),
		"unknown":   detail(all, 1870, 0, 0),
	}
	sending := detail(all, 1851, 1, 0)
	sending.Pane.Board.Sending = 1851
	cases["sending"] = sending
	refused := detail(all, 1851, 1, 0)
	refused.Pane.Board.Message, refused.Pane.Board.Failure = "Refused.", "Item 9 is not on card 1851."
	cases["refused"] = refused
	input := detail(all, 1851, 3, 0)
	input.Pane.Board.Input = &core.BoardInput{Card: 1851, Op: core.CardOp{Op: "edit"}, Prompt: "item 3", Text: "Import"}
	cases["input"] = input
	confirm := all
	confirm.Pane.Board.Confirm = 1840
	cases["confirm"] = confirm
	none := boardState(true)
	none.Pane.Board.Unavailable, none.Pane.Board.Loaded = true, false
	cases["no_board"] = none
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			goldenLines(t, PaneLines(s, t0, 44, 26))
		})
	}
}

func TestBoardSideBySide(t *testing.T) {
	s := detail(boardState(false, cardChecklist), 1851, 0, 0)
	f := View(s, "› ", "ctrl+j for a new line", t0, 130, 20)
	plain := ansi.Strip(f.Content)
	if !strings.Contains(plain, "board (1)") || !strings.Contains(plain, "Make a folder") ||
		!strings.Contains(plain, "ctrl+x to focus") {
		t.Errorf("board not beside the chat:\n%s", plain)
	}
}

// A tracker too wide for one line per row puts each row's cells under
// its name, and every line still fits.
func TestWideTrackerWraps(t *testing.T) {
	c := cardTracker
	c.Columns = nil
	for i := range 24 {
		c.Columns = append(c.Columns, core.CardElem{ID: i + 1, CardText: ct("M" + string(rune('a'+i)))})
	}
	lines := PaneLines(detail(boardState(true, c), c.ID, 1, 23), t0, 40, 30)
	for _, l := range lines {
		if ansi.StringWidth(l) > 40 {
			t.Fatalf("line %q too wide", l)
		}
	}
	joined := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "▸ 1 BMO\n") {
		t.Errorf("row name not on its own line:\n%s", joined)
	}
}

// Outside text shows under the untrusted mark, as relay sent it, with no
// live link.
func TestOutsiderTextIsMarked(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		url := rapid.SampledFrom([]string{"https://evil.test/x?d=1", "www.evil.test", "hxxps://evil[.]test/x"}).Draw(t, "url")
		text := "see " + url + " now"
		c := cardChecklist
		c.Title = core.CardText{Text: text, Outsider: true}
		c.Items = []core.CardElem{{ID: 1, CardText: core.CardText{Text: text, Outsider: true}}}
		w := rapid.IntRange(30, 70).Draw(t, "width")
		for _, s := range []core.State{boardState(true, c), detail(boardState(true, c), c.ID, 1, 0)} {
			plain := ansi.Strip(strings.Join(PaneLines(s, t0, w, 30), "\n"))
			if !strings.Contains(plain, "untrusted") {
				t.Fatalf("no untrusted mark:\n%s", plain)
			}
			if live(plain) {
				t.Fatalf("a live link:\n%s", plain)
			}
		}
	})
}

func genBoardText(t *rapid.T, label string) core.CardText {
	return core.CardText{Text: paneText0(t, label), Outsider: rapid.Bool().Draw(t, label+"Out")}
}

func genBoardCard(t *rapid.T, id int64) core.Card {
	c := core.Card{ID: id, Kind: rapid.SampledFrom([]string{core.CardChecklist, core.CardTracker, core.CardNote, paneText0(t, "kind")}).Draw(t, "kind"),
		Status: core.CardActive, Owner: paneText0(t, "owner"), Title: genBoardText(t, "title"), Text: paneText0(t, "text"),
		Done: rapid.IntRange(0, 9).Draw(t, "done"), Total: rapid.IntRange(0, 9).Draw(t, "total"),
		Complete: rapid.Bool().Draw(t, "complete"), Labels: core.Labels{Contaminated: rapid.Bool().Draw(t, "c")}}
	elems := func(label string) []core.CardElem {
		var out []core.CardElem
		for i := range rapid.IntRange(0, 5).Draw(t, label) {
			out = append(out, core.CardElem{ID: i + 1, CardText: genBoardText(t, label+"Text"), Done: rapid.Bool().Draw(t, label+"Done")})
		}
		return out
	}
	switch c.Kind {
	case core.CardChecklist:
		c.Items = elems("items")
	case core.CardTracker:
		c.Rows, c.Columns = elems("rows"), elems("columns")
		for _, r := range c.Rows {
			for _, k := range c.Columns {
				if st := rapid.SampledFrom([]string{"", core.CellDone, core.CellSkip, paneText0(t, "state")}).Draw(t, "cell"); st != "" {
					c.Cells = append(c.Cells, core.Cell{Row: r.ID, Column: k.ID, State: st})
				}
			}
		}
	case core.CardNote:
		n := genBoardText(t, "note")
		c.Note = &n
	}
	return c
}

// Whatever relay sends, the board draws exactly the room it was given, no
// wider than the width, with no character that could move the cursor,
// start a link or reorder text.
func TestBoardNeverEmitsRawControls(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var cards []core.Card
		for i := range rapid.IntRange(0, 4).Draw(t, "cards") {
			cards = append(cards, genBoardCard(t, int64(i+1)))
		}
		s := boardState(rapid.Bool().Draw(t, "focus"), cards...)
		b := &s.Pane.Board
		if len(cards) > 0 {
			b.SelID = rapid.Int64Range(0, int64(len(cards))).Draw(t, "sel")
			b.Detail = rapid.Bool().Draw(t, "detail")
			b.Line, b.Col = rapid.IntRange(-1, 7).Draw(t, "line"), rapid.IntRange(-1, 7).Draw(t, "col")
			if rapid.Bool().Draw(t, "input") {
				b.Input = &core.BoardInput{Card: cards[0].ID, Prompt: paneText0(t, "prompt"), Text: paneText0(t, "typed")}
			}
		}
		b.Message, b.Failure = paneText0(t, "msg"), paneText0(t, "fail")
		b.Unavailable = rapid.IntRange(0, 9).Draw(t, "unavailable") == 0
		w := rapid.IntRange(1, 70).Draw(t, "width")
		h := rapid.IntRange(0, 40).Draw(t, "height")
		lines := PaneLines(s, t0, w, h)
		if len(lines) != h {
			t.Fatalf("%d lines, want %d", len(lines), h)
		}
		for _, l := range lines {
			if strings.Contains(l, "\x1b]") {
				t.Fatalf("operating system command in %q", l)
			}
			if ansi.StringWidth(l) > w {
				t.Fatalf("line %q is %d wide, limit %d", l, ansi.StringWidth(l), w)
			}
			plain := ansi.Strip(l)
			for _, r := range plain {
				if unprintable(r) && r != 0xFFFD {
					t.Fatalf("raw %U in %q (plain %q)", r, l, plain)
				}
			}
			if strings.ContainsAny(plain, "\r\n") {
				t.Fatalf("line break inside a line: %q", plain)
			}
		}
	})
}

// Every board view's hint names the keys that work there: r and b in the
// list and in each card, and no r where it would do nothing.
func TestBoardHintsNameTheKeys(t *testing.T) {
	all := boardState(true, cardTracker, cardChecklist, cardNote)
	views := map[string]core.State{
		"list":      all,
		"tracker":   detail(all, 1840, 1, 0),
		"checklist": detail(all, 1851, 1, 0),
		"note":      detail(all, 1860, 0, 0),
	}
	for name, s := range views {
		foot := strings.Join(PaneLines(s, t0, 200, 26), " ")
		for _, want := range []string{"r reload", "b workspace", "X archive"} {
			if !strings.Contains(ansi.Strip(foot), want) {
				t.Errorf("%s: no %q in %q", name, want, ansi.Strip(foot))
			}
		}
	}
	if foot := ansi.Strip(strings.Join(PaneLines(all, t0, 200, 26), " ")); !strings.Contains(foot, "e retitle") {
		t.Errorf("list: no e retitle in %q", foot)
	}
	none := boardState(true)
	none.Pane.Board.Unavailable, none.Pane.Board.Loaded = true, false
	if foot := ansi.Strip(strings.Join(PaneLines(none, t0, 200, 26), " ")); strings.Contains(foot, "r reload") {
		t.Errorf("no board offers r: %q", foot)
	}
}
