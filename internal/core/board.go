package core

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// The board is the workspace pane's second view: the cards relay keeps,
// small structured things the operator works through over days. The
// client holds only what relay last sent. Every key that changes a card
// sends relay an op and waits; the card that comes back, in the answer or
// in a _relay/cards/changed notification, replaces the one held. Nothing
// here works a card's state out for itself, except which op a key means.

// Card kinds this client draws natively. Any other kind shows its text.
const (
	CardChecklist = "checklist"
	CardTracker   = "tracker"
	CardNote      = "note"
)

// Card statuses and tracker cell states, as relay names them.
const (
	CardActive   = "active"
	CardArchived = "archived"
	CellOpen     = "open"
	CellDone     = "done"
	CellSkip     = "skip"
)

// CardText is a piece of a card's text as relay sent it: already made
// visible, and defanged when Outsider says it came from outside.
type CardText struct {
	Text     string
	Outsider bool
	Rev      int64
}

// CardElem is a checklist item, a tracker row or a tracker column.
type CardElem struct {
	ID int
	CardText
	Done bool // a checklist item's
}

// Cell is a tracker cell that is not open.
type Cell struct {
	Row, Column int
	State       string
}

// Card is relay's wire form of a card.
type Card struct {
	ID       int64
	Kind     string
	Status   string
	Complete bool
	Rev      int64
	Owner    string
	AddedAt  string
	Title    CardText
	Done     int
	Total    int
	Labels   Labels
	Items    []CardElem // checklist
	Rows     []CardElem // tracker
	Columns  []CardElem // tracker
	Cells    []Cell     // tracker
	Note     *CardText  // note
	Text     string     // the text form, for a kind the client does not know
}

// Native says the client draws the card's kind itself.
func (c Card) Native() bool {
	switch c.Kind {
	case CardChecklist, CardTracker, CardNote:
		return true
	}
	return false
}

// CellState is the state of the tracker cell at row and column.
func (c Card) CellState(row, column int) string {
	for _, x := range c.Cells {
		if x.Row == row && x.Column == column {
			return x.State
		}
	}
	return CellOpen
}

// Lines is how many selectable lines the card's detail has: the title,
// then each item or row. A note and a card of another kind have only the
// title.
func (c Card) Lines() int {
	switch c.Kind {
	case CardChecklist:
		return 1 + len(c.Items)
	case CardTracker:
		return 1 + len(c.Rows)
	}
	return 1
}

// CardOp is an op as relay takes it: one flat shape for every kind. Nil
// fields are left out.
type CardOp struct {
	Op     string
	Item   *int
	Row    *int
	Column *int
	State  *string
	Text   *string
	Seen   *int64
}

// BoardInput is the one-line input for a card's text: an op waiting for
// the text the operator types. Seen is the rev the operator was looking
// at when the input opened, so an edit made meanwhile elsewhere is
// refused rather than overwritten. Was is the text it started with,
// empty when it started empty.
type BoardInput struct {
	Card   int64
	Op     CardOp
	Prompt string
	Text   string
	Was    string
}

// Board is the board view's state.
type Board struct {
	// Open says the pane shows the board rather than the workspace items.
	Open bool
	// Asked says _relay/cards has been called; Loaded, that it answered.
	// Unavailable says the agent has no such method.
	Asked, Loaded, Unavailable bool
	Rev                        int64
	Cards                      []Card // the active cards, as relay sent them
	// Gone is the rev at which each card left the board, so an older
	// copy that arrives late cannot bring it back.
	Gone map[int64]int64
	// SelID is the selected card; Detail says it fills the pane, with
	// Line the selected line (0 the title, then items or rows) and Col
	// the selected tracker column, by index.
	SelID  int64
	Detail bool
	Line   int
	Col    int
	Input  *BoardInput
	// Confirm is the card waiting for y to archive it.
	Confirm int64
	// Sending is the card an op is on its way for; the op is its name.
	// Sent is the input the op came from, if it carried text, so a
	// refusal can give the operator back what they typed.
	Sending   int64
	SendingOp string
	Sent      *BoardInput
	// Message is the outcome of the last action, and Failure relay's
	// refusal, shown escaped.
	Message string
	Failure string
}

