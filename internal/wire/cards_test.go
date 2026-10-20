package wire

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	acp "github.com/caelis-labs/acp-go-sdk"
	"pgregory.net/rapid"

	"github.com/kirahowe/acp-tui/internal/core"
)

// trackerWire is the wire form in relay's brief.
const trackerWire = `{"id": 1840, "kind": "tracker", "status": "active", "complete": false,
 "rev": 1842, "owner": "hub", "addedAt": "2026-10-20T15:03:00Z",
 "title": {"text": "Catch up monthly financials", "outsider": false, "rev": 1840},
 "progress": {"done": 6, "total": 24},
 "labels": ["private"],
 "rows": [{"id": 1, "text": "BMO", "outsider": false, "rev": 1840}],
 "columns": [{"id": 1, "text": "Jun", "outsider": false, "rev": 1840}, {"id": 2, "text": "Jul", "outsider": true, "rev": 1841}],
 "cells": [{"row": 1, "column": 1, "state": "done"}, {"row": 1, "column": 2, "state": "open"}],
 "text": "Card 1840: ..."}`

func TestCardsReadsTheWireForm(t *testing.T) {
	raw := json.RawMessage(`{"rev": 1900, "cards": [` + trackerWire + `,
	 {"id": 1851, "kind": "checklist", "status": "active", "rev": 1851, "owner": "operator",
	  "title": {"text": "How to", "outsider": false, "rev": 1851}, "progress": {"done": 1, "total": 2},
	  "labels": ["private", "contaminated"],
	  "items": [{"id": 1, "text": "Make a folder", "done": true, "outsider": false, "rev": 1851},
	            {"id": 2, "text": "hxxps://evil[.]test", "done": false, "outsider": true, "rev": 1852}],
	  "text": "Card 1851"},
	 {"id": 1860, "kind": "note", "status": "active", "rev": 1860, "title": {"text": "N", "rev": 1860},
	  "note": {"text": "line one\nline two", "outsider": false, "rev": 1860}, "text": "Card 1860"},
	 {"id": 1870, "kind": "kanban", "status": "active", "rev": 1870, "title": {"text": "K", "outsider": false, "rev": 1870},
	  "text": "Card 1870: a kind from the future"},
	 {"kind": "checklist", "title": {"text": "no id"}}]}`)
	got := Cards(raw, nil, t0)
	if got.Err != "" || got.Unavailable || got.Rev != 1900 || len(got.Cards) != 4 {
		t.Fatalf("%+v", got)
	}
	want := core.Card{ID: 1840, Kind: core.CardTracker, Status: core.CardActive, Rev: 1842, Owner: "hub",
		AddedAt: "2026-10-20T15:03:00Z", Title: core.CardText{Text: "Catch up monthly financials", Rev: 1840},
		Done: 6, Total: 24, Labels: core.Labels{Private: true},
		Rows: []core.CardElem{{ID: 1, CardText: core.CardText{Text: "BMO", Rev: 1840}}},
		Columns: []core.CardElem{{ID: 1, CardText: core.CardText{Text: "Jun", Rev: 1840}},
			{ID: 2, CardText: core.CardText{Text: "Jul", Outsider: true, Rev: 1841}}},
		Cells: []core.Cell{{Row: 1, Column: 1, State: core.CellDone}}, Text: "Card 1840: ..."}
	if !reflect.DeepEqual(got.Cards[0], want) {
		t.Errorf("tracker\n%+v\nwant\n%+v", got.Cards[0], want)
	}
	cl := got.Cards[1]
	if len(cl.Items) != 2 || !cl.Items[0].Done || cl.Items[0].Outsider || !cl.Items[1].Outsider || !cl.Labels.Contaminated {
		t.Errorf("checklist %+v", cl)
	}
	// A missing outsider flag reads as outside text.
	if n := got.Cards[2]; n.Note == nil || n.Note.Text != "line one\nline two" || !n.Title.Outsider {
		t.Errorf("note %+v", n)
	}
	if k := got.Cards[3]; k.Native() || k.Text != "Card 1870: a kind from the future" {
		t.Errorf("unknown kind %+v", k)
	}
}

