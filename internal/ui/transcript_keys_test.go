package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kirahowe/acp-tui/internal/core"
	"github.com/kirahowe/acp-tui/internal/render"
)

// press is the key press Bubble Tea reports for a key it names so.
func press(t *testing.T, name string) tea.KeyPressMsg {
	t.Helper()
	codes := map[string]rune{"enter": tea.KeyEnter, "esc": tea.KeyEscape, "up": tea.KeyUp, "down": tea.KeyDown,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd, "space": tea.KeySpace}
	var k tea.KeyPressMsg
	mods, key, ok := strings.Cut(name, "+")
	if ok && len(key) > 0 {
		switch mods {
		case "ctrl":
			k.Mod = tea.ModCtrl
		case "shift":
			k.Mod = tea.ModShift
		}
	} else {
		key = name
	}
	if c, ok := codes[key]; ok {
		k.Code = c
	} else {
		k.Code = []rune(key)[0]
		if k.Mod == 0 {
			k.Text = key
		}
	}
	if got := k.String(); got != name {
		t.Fatalf("press(%q) is %q", name, got)
	}
	return k
}

// Every key the list of keys names does something there.
func TestHelpRowsMatchKeys(t *testing.T) {
	for _, r := range render.InlineKeys {
		for _, name := range r.Keys {
			if a := Classify(press(t, name), true); a == PassThrough {
				t.Errorf("inline %q (%s) does nothing", name, r.What)
			}
		}
	}
	for _, r := range render.TranscriptKeys {
		for _, name := range r.Keys {
			if a, _ := ClassifyTranscript(press(t, name), true); a == PassThrough {
				t.Errorf("transcript %q (%s) does nothing", name, r.What)
			}
		}
	}
}

func TestTranscriptKeys(t *testing.T) {
	cases := []struct {
		name string
		want Action
		move core.TranscriptAction
	}{
		{"ctrl+t", ShowTools, 0}, {"t", ShowTools, 0},
		{"ctrl+o", ShowThinking, 0}, {"o", ShowThinking, 0},
		{"up", Move, core.TranscriptUp}, {"k", Move, core.TranscriptUp},
		{"space", Move, core.TranscriptPageDown}, {"G", Move, core.TranscriptBottom},
		{"esc", Move, core.TranscriptClose}, {"q", Move, core.TranscriptClose},
		{"ctrl+c", Interrupt, 0}, {"?", Help, 0},
		{"enter", PassThrough, 0}, {"y", PassThrough, 0},
	}
	for _, c := range cases {
		if a, m := ClassifyTranscript(press(t, c.name), true); a != c.want || m != c.move {
			t.Errorf("%q: %v %v, want %v %v", c.name, a, m, c.want, c.move)
		}
	}
}

// ? opens the list only from an empty input, so a message can still
// have one; ctrl+t no longer arms a trusted paste, ctrl+y does.
func TestInlineKeys(t *testing.T) {
	if a := Classify(press(t, "?"), false); a != PassThrough {
		t.Errorf("? with text: %v", a)
	}
	for name, want := range map[string]Action{"ctrl+t": ShowTools, "ctrl+o": ShowThinking, "ctrl+y": TrustPaste, "ctrl+_": Help} {
		if a := Classify(press(t, name), false); a != want {
			t.Errorf("%q: %v, want %v", name, a, want)
		}
	}
}

// The agent exiting while the view is open leaves the view, then stops
// the program once nothing is left to print, so it neither loses lines
// nor hangs.
func TestAgentExitFromTranscript(t *testing.T) {
	step := func(m Model, msg tea.Msg) Model {
		next, _ := m.Update(msg)
		return next.(Model)
	}
	m := New(nopConn{}, "")
	m = step(m, tea.WindowSizeMsg{Width: 40, Height: 10})
	m = step(m, readyMsg{})
	m = step(m, EventMsg{Event: core.Connected{}})
	m = step(m, printedMsg{})
	m = step(m, press(t, "ctrl+t"))
	if !m.alt {
		t.Fatal("the view did not open")
	}
	m = step(m, EventMsg{Event: core.AgentExited{Err: "gone"}})
	if m.alt || !m.back || m.stopping {
		t.Fatalf("after the exit: alt %v back %v stopping %v", m.alt, m.back, m.stopping)
	}
	m = step(m, backMsg{})
	for i := 0; m.printing && i < 10; i++ {
		m = step(m, printedMsg{})
	}
	if !m.stopping {
		t.Fatal("the program never stops")
	}
}
