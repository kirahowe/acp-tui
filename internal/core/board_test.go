package core

import (
	"reflect"
	"slices"
	"testing"

	"pgregory.net/rapid"
)

func txt(s string, rev int64) CardText { return CardText{Text: s, Rev: rev} }

func checklist(id, rev int64, done ...bool) Card {
	c := Card{ID: id, Kind: CardChecklist, Status: CardActive, Rev: rev, Owner: "hub", Title: txt("Steps", id)}
	for i, d := range done {
		c.Items = append(c.Items, CardElem{ID: i + 1, CardText: txt("step", id), Done: d})
		c.Total++
		if d {
			c.Done++
		}
	}
	return c
}

func tracker(id, rev int64, cells ...Cell) Card {
	return Card{ID: id, Kind: CardTracker, Status: CardActive, Rev: rev, Title: txt("Accounts", id),
		Rows:    []CardElem{{ID: 1, CardText: txt("BMO", id)}, {ID: 2, CardText: txt("RBC", id)}},
		Columns: []CardElem{{ID: 1, CardText: txt("Jun", id)}, {ID: 2, CardText: txt("Jul", id)}},
		Cells:   cells}
}

// onBoard is a session with the pane focused on a loaded board.
func onBoard(cards ...Card) State {
	s, _ := feed(State{}, Connected{At: t0}, key(t0, PaneToggle), key(t0, PaneBoard),
		CardsLoaded{At: t0, Rev: 100, Cards: cards})
	return s
}

func boardKeys(s State, actions ...PaneAction) (State, []Effect) {
	var evs []Event
	for _, a := range actions {
		evs = append(evs, key(t0, a))
	}
	return feed(s, evs...)
}

func typeText(s State, text string) State {
	for _, r := range text {
		s, _ = Reduce(s, PaneKey{At: t0, Action: PaneChar, Text: string(r)})
	}
	return s
}

func TestBoardAsksOnceWhenFirstOpened(t *testing.T) {
	s, fx := feed(State{}, Connected{At: t0}, key(t0, PaneToggle), key(t0, PaneBoard))
	if !s.Pane.Board.Open || len(effectsOf[RequestCards](fx)) != 1 {
		t.Fatalf("open %v, effects %v", s.Pane.Board.Open, fx)
	}
	s, fx = feed(s, key(t0, PaneBoard), key(t0, PaneBoard))
	if !s.Pane.Board.Open || len(effectsOf[RequestCards](fx)) != 0 {
		t.Fatalf("asked again on reopening: %v", fx)
	}
	// Unfocused, the board stays the pane's view.
	s, _ = feed(s, key(t0, PaneEsc))
	if s.Pane.Focus || !s.Pane.Board.Open {
		t.Fatal("esc should leave the pane on the board")
	}
}

func TestBoardWithoutTheMethod(t *testing.T) {
	s, _ := feed(State{}, Connected{At: t0}, key(t0, PaneToggle), key(t0, PaneBoard), CardsLoaded{At: t0, Unavailable: true})
	if !s.Pane.Board.Unavailable {
		t.Fatal("not unavailable")
	}
	_, fx := boardKeys(s, PaneRefresh, PaneBoard, PaneBoard)
	if len(effectsOf[RequestCards](fx)) != 0 {
		t.Fatal("asked again after method-not-found")
	}
}

func TestBoardLoadFailureAsksAgain(t *testing.T) {
	s, _ := feed(State{}, Connected{At: t0}, key(t0, PaneToggle), key(t0, PaneBoard), CardsLoaded{At: t0, Err: "boom"})
	if s.Pane.Board.Failure != "boom" {
		t.Fatalf("failure %q", s.Pane.Board.Failure)
	}
	_, fx := boardKeys(s, PaneBoard, PaneBoard)
	if len(effectsOf[RequestCards](fx)) != 1 {
		t.Fatal("reopening after a failure should ask again")
	}
}