func TestBrokenNativeCardFallsBackToText(t *testing.T) {
	got := Cards(json.RawMessage(`{"rev":1,"cards":[{"id":5,"kind":"checklist","items":[{"text":"no id"}],"text":"Card 5"}]}`), nil, t0)
	if len(got.Cards) != 1 || got.Cards[0].Native() || got.Cards[0].Text != "Card 5" {
		t.Fatalf("%+v", got.Cards)
	}
}

func TestCardsWithoutTheMethod(t *testing.T) {
	if got := Cards(nil, acp.NewMethodNotFound(MethodCards), t0); !got.Unavailable || got.Err != "" {
		t.Errorf("%+v", got)
	}
	if got := Cards(nil, errors.New("timeout"), t0); got.Unavailable || got.Err != "timeout" {
		t.Errorf("%+v", got)
	}
	if got := Cards(json.RawMessage(`{"rev":1}`), nil, t0); got.Err == "" {
		t.Errorf("an answer without cards read as an empty board: %+v", got)
	}
}

func TestCardOpParamsLeaveOutWhatIsNotSet(t *testing.T) {
	item, text, seen := 3, "Buy milk", int64(1842)
	got, _ := json.Marshal(CardOpParams("s1", 1840, core.CardOp{Op: "edit", Item: &item, Text: &text, Seen: &seen}))
	if string(got) != `{"card":1840,"item":3,"op":"edit","seen":1842,"sessionId":"s1","text":"Buy milk"}` {
		t.Errorf("%s", got)
	}
	row, col, st := 1, 2, "skip"
	got, _ = json.Marshal(CardOpParams("s1", 7, core.CardOp{Op: "set", Row: &row, Column: &col, State: &st}))
	if string(got) != `{"card":7,"column":2,"op":"set","row":1,"sessionId":"s1","state":"skip"}` {
		t.Errorf("%s", got)
	}
}

func TestCardOpOutcome(t *testing.T) {
	ok := CardOpOutcome(1840, json.RawMessage(`{"card":`+trackerWire+`}`), nil, t0)
	if ok.Err != "" || ok.Card == nil || ok.Card.ID != 1840 || ok.CardID != 1840 {
		t.Fatalf("%+v", ok)
	}
	refused := CardOpOutcome(1840, nil, &acp.RequestError{Code: -32000, Message: "Item 9 is not on card 1840."}, t0)
	if refused.Card != nil || refused.Err != "Item 9 is not on card 1840." {
		t.Errorf("%+v", refused)
	}
	// An answer about another card is not taken.
	if other := CardOpOutcome(1, json.RawMessage(`{"card":`+trackerWire+`}`), nil, t0); other.Err == "" || other.Card != nil {
		t.Errorf("%+v", other)
	}
}

func TestCardsChangedNotification(t *testing.T) {
	got, ok := CardsChanged(json.RawMessage(`{"rev": 1950, "cards": [`+trackerWire+`]}`), t0)
	if !ok || got.Rev != 1950 || len(got.Cards) != 1 || got.Cards[0].ID != 1840 {
		t.Fatalf("%+v", got)
	}
	if _, ok := CardsChanged(json.RawMessage(`[1]`), t0); ok {
		t.Error("read a notification that is not an object")
	}
}

