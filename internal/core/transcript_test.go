package core

import (
	"testing"

	"pgregory.net/rapid"
)

// Thinking streams into one entry, which the reply after it closes, so
// it never holds the reply back from scrollback.
func TestThoughtIsAnEntry(t *testing.T) {
	s, _ := run(Connected{At: t0}, Submit{At: t0, Text: "a"},
		ThoughtChunk{At: t0, MessageID: "m1", Text: "Look at "},
		ThoughtChunk{At: t0, MessageID: "m1", Text: "the parser."})
	last := s.Entries[len(s.Entries)-1]
	if last.Kind != Thought || last.Text != "Look at the parser." || last.Final {
		t.Fatalf("thought %+v", last)
	}
	s, _ = Reduce(s, TextChunk{At: t0, MessageID: "m1", Text: "Fixed"})
	if th := s.Entries[len(s.Entries)-2]; th.Kind != Thought || !th.Final {
		t.Fatalf("the reply left the thought open: %+v", th)
	}
}

// Either key opens the view with its kind in full; while open, each
// flips its own kind. The settings outlast closing the view.
func TestShowTogglesEachKind(t *testing.T) {
	s, _ := run(Connected{At: t0}, ShowTools{At: t0})
	if tr := s.Transcript; !tr.Open || !tr.Tools || tr.Thinking {
		t.Fatalf("after ctrl+t: %+v", tr)
	}
	s, _ = Reduce(s, ShowThinking{At: t0})
	s, _ = Reduce(s, ShowTools{At: t0})
	if tr := s.Transcript; !tr.Open || tr.Tools || !tr.Thinking {
		t.Fatalf("after ctrl+o, ctrl+t: %+v", tr)
	}
	s, _ = Reduce(s, TranscriptKey{At: t0, Action: TranscriptClose})
	if s.Transcript.Open {
		t.Fatal("still open")
	}
	s, _ = Reduce(s, ShowThinking{At: t0})
	if tr := s.Transcript; !tr.Open || tr.Tools || !tr.Thinking {
		t.Fatalf("reopened: %+v", tr)
	}
}

// A permission request must not wait behind the view: it closes it, and
// the list of keys with it.
func TestPermissionClosesTranscript(t *testing.T) {
	s := busyState()
	s, _ = Reduce(s, ShowTools{At: t0})
	s, _ = Reduce(s, ToggleHelp{At: t0})
	s, _ = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: 1, Name: "web_fetch", Options: []PermOption{{ID: "allow", Kind: AllowOnce}}}})
	if s.Transcript.Open || s.Help || len(s.Asks) != 1 {
		t.Fatalf("transcript %+v, help %v, asks %d", s.Transcript, s.Help, len(s.Asks))
	}
}

// The view never scrolls past either end, whatever the keys and however
// the content's height changes between them.
func TestTranscriptScrollStaysInRange(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, _ := run(Connected{At: t0}, ShowTools{At: t0})
		for _, k := range rapid.SliceOf(rapid.IntRange(0, int(TranscriptBottom))).Draw(t, "keys") {
			mx := rapid.IntRange(-2, 50).Draw(t, "max")
			s, _ = Reduce(s, TranscriptKey{At: t0, Action: TranscriptAction(k), Max: mx, Page: rapid.IntRange(-1, 20).Draw(t, "page")})
			if s.Transcript.Scroll < 0 || s.Transcript.Scroll > max(mx, 0) {
				t.Fatalf("scroll %d with max %d", s.Transcript.Scroll, mx)
			}
		}
	})
}

// A paste that goes nowhere uses up an armed trust, so the next paste,
// perhaps from somewhere else, goes in as untrusted.
func TestDroppedPasteDisarmsTrust(t *testing.T) {
	cases := map[string]func(State) State{
		"transcript": func(s State) State { s, _ = Reduce(s, ShowTools{At: t0}); return s },
		"pane":       func(s State) State { s.Pane.Focus = true; return s },
		"request": func(s State) State {
			s, _ = Reduce(s, Submit{At: t0, Text: "a"})
			s, _ = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: 1, Options: []PermOption{{ID: "allow", Kind: AllowOnce}}}})
			return s
		},
	}
	for name, away := range cases {
		s, _ := run(Connected{At: t0, Embedded: true}, ToggleTrustedPaste{At: t0})
		s = away(s)
		s, fx := Reduce(s, Pasted{At: t0, Text: "meant to be trusted"})
		if s.TrustNext || len(fx) != 0 {
			t.Errorf("%s: trust %v, effects %+v", name, s.TrustNext, fx)
		}
	}
}

// Quitting leaves the view, so what it held back can still print.
func TestQuitClosesTranscript(t *testing.T) {
	for _, quit := range []Event{EOF{At: t0}, Interrupt{At: t0, InputEmpty: true}} {
		s, _ := run(Connected{At: t0}, ShowThinking{At: t0}, quit)
		if s.Transcript.Open {
			t.Errorf("%T left the transcript open", quit)
		}
	}
}