func TestCardsChangedReplacesAndDropsArchived(t *testing.T) {
	s := onBoard(checklist(10, 10, false), checklist(20, 20, false))
	done := checklist(10, 30, true)
	gone := checklist(20, 31)
	gone.Status = CardArchived
	added := tracker(40, 40)
	s, _ = Reduce(s, CardsChanged{At: t0, Rev: 40, Cards: []Card{done, gone, added}})
	b := s.Pane.Board
	if len(b.Cards) != 2 || !reflect.DeepEqual(b.Cards[0], done) || !reflect.DeepEqual(b.Cards[1], added) || b.Rev != 100 {
		t.Fatalf("cards %+v rev %d", b.Cards, b.Rev)
	}
	// An older copy of a card never replaces a newer one.
	s, _ = Reduce(s, CardsChanged{At: t0, Rev: 200, Cards: []Card{checklist(10, 12, false)}})
	if !reflect.DeepEqual(s.Pane.Board.Cards[0], done) || s.Pane.Board.Rev != 200 {
		t.Fatal("a stale card replaced a newer one")
	}
}

func TestSnapshotKeepsANewerNotification(t *testing.T) {
	s, _ := feed(State{}, Connected{At: t0}, key(t0, PaneToggle), key(t0, PaneBoard),
		CardsChanged{At: t0, Rev: 50, Cards: []Card{checklist(10, 50, true)}},
		CardsLoaded{At: t0, Rev: 40, Cards: []Card{checklist(10, 40, false), checklist(20, 20)}})
	b := s.Pane.Board
	if len(b.Cards) != 2 || b.Cards[0].Rev != 50 || b.Rev != 50 {
		t.Fatalf("cards %+v rev %d", b.Cards, b.Rev)
	}
}

// Whatever order relay's cards arrive in, in notifications or answers,
// the board holds the newest copy of each card, and no card whose newest
// copy is archived: a late answer cannot bring an archived card back.
func TestBoardHoldsTheNewestOfEachCard(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := onBoard()
		newest := map[int64]Card{}
		// A rev fixes a copy's content, as it does in relay's log.
		copyAt := func(id, rev int64) Card {
			c := checklist(id, rev, rev%2 == 0)
			if rev%5 == 0 {
				c.Status = CardArchived
			}
			return c
		}
		for range rapid.IntRange(0, 30).Draw(t, "events") {
			var cards []Card
			for range rapid.IntRange(1, 3).Draw(t, "cards") {
				cards = append(cards, copyAt(rapid.Int64Range(1, 5).Draw(t, "id"), rapid.Int64Range(1, 60).Draw(t, "rev")))
			}
			var e Event = CardsChanged{At: t0, Cards: cards}
			if rapid.Bool().Draw(t, "answer") {
				cards = cards[:1]
				e = CardOpDone{At: t0, CardID: cards[0].ID, Card: &cards[0]}
			}
			for _, c := range cards {
				if old, ok := newest[c.ID]; !ok || c.Rev > old.Rev {
					newest[c.ID] = c
				}
			}
			s, _ = Reduce(s, e)
		}
		want := map[int64]Card{}
		for id, c := range newest {
			if c.Status == CardActive {
				want[id] = c
			}
		}
		got := map[int64]Card{}
		for _, c := range s.Pane.Board.Cards {
			if _, dup := got[c.ID]; dup {
				t.Fatalf("card %d twice", c.ID)
			}
			got[c.ID] = c
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("board %v, want %v", got, want)
		}
	})
}