// Events about the board.
type (
	// CardsLoaded is relay's answer to _relay/cards. Unavailable says the
	// agent does not have the method; Err, that the call failed some other
	// way.
	CardsLoaded struct {
		At          time.Time
		Rev         int64
		Cards       []Card
		Unavailable bool
		Err         string
	}
	// CardsChanged is a _relay/cards/changed notification: the cards
	// whose rev moved, archived ones included.
	CardsChanged struct {
		At    time.Time
		Rev   int64
		Cards []Card
	}
	// CardOpDone is relay's answer to _relay/cards/op on card Card: the
	// card as it now stands, or relay's refusal in Err. Unchanged says
	// relay appended nothing because the op would change nothing.
	CardOpDone struct {
		At        time.Time
		CardID    int64
		Card      *Card
		Unchanged bool
		Err       string
	}
)

func (e CardsLoaded) at() time.Time  { return e.At }
func (e CardsChanged) at() time.Time { return e.At }
func (e CardOpDone) at() time.Time   { return e.At }

// Effects asking the shell to call relay about cards.
type (
	RequestCards struct{}
	SendCardOp   struct {
		Card int64
		Op   CardOp
	}
)

func (RequestCards) effect() {}
func (SendCardOp) effect()   {}

// BoardCard is the selected card and its index, or -1.
func BoardCard(b Board) (Card, int) {
	if len(b.Cards) == 0 {
		return Card{}, -1
	}
	for i, c := range b.Cards {
		if c.ID == b.SelID {
			return c, i
		}
	}
	return b.Cards[0], 0
}

// BoardLine is the selected line of card c, kept in range.
func BoardLine(b Board, c Card) int { return min(max(b.Line, 0), c.Lines()-1) }

// BoardCol is the selected tracker column of card c by index, kept in
// range, or -1 when it has none.
func BoardCol(b Board, c Card) int {
	if len(c.Columns) == 0 {
		return -1
	}
	return min(max(b.Col, 0), len(c.Columns)-1)
}

// put replaces the card with c's id by c, unless the one held is newer.
// An archived card leaves the board; a new active one joins it at the end.
func (b *Board) put(c Card) {
	i := slices.IndexFunc(b.Cards, func(x Card) bool { return x.ID == c.ID })
	gone, wasGone := b.Gone[c.ID]
	switch {
	case i >= 0 && b.Cards[i].Rev > c.Rev:
	case wasGone && gone >= c.Rev:
	case c.Status == CardArchived:
		if i >= 0 {
			b.Cards = slices.Delete(b.Cards, i, i+1)
		}
		b.Gone = maps.Clone(b.Gone) // the old state may share the map
		if b.Gone == nil {
			b.Gone = map[int64]int64{}
		}
		b.Gone[c.ID] = c.Rev
	case i >= 0:
		b.Cards[i] = c
	default:
		b.Cards = append(b.Cards, c)
	}
}

// keepBoardSelection keeps the selected card selected, and closes what
// belonged to a card that left the board.
func (r *reducer) keepBoardSelection() {
	b := &r.s.Pane.Board
	if slices.ContainsFunc(b.Cards, func(c Card) bool { return c.ID == b.SelID }) {
		return
	}
	// A refusal of an op on the card no longer holds once the card is gone.
	if (b.Detail || b.Input != nil || b.Confirm != 0) && (b.Message == "" || b.Message == refusedNotice) {
		b.Message, b.Failure = "That card has left the board.", ""
	}
	b.Detail, b.Input, b.Confirm, b.Line, b.Col = false, nil, 0, 0, 0
	if len(b.Cards) > 0 {
		b.SelID = b.Cards[0].ID
	} else {
		b.SelID = 0
	}
}

// boardAnchor is what an open card's cursor is on, by id: the card, the
// item or row (0 for the title) and the tracker column (0 for none),
// with the positions they were at.
type boardAnchor struct {
	card         int64
	line, col    int
	elem, column int
}

// anchorOf is where the open card's cursor is, or false when no card is
// open.
func anchorOf(b Board) (boardAnchor, bool) {
	c, i := BoardCard(b)
	if !b.Detail || i < 0 {
		return boardAnchor{}, false
	}
	a := boardAnchor{card: c.ID, line: BoardLine(b, c), col: BoardCol(b, c)}
	if elems := c.lineElems(); a.line > 0 {
		a.elem = elems[a.line-1].ID
	}
	if a.col >= 0 {
		a.column = c.Columns[a.col].ID
	}
	return a, true
}

// lineElems are the elements behind a card's lines after the title.
func (c Card) lineElems() []CardElem {
	switch c.Kind {
	case CardChecklist:
		return c.Items
	case CardTracker:
		return c.Rows
	}
	return nil
}

