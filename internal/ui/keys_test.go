package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kirahowe/acp-tui/internal/core"
	"pgregory.net/rapid"
)

type nopConn struct{}

func (nopConn) Prompt(string, []core.Attachment) {}
func (nopConn) Cancel()                          {}
func (nopConn) Answer(core.AnswerPermission)     {}
func (nopConn) Items()                           {}
func (nopConn) Resolve(string)                   {}
func (nopConn) Trust(string, string, string)     {}
func (nopConn) Cards()                           {}
func (nopConn) CardOp(int64, core.CardOp)        {}
func (nopConn) Shutdown()                        {}

func genKey(t *rapid.T) tea.KeyPressMsg {
	code := rapid.OneOf(
		rapid.SampledFrom([]rune{tea.KeyEnter, tea.KeyEscape, tea.KeyTab, tea.KeyBackspace, tea.KeyUp, 'j', 'c', 'd', 'm', '[', 0x1b, 0x07, 0}),
		rapid.Rune(),
	).Draw(t, "code")
	return tea.KeyPressMsg{
		Code: code,
		Mod:  tea.KeyMod(rapid.IntRange(0, 0xff).Draw(t, "mod")),
		Text: rapid.OneOf(rapid.String(), rapid.SampledFrom([]string{"", "\x1b[27;5;13~", "\x1b", "\r", "\x00", "a\tb"})).Draw(t, "text"),
	}
}

// P12: no key puts an escape or another control character into the
// input, every newline key gives Newline, and enter never does.
func TestKeysNeverLeakEscapes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := New(nopConn{}, "")
		for range rapid.IntRange(1, 20).Draw(t, "keys") {
			k := genKey(t)
			if Classify(k, false) == Newline && k.String() == "enter" {
				t.Fatalf("enter gave Newline")
			}
			next, _ := m.Update(k)
			m = next.(Model)
			for _, r := range m.input.Value() {
				if r == 0x1b || (r < 0x20 && r != '\n') || r == 0x7f {
					t.Fatalf("key %#v left %q in the input", k, m.input.Value())
				}
			}
		}
	})
	newline := []tea.KeyPressMsg{
		{Code: tea.KeyEnter, Mod: tea.ModShift},
		{Code: tea.KeyEnter, Mod: tea.ModCtrl},
		{Code: tea.KeyEnter, Mod: tea.ModAlt},
		{Code: 'j', Mod: tea.ModCtrl},
	}
	for _, k := range newline {
		if a := Classify(k, true); a != Newline {
			t.Errorf("%s gave %v, want Newline", k, a)
		}
	}
	if a := Classify(tea.KeyPressMsg{Code: tea.KeyEnter}, false); a != Send {
		t.Errorf("enter gave %v", a)
	}
	if a := Classify(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, false); a != PassThrough {
		t.Errorf("ctrl+d with text gave %v", a)
	}
}

// P13: every chunk fits the room and is not empty, and the chunks give
// back the lines.
func TestPrintChunks(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lines := rapid.SliceOf(rapid.SampledFrom([]string{"", "a", "bb", "世界", "\x1b[1mx\x1b[m"})).Draw(t, "lines")
		room := rapid.IntRange(-3, 30).Draw(t, "room")
		var back []string
		for _, c := range PrintChunks(lines, room) {
			if c == "" {
				t.Fatal("an empty chunk: Bubble Tea would drop it")
			}
			if n := strings.Count(c, "\n") + 1; n > max(room, 1) {
				t.Fatalf("chunk of %d lines for room %d", n, room)
			}
			if c == " " {
				c = ""
			}
			back = append(back, strings.Split(c, "\n")...)
		}
		if len(lines) == 0 && len(back) == 0 {
			return
		}
		if !reflect.DeepEqual(back, lines) {
			t.Fatalf("chunks give %q, want %q", back, lines)
		}
	})
}