// encodeCard writes a card in relay's wire form, as relay would.
func encodeCard(c core.Card) map[string]any {
	text := func(t core.CardText) map[string]any {
		return map[string]any{"text": t.Text, "outsider": t.Outsider, "rev": t.Rev}
	}
	elems := func(es []core.CardElem, done bool) []any {
		var out []any
		for _, e := range es {
			m := text(e.CardText)
			m["id"] = e.ID
			if done {
				m["done"] = e.Done
			}
			out = append(out, m)
		}
		return out
	}
	var labels []string
	if c.Labels.Private {
		labels = append(labels, "private")
	}
	if c.Labels.Contaminated {
		labels = append(labels, "contaminated")
	}
	m := map[string]any{"id": c.ID, "kind": c.Kind, "status": c.Status, "complete": c.Complete, "rev": c.Rev,
		"owner": c.Owner, "addedAt": c.AddedAt, "title": text(c.Title), "progress": map[string]any{"done": c.Done, "total": c.Total},
		"labels": labels, "text": c.Text}
	switch c.Kind {
	case core.CardChecklist:
		m["items"] = elems(c.Items, true)
	case core.CardTracker:
		m["rows"], m["columns"] = elems(c.Rows, false), elems(c.Columns, false)
		var cells []any
		for _, x := range c.Cells {
			cells = append(cells, map[string]any{"row": x.Row, "column": x.Column, "state": x.State})
		}
		m["cells"] = cells
	case core.CardNote:
		m["note"] = text(*c.Note)
	}
	return m
}

func genCardText(t *rapid.T, label string) core.CardText {
	return core.CardText{Text: rapid.String().Draw(t, label), Outsider: rapid.Bool().Draw(t, label+"Out"),
		Rev: rapid.Int64Range(0, 1<<40).Draw(t, label+"Rev")}
}

func genElems(t *rapid.T, label string, done bool) []core.CardElem {
	var out []core.CardElem
	for i := range rapid.IntRange(0, 4).Draw(t, label) {
		e := core.CardElem{ID: i + 1, CardText: genCardText(t, label+"Text")}
		if done {
			e.Done = rapid.Bool().Draw(t, label+"Done")
		}
		out = append(out, e)
	}
	return out
}

// Any card relay writes in its wire form reads back as the same card.
func TestWireFormRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := core.Card{ID: rapid.Int64Range(1, 1<<40).Draw(t, "id"),
			Kind:     rapid.SampledFrom([]string{core.CardChecklist, core.CardTracker, core.CardNote, "kanban"}).Draw(t, "kind"),
			Status:   rapid.SampledFrom([]string{core.CardActive, core.CardArchived}).Draw(t, "status"),
			Complete: rapid.Bool().Draw(t, "complete"), Rev: rapid.Int64Range(1, 1<<40).Draw(t, "rev"),
			Owner: rapid.String().Draw(t, "owner"), AddedAt: rapid.String().Draw(t, "at"), Title: genCardText(t, "title"),
			Done: rapid.IntRange(0, 100).Draw(t, "done"), Total: rapid.IntRange(0, 100).Draw(t, "total"),
			Labels: core.Labels{Private: rapid.Bool().Draw(t, "p"), Contaminated: rapid.Bool().Draw(t, "c")},
			Text:   rapid.String().Draw(t, "text")}
		switch c.Kind {
		case core.CardChecklist:
			c.Items = genElems(t, "items", true)
		case core.CardTracker:
			c.Rows, c.Columns = genElems(t, "rows", false), genElems(t, "columns", false)
			for _, r := range c.Rows {
				for _, k := range c.Columns {
					if st := rapid.SampledFrom([]string{"", core.CellDone, core.CellSkip}).Draw(t, "cell"); st != "" {
						c.Cells = append(c.Cells, core.Cell{Row: r.ID, Column: k.ID, State: st})
					}
				}
			}
		case core.CardNote:
			n := genCardText(t, "note")
			c.Note = &n
		}
		raw, err := json.Marshal(map[string]any{"rev": c.Rev, "cards": []any{encodeCard(c)}})
		if err != nil {
			t.Fatal(err)
		}
		got := Cards(raw, nil, t0)
		if len(got.Cards) != 1 || !reflect.DeepEqual(got.Cards[0], c) {
			t.Fatalf("read back\n%+v\nwant\n%+v", got.Cards, c)
		}
	})
}