func TestTickSendsAnOpAndWaits(t *testing.T) {
	s := onBoard(checklist(10, 10, false, true))
	s, fx := boardKeys(s, PaneEnter, PaneDown, PaneMark)
	ops := effectsOf[SendCardOp](fx)
	if len(ops) != 1 || ops[0].Card != 10 || ops[0].Op.Op != "tick" || *ops[0].Op.Item != 1 || ops[0].Op.Seen != nil {
		t.Fatalf("ops %+v", ops)
	}
	b := s.Pane.Board
	if b.Sending != 10 || b.Cards[0].Items[0].Done {
		t.Fatalf("sending %d, item done %v: no optimistic change", b.Sending, b.Cards[0].Items[0].Done)
	}
	// A second op waits for the first.
	s, fx = boardKeys(s, PaneDown, PaneMark)
	if len(effectsOf[SendCardOp](fx)) != 0 || s.Pane.Board.Message == "" {
		t.Fatalf("sent while sending: %v", fx)
	}
	after := checklist(10, 11, true, true)
	s, _ = Reduce(s, CardOpDone{At: t0, CardID: 10, Card: &after})
	if s.Pane.Board.Sending != 0 || !s.Pane.Board.Cards[0].Items[0].Done {
		t.Fatal("the answer did not replace the card")
	}
	_, fx = boardKeys(s, PaneMark)
	if ops := effectsOf[SendCardOp](fx); len(ops) != 1 || ops[0].Op.Op != "untick" || *ops[0].Op.Item != 2 {
		t.Fatalf("ops %+v", ops)
	}
}

func TestRefusalIsShown(t *testing.T) {
	s := onBoard(checklist(10, 10, false))
	s, _ = boardKeys(s, PaneEnter, PaneDown, PaneMark)
	s, _ = Reduce(s, CardOpDone{At: t0, CardID: 10, Err: "Card 10 is archived."})
	b := s.Pane.Board
	if b.Message != "Refused." || b.Failure != "Card 10 is archived." || b.Sending != 0 {
		t.Fatalf("%+v", b)
	}
}

func TestUnchangedOpSaysSo(t *testing.T) {
	s := onBoard(checklist(10, 10, true))
	s, _ = boardKeys(s, PaneEnter, PaneDown, PaneMark)
	same := checklist(10, 10, true)
	s, _ = Reduce(s, CardOpDone{At: t0, CardID: 10, Card: &same, Unchanged: true})
	b := s.Pane.Board
	if b.Message != "Nothing to change." || b.Sending != 0 || b.Failure != "" {
		t.Fatalf("%+v", b)
	}
}

// A notification can reach the client before the answer to _relay/cards
// it follows, carrying a card the answer predates. The card stays.
func TestNotificationBeforeTheAnswerKeepsANewCard(t *testing.T) {
	s, _ := feed(State{}, Connected{At: t0}, key(t0, PaneToggle), key(t0, PaneBoard),
		CardsChanged{At: t0, Rev: 101, Cards: []Card{checklist(101, 101)}},
		CardsLoaded{At: t0, Rev: 100, Cards: []Card{checklist(50, 60)}})
	var ids []int64
	for _, c := range s.Pane.Board.Cards {
		ids = append(ids, c.ID)
	}
	if !slices.Equal(ids, []int64{50, 101}) {
		t.Fatalf("cards %v", ids)
	}
	// A card held from an older answer that relay no longer lists goes.
	s, _ = Reduce(s, CardsLoaded{At: t0, Rev: 120, Cards: []Card{checklist(101, 101)}})
	if len(s.Pane.Board.Cards) != 1 || s.Pane.Board.Cards[0].ID != 101 {
		t.Fatalf("%+v", s.Pane.Board.Cards)
	}
	// A notification that archived a card before the answer keeps it off.
	gone := checklist(101, 130)
	gone.Status = CardArchived
	s, _ = Reduce(s, CardsChanged{At: t0, Rev: 130, Cards: []Card{gone}})
	s, _ = Reduce(s, CardsLoaded{At: t0, Rev: 125, Cards: []Card{checklist(101, 101)}})
	if len(s.Pane.Board.Cards) != 0 {
		t.Fatalf("%+v", s.Pane.Board.Cards)
	}
}