// reanchor puts the open card's cursor back on the element it was on
// before relay's newer copy of the card replaced the held one, since an
// item or row added or removed above it moves it. When that element has
// gone, the cursor stays at the nearest position, and a note says so
// (over any older note, but not over a refusal and its reason), so that
// x never acts on a line the operator did not choose without them being
// told.
func (r *reducer) reanchor(a boardAnchor, was bool) {
	b := &r.s.Pane.Board
	c, i := BoardCard(*b)
	if !was || !b.Detail || i < 0 || c.ID != a.card {
		return
	}
	gone := ""
	if a.elem != 0 {
		elems := c.lineElems()
		if j := slices.IndexFunc(elems, func(x CardElem) bool { return x.ID == a.elem }); j >= 0 {
			b.Line = j + 1
		} else {
			b.Line = min(a.line, c.Lines()-1)
			gone = "item"
			if c.Kind == CardTracker {
				gone = "row"
			}
		}
	}
	if a.column != 0 {
		if j := slices.IndexFunc(c.Columns, func(x CardElem) bool { return x.ID == a.column }); j >= 0 {
			b.Col = j
		} else {
			b.Col = max(min(a.col, len(c.Columns)-1), 0)
			if gone == "" {
				gone = "column"
			}
		}
	}
	if gone != "" && b.Failure == "" {
		b.Message = "The selected " + gone + " is gone; the cursor moved to the nearest one."
	}
}

func (r *reducer) board(e Event) {
	b := &r.s.Pane.Board
	anchor, anchored := anchorOf(*b)
	defer func() { r.reanchor(anchor, anchored) }()
	switch e := e.(type) {
	case CardsLoaded:
		switch {
		case e.Unavailable:
			b.Unavailable, b.Loaded, b.Cards = true, false, nil
		case e.Err != "":
			b.Asked = false // reopening the board asks again
			b.Message, b.Failure = "The board did not load.", e.Err
			return
		default:
			held := b.Cards
			b.Loaded, b.Unavailable = true, false
			b.Cards = nil
			for _, c := range e.Cards {
				b.put(c)
			}
			// A notification that overtook the answer is newer: it may
			// replace a card in the answer or bring one the answer
			// predates. put keeps whichever copy is newer and leaves out a
			// card archived since.
			for _, c := range held {
				if c.Rev > e.Rev || slices.ContainsFunc(b.Cards, func(x Card) bool { return x.ID == c.ID }) {
					b.put(c)
				}
			}
			b.Rev = max(b.Rev, e.Rev)
		}
		if b.SelID == 0 && len(b.Cards) > 0 {
			b.SelID = b.Cards[0].ID
		}
		r.keepBoardSelection()
	case CardsChanged:
		for _, c := range e.Cards {
			b.put(c)
		}
		b.Rev = max(b.Rev, e.Rev)
		if b.SelID == 0 && len(b.Cards) > 0 {
			b.SelID = b.Cards[0].ID
		}
		r.keepBoardSelection()
	case CardOpDone:
		sent := b.Sent
		if b.Sending == e.CardID {
			b.Sending, b.SendingOp, b.Sent = 0, "", nil
		}
		if b.Message == busyNotice {
			b.Message = ""
		}
		if e.Err != "" {
			b.Message, b.Failure = refusedNotice, e.Err
			if sent != nil && sent.Card == e.CardID {
				r.reopen(*sent)
			}
			return
		}
		if b.Message == refusedNotice {
			// relay took the op; the refusal before it no longer holds.
			b.Message, b.Failure = "", ""
		}
		if e.Card != nil {
			b.put(*e.Card)
			if e.Unchanged {
				b.Message = "Nothing to change."
			} else if e.Card.Status == CardArchived {
				b.Message = "Archived card " + strconv.FormatInt(e.CardID, 10) + "."
			}
		}
		r.keepBoardSelection()
	}
}

// reopen gives the operator back the text relay refused, in the input
// it was typed in, unless they have since moved on to another input or a
// confirm, or the card has left the board. An edit takes the rev of the
// card as now held, so the operator, who has just read why relay refused,
// can send it again on purpose; Was is cleared so Enter always sends.
func (r *reducer) reopen(in BoardInput) {
	b := &r.s.Pane.Board
	if b.Input != nil || b.Confirm != 0 {
		return
	}
	i := slices.IndexFunc(b.Cards, func(c Card) bool { return c.ID == in.Card })
	if i < 0 {
		return
	}
	if in.Op.Seen != nil {
		in.Op.Seen = int64p(b.Cards[i].Rev)
	}
	in.Op.Text, in.Was = nil, ""
	b.SelID = in.Card
	b.Input = &in
}

