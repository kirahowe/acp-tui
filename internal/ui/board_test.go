package ui_test

import (
	"strings"
	"testing"
)

// openBoard starts the board script on a wide screen and opens the board.
func openBoard(t *testing.T) *harness {
	h := start(t, "board.jsonl", 140, 40)
	h.keys(ctrlX)
	h.waitScreen("focus", func(s string) bool { return strings.Contains(s, "↑↓ select") })
	h.keys("b")
	h.waitScreen("the board", func(s string) bool { return strings.Contains(s, "board (4)") })
	return h
}

func TestBoardSnapshot(t *testing.T) {
	h := openBoard(t)
	scr := h.screen()
	for _, want := range []string{"Catch up monthly financials", "tracker · 3 of 8 done · card 1840", "How to work through it",
		"Where the statements are", "Moves", "kanban"} {
		if !strings.Contains(scr, want) {
			t.Errorf("%q not on the board:\n%s", want, scr)
		}
	}
	if calls := h.extensionCalls("_relay/cards"); len(calls) != 1 || !strings.Contains(calls[0], `"sessionId":"fake-session"`) {
		t.Errorf("cards calls %q", calls)
	}
	// Switching away and back does not ask again.
	h.keys("b")
	h.waitScreen("the items", func(s string) bool { return strings.Contains(s, "workspace (") })
	h.keys("b")
	h.waitScreen("the board", func(s string) bool { return strings.Contains(s, "board (4)") })
	if n := len(h.extensionCalls("_relay/cards")); n != 1 {
		t.Errorf("%d cards calls", n)
	}
	// A kind this client does not know shows relay's text form.
	h.keys("jjj\r")
	scr = h.waitScreen("the kanban card", func(s string) bool { return strings.Contains(s, "Todo: pack") })
	if !strings.Contains(scr, "Done: book van") {
		t.Errorf("text form not shown:\n%s", scr)
	}
}

func TestBoardOpRoundTripAndRefusal(t *testing.T) {
	h := openBoard(t)
	h.keys("j\r")
	scr := h.waitScreen("the checklist", func(s string) bool { return strings.Contains(s, "x tick") })
	// Outside text is marked and shows as relay sent it.
	if l := lineWith(scr, "[ ] 2"); !strings.Contains(l, "untrusted") || !strings.Contains(scr, "hxxps://evil[.]test/x?d=1") {
		t.Errorf("outside item not marked:\n%s", scr)
	}
	if strings.Contains(scr, "https://") {
		t.Errorf("a live link:\n%s", scr)
	}
	h.keys("jjj")
	h.waitScreen("item 3", func(s string) bool { return strings.Contains(lineWith(s, "Import into the app"), "▸") })
	h.keys("x")
	h.waitScreen("the answer", func(s string) bool { return strings.Contains(s, "[x] 3 Import into the app") })
	calls := h.extensionCalls("_relay/cards/op")
	if len(calls) != 1 || calls[0] != `{"card":1851,"item":3,"op":"tick","sessionId":"fake-session"}` {
		t.Fatalf("op calls %q", calls)
	}
	// Unticking is refused; relay's words show and the item stays done.
	h.keys("x")
	scr = h.waitScreen("the refusal", func(s string) bool { return strings.Contains(s, "Refused.") })
	if !strings.Contains(scr, "Card 1851 is archived.") || !strings.Contains(scr, "[x] 3 Import") {
		t.Errorf("refusal not shown:\n%s", scr)
	}
	if calls := h.extensionCalls("_relay/cards/op"); len(calls) != 2 || !strings.Contains(calls[1], `"op":"untick"`) {
		t.Errorf("op calls %q", calls)
	}
}

func TestBoardEditAndNotifications(t *testing.T) {
	h := openBoard(t)
	h.keys("\r") // the tracker
	h.waitScreen("the tracker", func(s string) bool { return strings.Contains(s, "A column") })
	h.keys("x") // on the title line: nothing to mark
	h.keys("e")
	h.waitScreen("the input", func(s string) bool { return strings.Contains(s, "title › Catch up monthly financials") })
	h.keys(" (Q3)\r")
	h.waitScreen("the new title", func(s string) bool { return strings.Contains(s, "Catch up monthly financials (Q3)") })
	calls := h.extensionCalls("_relay/cards/op")
	if len(calls) != 1 || calls[0] != `{"card":1840,"op":"retitle","seen":1842,"sessionId":"fake-session","text":"Catch up monthly financials (Q3)"}` {
		t.Fatalf("op calls %q", calls)
	}
	// A card another process archives leaves the board.
	h.keys("\x1b")
	h.waitScreen("the list", func(s string) bool { return strings.Contains(s, "enter open") })
	h.keys("\x1b")
	h.waitScreen("unfocused", func(s string) bool { return strings.Contains(s, ctrlXHint) })
	h.keys("go\r")
	h.waitIdle()
	scr := h.waitScreen("the change", func(s string) bool { return strings.Contains(s, "board (3)") })
	if strings.Contains(scr, "Where the statements are") {
		t.Errorf("the archived card is still on the board:\n%s", scr)
	}
	if strings.Contains(scr, "Filed.") == false {
		t.Errorf("the reply is missing:\n%s", scr)
	}
}

func TestBoardWithoutRelay(t *testing.T) {
	h := start(t, "simple.jsonl", 140, 30)
	h.keys(ctrlX)
	h.keys("b")
	h.waitScreen("no board", func(s string) bool { return strings.Contains(s, "This agent has no board.") })
}