func TestTrackerCellCycles(t *testing.T) {
	s := onBoard(tracker(30, 30))
	s, _ = boardKeys(s, PaneEnter, PaneDown, PaneRight)
	var states []string
	for _, cell := range []Cell{{}, {Row: 1, Column: 2, State: CellDone}, {Row: 1, Column: 2, State: CellSkip}} {
		c := tracker(30, 31+int64(len(states)), cell)
		s, _ = Reduce(s, CardsChanged{At: t0, Cards: []Card{c}})
		var fx []Effect
		s, fx = boardKeys(s, PaneMark)
		op := effectsOf[SendCardOp](fx)[0].Op
		if op.Op != "set" || *op.Row != 1 || *op.Column != 2 {
			t.Fatalf("op %+v", op)
		}
		states = append(states, *op.State)
		s, _ = Reduce(s, CardOpDone{At: t0, CardID: 30, Card: &c})
	}
	if !slices.Equal(states, []string{CellDone, CellSkip, CellOpen}) {
		t.Fatalf("states %q", states)
	}
}

func TestAddSendsTheTypedText(t *testing.T) {
	s := onBoard(tracker(30, 30))
	s, _ = boardKeys(s, PaneEnter, PaneAddColumn)
	if in := s.Pane.Board.Input; in == nil || in.Op.Op != "add_column" {
		t.Fatalf("input %+v", s.Pane.Board.Input)
	}
	s = typeText(s, "Aug\x1b​")
	s, fx := boardKeys(s, PaneBackspace, PaneEnter)
	ops := effectsOf[SendCardOp](fx)
	if len(ops) != 1 || ops[0].Op.Op != "add_column" || *ops[0].Op.Text != "Au" || ops[0].Op.Seen != nil {
		t.Fatalf("ops %+v", ops)
	}
	if s.Pane.Board.Input != nil {
		t.Fatal("input still open")
	}
}

// An edit carries the rev the operator saw when the input opened, not
// the rev of a change that arrived while they typed: relay then refuses
// it rather than overwrite the newer text.
func TestEditCarriesTheRevItStartedFrom(t *testing.T) {
	s := onBoard(checklist(10, 15, false))
	s, _ = boardKeys(s, PaneEnter, PaneDown, PaneEdit)
	in := s.Pane.Board.Input
	if in == nil || in.Text != "step" || in.Op.Op != "edit" || *in.Op.Item != 1 {
		t.Fatalf("input %+v", in)
	}
	newer := checklist(10, 22, false)
	newer.Items[0].Text, newer.Items[0].Rev = "step, changed elsewhere", 22
	s, _ = Reduce(s, CardsChanged{At: t0, Rev: 22, Cards: []Card{newer}})
	s = typeText(s, " one")
	_, fx := boardKeys(s, PaneEnter)
	ops := effectsOf[SendCardOp](fx)
	if len(ops) != 1 || *ops[0].Op.Seen != 15 || *ops[0].Op.Text != "step one" {
		t.Fatalf("ops %+v", ops)
	}
}

func TestEditOfOutsideTextStartsEmpty(t *testing.T) {
	c := checklist(10, 10, false)
	c.Title.Outsider = true
	s := onBoard(c)
	s, _ = boardKeys(s, PaneEnter, PaneEdit)
	if in := s.Pane.Board.Input; in == nil || in.Text != "" || in.Op.Op != "retitle" {
		t.Fatalf("input %+v", in)
	}
	// Enter on an empty input still sends: relay says what is wrong.
	_, fx := boardKeys(s, PaneEnter)
	if len(effectsOf[SendCardOp](fx)) != 1 {
		t.Fatal("not sent")
	}
}

func TestUnchangedEditIsNotSent(t *testing.T) {
	s := onBoard(checklist(10, 10, false))
	s, fx := boardKeys(s, PaneEnter, PaneEdit, PaneEnter)
	if len(effectsOf[SendCardOp](fx)) != 0 || s.Pane.Board.Message != "Unchanged." {
		t.Fatalf("%v %q", fx, s.Pane.Board.Message)
	}
}

