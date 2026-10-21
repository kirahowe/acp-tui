package core

import "time"

// Transcript is the full-screen view of the whole session, where tool
// calls and thinking can be shown in full. The inline view prints into
// the terminal's scrollback, which cannot be redrawn, so a block can only
// be opened and closed here.
type Transcript struct {
	Open bool
	// Tools and Thinking say each kind of block shows in full. They keep
	// their values between openings.
	Tools, Thinking bool
	// Scroll is how many lines above the bottom the view ends; 0 follows
	// the end as it grows.
	Scroll int
}

// TranscriptAction is what a key does in the transcript view.
type TranscriptAction int

const (
	TranscriptUp TranscriptAction = iota
	TranscriptDown
	TranscriptPageUp
	TranscriptPageDown
	TranscriptTop
	TranscriptBottom
	TranscriptClose
)

// Events from the user.
type (
	// ShowTools opens the transcript with tool calls in full, or, when
	// it is open, shows or hides them. ShowThinking does the same for
	// thinking.
	ShowTools    struct{ At time.Time }
	ShowThinking struct{ At time.Time }
	// TranscriptKey moves or closes the transcript view. Max is how far
	// it can scroll as drawn, and Page how many lines a page is.
	TranscriptKey struct {
		At        time.Time
		Action    TranscriptAction
		Max, Page int
	}
	// ToggleHelp shows or hides the list of keys.
	ToggleHelp struct{ At time.Time }
)

func (e ShowTools) at() time.Time     { return e.At }
func (e ShowThinking) at() time.Time  { return e.At }
func (e TranscriptKey) at() time.Time { return e.At }
func (e ToggleHelp) at() time.Time    { return e.At }

// show opens the transcript with one kind of block in full, or flips that
// kind when it is already open.
func (r *reducer) show(kind *bool) {
	t := &r.s.Transcript
	if t.Open {
		*kind = !*kind
		return
	}
	t.Open, *kind, t.Scroll = true, true, 0
	r.s.Help = false
}

func (r *reducer) transcriptKey(e TranscriptKey) {
	t := &r.s.Transcript
	if !t.Open || (e.Action == TranscriptClose && r.s.Shared) {
		return
	}
	d := 0
	switch e.Action {
	case TranscriptUp:
		d = 1
	case TranscriptDown:
		d = -1
	case TranscriptPageUp:
		d = max(e.Page, 1)
	case TranscriptPageDown:
		d = -max(e.Page, 1)
	case TranscriptTop:
		t.Scroll = max(e.Max, 0)
	case TranscriptBottom:
		t.Scroll = 0
	case TranscriptClose:
		t.Open, t.Scroll = false, 0
		r.s.Help = false
	}
	t.Scroll = min(max(t.Scroll+d, 0), max(e.Max, 0))
}

// closeTranscript leaves the view, so what needs the person, such as a
// permission request, is on screen.
func (r *reducer) closeTranscript() {
	if r.s.Shared {
		return // the hub's conversation view is the transcript
	}
	r.s.Transcript.Open, r.s.Transcript.Scroll = false, 0
	r.s.Help = false
}

// appendThought adds a chunk to the open thought, or opens one.
func (r *reducer) appendThought(id, text string) {
	es := r.s.Entries
	if n := len(es); n > 0 && es[n-1].Kind == Thought && !es[n-1].Final && (id == "" || es[n-1].ID == id) {
		es[n-1].Text += text
		es[n-1].Defang = es[n-1].Defang || r.s.Labels.Contaminated
		return
	}
	if text != "" {
		r.add(Entry{Kind: Thought, ID: id, Text: text})
	}
}