// P-perm5: while a request waits, a key is either one of the request
// keys or swallowed; it never edits the input.
func TestAskKeys(t *testing.T) {
	for key, want := range map[string]AskAction{
		"y": AskAllow, "n": AskReject, "enter": AskConfirm, "esc": AskCancel, "v": AskShowAll,
		"up": AskUp, "down": AskDown, "ctrl+c": AskInterrupt, "a": AskNone, "space": AskNone, "ctrl+j": AskNone,
	} {
		var k tea.KeyPressMsg
		switch key {
		case "enter":
			k = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			k = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "up":
			k = tea.KeyPressMsg{Code: tea.KeyUp}
		case "down":
			k = tea.KeyPressMsg{Code: tea.KeyDown}
		case "ctrl+c":
			k = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
		case "ctrl+j":
			k = tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
		case "space":
			k = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		default:
			k = tea.KeyPressMsg{Code: rune(key[0]), Text: key}
		}
		if got := ClassifyAsk(k); got != want {
			t.Errorf("%s: %v, want %v", key, got, want)
		}
	}
}

func TestAskNeverTouchesTheInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := New(nopConn{}, "")
		var model any = m
		model, _ = model.(Model).Update(EventMsg{Event: core.PermissionAsked{At: time.Now(), Permission: core.Permission{ID: 1,
			Options: []core.PermOption{{ID: "a", Kind: core.AllowOnce}}}}})
		for range rapid.IntRange(1, 20).Draw(t, "keys") {
			if len(model.(Model).core.Asks) == 0 {
				break // a key answered it; the keys are the input's again
			}
			model, _ = model.(Model).Update(genKey(t))
			if v := model.(Model).input.Value(); v != "" {
				t.Fatalf("input %q", v)
			}
		}
	})
}

// While the workspace pane has focus, no key reaches the input box.
func TestPaneFocusSwallowsKeys(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var model any = New(nopConn{}, "")
		step := func(msg tea.Msg) { model, _ = model.(Model).Update(msg) }
		step(tea.WindowSizeMsg{Width: 100, Height: 30})
		step(EventMsg{Event: core.Connected{At: time.Now()}})
		step(EventMsg{Event: core.ItemsLoaded{At: time.Now(), Items: []core.Item{{Kind: core.ShownItem, Ref: "shown:x", Title: "x"}}}})
		step(tea.KeyPressMsg{Code: 'a', Text: "a"}) // something already typed
		step(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
		if !model.(Model).core.Pane.Focus {
			t.Fatal("ctrl+x did not focus the pane")
		}
		for range rapid.IntRange(1, 30).Draw(t, "keys") {
			m := model.(Model)
			if !m.core.Pane.Focus || m.core.Phase == core.Exited {
				return
			}
			before := m.input.Value()
			step(genKey(t))
			if model.(Model).input.Value() != before {
				t.Fatalf("input changed from %q to %q with the pane focused", before, model.(Model).input.Value())
			}
		}
	})
}

// While the board's input is open, enter and esc never type, a typed key
// types its text, and a modified key types nothing.
func TestBoardInputKeys(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		k := genKey(t)
		a, text := ClassifyBoardInput(k)
		switch k.String() {
		case "enter":
			if a != core.PaneEnter {
				t.Fatalf("enter gave %v", a)
			}
		case "esc":
			if a != core.PaneEsc {
				t.Fatalf("esc gave %v", a)
			}
		}
		if a == core.PaneChar && (text == "" || k.Mod&^tea.ModShift != 0) {
			t.Fatalf("key %q typed %q", k.String(), text)
		}
	})
	for _, k := range []tea.KeyPressMsg{{Code: 'x', Text: "x"}, {Code: 'X', Text: "X", Mod: tea.ModShift}, {Code: ' ', Text: " "}} {
		if a, text := ClassifyBoardInput(k); a != core.PaneChar || text != k.Text {
			t.Errorf("%q gave %v %q", k.String(), a, text)
		}
	}
	if ClassifyBoard(tea.KeyPressMsg{Code: 'x', Text: "x"}) != core.PaneMark || ClassifyPane(tea.KeyPressMsg{Code: 'b', Text: "b"}) != core.PaneBoard {
		t.Error("board keys")
	}
}