func TestArchiveNeedsConfirming(t *testing.T) {
	s := onBoard(checklist(10, 10), checklist(20, 20))
	s, fx := boardKeys(s, PaneDown, PaneArchive, PaneOther)
	if len(effectsOf[SendCardOp](fx)) != 0 || s.Pane.Board.Message != "Archive cancelled." {
		t.Fatal("archived without a y")
	}
	s, fx = boardKeys(s, PaneArchive, PaneYes)
	ops := effectsOf[SendCardOp](fx)
	if len(ops) != 1 || ops[0].Card != 20 || ops[0].Op.Op != "archive" {
		t.Fatalf("ops %+v", ops)
	}
	gone := checklist(20, 21)
	gone.Status = CardArchived
	s, _ = Reduce(s, CardOpDone{At: t0, CardID: 20, Card: &gone})
	if len(s.Pane.Board.Cards) != 1 || s.Pane.Board.SelID != 10 {
		t.Fatalf("cards %+v sel %d", s.Pane.Board.Cards, s.Pane.Board.SelID)
	}
}

func TestOpenCardLeavingTheBoardClosesIt(t *testing.T) {
	s := onBoard(checklist(10, 10, false))
	s, _ = boardKeys(s, PaneEnter, PaneDown, PaneEdit)
	gone := checklist(10, 11)
	gone.Status = CardArchived
	s, _ = Reduce(s, CardsChanged{At: t0, Cards: []Card{gone}})
	b := s.Pane.Board
	if b.Detail || b.Input != nil || len(b.Cards) != 0 || b.Message == "" {
		t.Fatalf("%+v", b)
	}
}

// Keys typed into the input never act on the board, and the board's keys
// do nothing while the pane is not focused.
func TestInputTakesEveryKey(t *testing.T) {
	s := onBoard(checklist(10, 10, false))
	s, _ = boardKeys(s, PaneEnter, PaneAdd)
	s, fx := boardKeys(s, PaneMark, PaneArchive, PaneDown, PaneBoard)
	if len(fx) != 0 || s.Pane.Board.Input == nil || !s.Pane.Board.Open {
		t.Fatalf("a key escaped the input: %v", fx)
	}
	s, _ = boardKeys(s, PaneEsc, PaneEsc, PaneEsc)
	if s.Pane.Focus {
		t.Fatal("esc should close the input, the card, then the pane")
	}
	_, fx = boardKeys(s, PaneMark, PaneArchive, PaneYes)
	if len(fx) != 0 {
		t.Fatalf("keys acted on an unfocused pane: %v", fx)
	}
}

func TestNoteOffersOnlyItsTitle(t *testing.T) {
	n := Card{ID: 50, Kind: CardNote, Status: CardActive, Rev: 50, Title: txt("Note", 50), Note: &CardText{Text: "a\nb"}}
	s := onBoard(n)
	s, fx := boardKeys(s, PaneEnter, PaneDown, PaneMark, PaneAdd)
	if len(fx) != 0 || s.Pane.Board.Input != nil {
		t.Fatalf("%v", fx)
	}
	s, _ = boardKeys(s, PaneEdit)
	if in := s.Pane.Board.Input; in == nil || in.Op.Op != "retitle" {
		t.Fatalf("input %+v", in)
	}
}

// Opened before the session exists, the board asks once it does.
func TestBoardOpenedBeforeConnectAsksOnConnect(t *testing.T) {
	s, fx := feed(State{}, key(t0, PaneToggle), key(t0, PaneBoard))
	if len(effectsOf[RequestCards](fx)) != 0 {
		t.Fatal("asked before the session exists")
	}
	_, fx = feed(s, Connected{At: t0})
	if len(effectsOf[RequestCards](fx)) != 1 {
		t.Fatalf("not asked on connect: %v", fx)
	}
}

func TestArchivingAnOpenCardSaysSo(t *testing.T) {
	s := onBoard(checklist(10, 10))
	s, _ = boardKeys(s, PaneEnter, PaneArchive, PaneYes)
	gone := checklist(10, 11)
	gone.Status = CardArchived
	s, _ = Reduce(s, CardOpDone{At: t0, CardID: 10, Card: &gone})
	if s.Pane.Board.Message != "Archived card 10." || s.Pane.Board.Detail {
		t.Fatalf("%+v", s.Pane.Board)
	}
}