// toggleBoard switches the pane between the board and the items, and asks relay for it the
// first time.
func (r *reducer) toggleBoard() {
	p := &r.s.Pane
	b := &p.Board
	p.Detail, p.Scroll = false, 0
	b.Open = !b.Open
	b.Input, b.Confirm = nil, 0
	r.askCards()
}

// askCards asks relay for the board when it is open and has not been
// asked for yet. Before the session exists the ask waits for Connected.
func (r *reducer) askCards() {
	b := &r.s.Pane.Board
	if b.Open && !b.Asked && !b.Unavailable && r.s.Phase != Starting && r.s.Phase != Exited {
		b.Asked = true
		r.emit(RequestCards{})
	}
}

func intp(n int) *int       { return &n }
func strp(s string) *string { return &s }
func int64p(n int64) *int64 { return &n }
func opOn(op string) CardOp { return CardOp{Op: op} }
func nextCell(st string) string {
	switch st {
	case CellOpen:
		return CellDone
	case CellDone:
		return CellSkip
	}
	return CellOpen
}

// boardKey handles a key while the pane shows the board.
func (r *reducer) boardKey(e PaneKey) {
	p := &r.s.Pane
	b := &p.Board
	if in := b.Input; in != nil {
		r.inputKey(e, in)
		return
	}
	if b.Confirm != 0 {
		id := b.Confirm
		b.Confirm = 0
		if e.Action == PaneYes {
			r.sendOp(id, opOn("archive"))
		} else {
			b.Message = "Archive cancelled."
		}
		return
	}
	b.Message, b.Failure = "", ""
	c, i := BoardCard(*b)
	switch e.Action {
	case PaneBoard:
		r.toggleBoard()
	case PaneRefresh:
		b.Asked = false
		r.askCards()
	case PaneUp, PaneDown, PagePrev, PageNext:
		d := map[PaneAction]int{PaneUp: -1, PaneDown: 1, PagePrev: -scrollPage, PageNext: scrollPage}[e.Action]
		switch {
		case i < 0:
		case b.Detail:
			b.Line = min(max(BoardLine(*b, c)+d, 0), c.Lines()-1)
		default:
			j := min(max(i+d, 0), len(b.Cards)-1)
			b.SelID = b.Cards[j].ID
		}
	case PaneLeft, PaneRight:
		if b.Detail && c.Kind == CardTracker && len(c.Columns) > 0 {
			d := 1
			if e.Action == PaneLeft {
				d = -1
			}
			b.Col = min(max(BoardCol(*b, c)+d, 0), len(c.Columns)-1)
		}
	case PaneEnter:
		if i >= 0 && !b.Detail {
			b.SelID, b.Detail, b.Line, b.Col = c.ID, true, 0, 0
		}
	case PaneEsc:
		if b.Detail {
			b.Detail = false
		} else {
			p.Focus = false
		}
	case PaneMark:
		if i >= 0 && b.Detail {
			r.mark(c)
		}
	case PaneAdd, PaneAddColumn:
		// Adding works in an open card only, where the operator sees what
		// they add to.
		if i >= 0 && b.Detail {
			r.startAdd(c, e.Action == PaneAddColumn)
		}
	case PaneEdit, PaneEditColumn:
		// From the list only the title shows, so only e (retitle) works
		// there; E renames a column the operator can see in an open card.
		if i >= 0 && (b.Detail || e.Action == PaneEdit) {
			r.startEdit(c, e.Action == PaneEditColumn)
		}
	case PaneArchive:
		if i >= 0 {
			b.SelID = c.ID
			b.Confirm = c.ID
		}
	}
}

// refusedNotice heads relay's refusal of an op. It goes when relay takes
// the next op, or when the operator closes the input it reopened.
const refusedNotice = "Refused."

// busyNotice is what a key that came while an op was on its way says.
// It goes when the op is done.
const busyNotice = "Still sending the last change; that key was not sent."

// busy says an op is on its way, and says so.
func (r *reducer) busy() bool {
	b := &r.s.Pane.Board
	if b.Sending != 0 {
		b.Message = busyNotice
		return true
	}
	return false
}

func (r *reducer) sendOp(card int64, op CardOp) {
	b := &r.s.Pane.Board
	if r.busy() {
		return
	}
	b.Sending, b.SendingOp = card, op.Op
	r.emit(SendCardOp{Card: card, Op: op})
}

// mark ticks or unticks the selected checklist item, or moves the
// selected tracker cell on: open, done, skip, open.
func (r *reducer) mark(c Card) {
	b := &r.s.Pane.Board
	line := BoardLine(*b, c)
	switch {
	case c.Kind == CardChecklist && line > 0:
		it := c.Items[line-1]
		op := opOn("tick")
		if it.Done {
			op.Op = "untick"
		}
		op.Item = intp(it.ID)
		r.sendOp(c.ID, op)
	case c.Kind == CardTracker && line > 0 && len(c.Columns) > 0:
		row, col := c.Rows[line-1], c.Columns[BoardCol(*b, c)]
		op := opOn("set")
		op.Row, op.Column = intp(row.ID), intp(col.ID)
		op.State = strp(nextCell(c.CellState(row.ID, col.ID)))
		r.sendOp(c.ID, op)
	}
}

func (r *reducer) startAdd(c Card, column bool) {
	b := &r.s.Pane.Board
	var op, prompt string
	switch {
	case c.Kind == CardChecklist && !column:
		op, prompt = "add", "new item"
	case c.Kind == CardTracker && !column:
		op, prompt = "add_row", "new row"
	case c.Kind == CardTracker:
		op, prompt = "add_column", "new column"
	case column:
		b.Message = "Only a tracker has columns."
		return
	default:
		b.Message = "Nothing to add to this card."
		return
	}
	if r.busy() {
		return
	}
	b.SelID = c.ID
	b.Input = &BoardInput{Card: c.ID, Op: opOn(op), Prompt: prompt}
}

// startEdit opens the input on the selected line's text. It starts with
// the text as it is, unless that text came from outside: what the
// operator sends is the operator's, so it starts empty rather than make
// vouching for outside text one key away.
func (r *reducer) startEdit(c Card, column bool) {
	b := &r.s.Pane.Board
	line := 0
	if b.Detail {
		line = BoardLine(*b, c)
	}
	var op CardOp
	var prompt string
	var t CardText
	switch {
	case column:
		if c.Kind != CardTracker || len(c.Columns) == 0 {
			b.Message = "Only a tracker has columns."
			return
		}
		col := c.Columns[BoardCol(*b, c)]
		op, prompt, t = opOn("rename_column"), "column "+strconv.Itoa(col.ID), col.CardText
		op.Column = intp(col.ID)
	case line == 0:
		op, prompt, t = opOn("retitle"), "title", c.Title
	case c.Kind == CardChecklist:
		it := c.Items[line-1]
		op, prompt, t = opOn("edit"), "item "+strconv.Itoa(it.ID), it.CardText
		op.Item = intp(it.ID)
	case c.Kind == CardTracker:
		row := c.Rows[line-1]
		op, prompt, t = opOn("rename_row"), "row "+strconv.Itoa(row.ID), row.CardText
		op.Row = intp(row.ID)
	default:
		return
	}
	if r.busy() {
		return
	}
	op.Seen = int64p(c.Rev)
	in := &BoardInput{Card: c.ID, Op: op, Prompt: prompt}
	if !t.Outsider {
		in.Text, in.Was = t.Text, t.Text
	}
	b.SelID = c.ID
	b.Input = in
}

// inputKey types into the one-line input, sends it, or cancels it.
func (r *reducer) inputKey(e PaneKey, in *BoardInput) {
	b := &r.s.Pane.Board
	switch e.Action {
	case PaneChar:
		t := typed(e.Text)
		b.Input = &BoardInput{Card: in.Card, Op: in.Op, Prompt: in.Prompt, Text: in.Text + t, Was: in.Was}
	case PaneBackspace:
		rs := []rune(in.Text)
		if len(rs) > 0 {
			rs = rs[:len(rs)-1]
		}
		b.Input = &BoardInput{Card: in.Card, Op: in.Op, Prompt: in.Prompt, Text: string(rs), Was: in.Was}
	case PaneEsc:
		b.Input = nil
		if b.Message == refusedNotice {
			b.Message, b.Failure = "", ""
		}
	case PaneEnter:
		if in.Was != "" && in.Text == in.Was {
			b.Input = nil
			b.Message = "Unchanged."
			return
		}
		if r.busy() {
			return
		}
		b.Input = nil
		op := in.Op
		op.Text = strp(in.Text)
		r.sendOp(in.Card, op)
		b.Sent = in
	}
}

// typed is the text a key typed, as the input takes it: one line, and
// only characters that print. Spacing becomes a space.
func typed(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		case printable(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}
