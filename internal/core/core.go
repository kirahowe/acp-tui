// Package core holds the TUI's state and the pure function that changes it.
//
// Reduce takes the state and one event and returns the new state and the
// effects the shell should carry out. Nothing here reads the clock, does
// IO, or knows about Bubble Tea or ACP: every event carries its own time,
// and every effect is plain data.
package core

import (
	"strings"
	"time"
)

// Event is an input to Reduce. Only this package defines events.
type Event interface{ at() time.Time }

// Events from the agent, translated by package wire.
type (
	// Connected means initialize and session/new have finished.
	Connected struct {
		At    time.Time
		Agent string
	}
	TextChunk struct {
		At              time.Time
		MessageID, Text string
	}
	ThoughtChunk struct {
		At              time.Time
		MessageID, Text string
	}
	ToolStarted struct {
		At              time.Time
		ID, Title, Kind string
		Status          ToolStatus
	}
	// ToolUpdated changes only the fields that are not nil.
	ToolUpdated struct {
		At     time.Time
		ID     string
		Title  *string
		Status *ToolStatus
		Text   *string // first line of a failed call's content
	}
	Notice struct {
		At                      time.Time
		Severity, Title, Detail string
		RetryAt                 *time.Time
	}
	PromptDone struct {
		At   time.Time
		Stop StopReason
	}
	PromptFailed struct {
		At      time.Time
		Message string
	}
	AgentExited struct {
		At  time.Time
		Err string
	}
	// Ignored is an update the MVP does not show. It still counts as
	// activity, so the status line knows the agent is alive.
	Ignored struct{ At time.Time }
)

// Events from the user.
type (
	Submit struct {
		At   time.Time
		Text string
	}
	Interrupt struct {
		At         time.Time
		InputEmpty bool
	} // Ctrl-C
	EOF struct{ At time.Time } // Ctrl-D on an empty input
)

// Events from the shell.
type (
	Tick          struct{ At time.Time }
	TerminalReady struct{ At time.Time } // the first frame is on screen
	Printed       struct {
		At   time.Time
		Upto int
	} // entries [0, Upto) are in scrollback
)

func (e Connected) at() time.Time     { return e.At }
func (e TextChunk) at() time.Time     { return e.At }
func (e ThoughtChunk) at() time.Time  { return e.At }
func (e ToolStarted) at() time.Time   { return e.At }
func (e ToolUpdated) at() time.Time   { return e.At }
func (e Notice) at() time.Time        { return e.At }
func (e PromptDone) at() time.Time    { return e.At }
func (e PromptFailed) at() time.Time  { return e.At }
func (e AgentExited) at() time.Time   { return e.At }
func (e Ignored) at() time.Time       { return e.At }
func (e Submit) at() time.Time        { return e.At }
func (e Interrupt) at() time.Time     { return e.At }
func (e EOF) at() time.Time           { return e.At }
func (e Tick) at() time.Time          { return e.At }
func (e TerminalReady) at() time.Time { return e.At }
func (e Printed) at() time.Time       { return e.At }

// Effect is something Reduce asks the shell to do.
type Effect interface{ effect() }

type (
	SendPrompt struct{ Text string }
	SendCancel struct{}
	// Print asks the shell to put entries [From, To) into scrollback and
	// then send Printed{Upto: To}. Entries is a copy.
	Print struct {
		From, To int
		Entries  []Entry
	}
	// RestoreInput puts text that was queued but never sent back into the
	// input box, ahead of whatever is there.
	RestoreInput struct{ Text string }
	Quit         struct{}
)

func (SendPrompt) effect()   {}
func (SendCancel) effect()   {}
func (Print) effect()        {}
func (RestoreInput) effect() {}
func (Quit) effect()         {}

type Phase int

const (
	Starting Phase = iota
	Idle
	Busy
	Exited
)

type ToolStatus int

const (
	Pending ToolStatus = iota
	Running
	OK
	Failed
	Stopped // the turn ended while the call was still pending or running
)

// StopReason is ACP's stopReason. Values a newer agent might send pass
// through unchanged.
type StopReason string

const (
	EndTurn         StopReason = "end_turn"
	MaxTokens       StopReason = "max_tokens"
	MaxTurnRequests StopReason = "max_turn_requests"
	Refusal         StopReason = "refusal"
	Cancelled       StopReason = "cancelled"
)

type Activity int

const (
	Waiting Activity = iota
	Thinking
	Writing
)

type EntryKind int

const (
	User EntryKind = iota
	Agent
	Tool
	Note
)

// Entry is one item of the transcript. An agent message is split into one
// entry per markdown block (see SplitStable), so a long reply can reach
// scrollback a block at a time.
type Entry struct {
	Kind   EntryKind
	ID     string // message id or tool call id
	Text   string // user text, agent markdown, or note text
	Title  string // tool title
	Status ToolStatus
	Detail string // a failed tool's first content line, or a notice's description
	Final  bool   // nothing will change it again
}

type Retry struct {
	At    time.Time
	Title string
}

type Turn struct {
	Started, LastEvent time.Time
	Activity           Activity
	Cancelling         bool
	Retry              *Retry
}

type State struct {
	Phase   Phase
	Agent   string
	Entries []Entry
	Printed int      // entries [0, Printed) are in scrollback
	Asked   int      // entries [Printed, Asked) have a Print in flight
	Turn    Turn     // meaningful only while Busy
	Queue   []string // submitted while starting or busy, oldest first
	Ready   bool     // the terminal has drawn its first frame
	Exit    string   // why the agent went away, once Exited
}

// Reduce applies one event. It never mutates s's slices in place, so a
// caller may keep the old state.
func Reduce(s State, e Event) (State, []Effect) {
	s.Entries = append([]Entry(nil), s.Entries...)
	s.Queue = append([]string(nil), s.Queue...)
	r := &reducer{s: s}
	r.apply(e)
	r.print()
	return r.s, r.fx
}

type reducer struct {
	s  State
	fx []Effect
}

func (r *reducer) emit(e Effect) { r.fx = append(r.fx, e) }

func (r *reducer) apply(e Event) {
	s := &r.s
	switch e := e.(type) {
	case Connected:
		if s.Phase == Starting {
			s.Phase = Idle
			s.Agent = e.Agent
			r.sendQueue(e.At)
		}
	case TextChunk, ThoughtChunk, ToolStarted, ToolUpdated, Ignored:
		if s.Phase == Busy {
			s.Turn.LastEvent = e.at()
			s.Turn.Retry = nil
			r.agentEvent(e)
		}
	case Notice:
		if s.Phase == Busy {
			s.Turn.LastEvent = e.At
			s.Turn.Retry = nil
			if e.Severity == "warning" && e.RetryAt != nil {
				s.Turn.Retry = &Retry{At: *e.RetryAt, Title: e.Title}
				return
			}
		}
		if s.Phase != Exited {
			r.add(Entry{Kind: Note, Text: "! " + e.Title, Detail: e.Detail, Final: true})
		}
	case PromptDone:
		if s.Phase == Busy {
			r.endTurn(e.At, e.Stop != Cancelled, stopNote(e.Stop))
		}
	case PromptFailed:
		if s.Phase == Busy {
			r.endTurn(e.At, false, "error: "+e.Message)
		}
	case AgentExited:
		if s.Phase != Exited {
			r.closeAll()
			s.Phase = Exited
			s.Exit = e.Err
			r.restoreQueue()
			note := "the agent exited"
			if e.Err != "" {
				note += ": " + e.Err
			}
			r.add(Entry{Kind: Note, Text: note, Final: true})
			r.emit(Quit{})
		}
	case Submit:
		if strings.TrimSpace(e.Text) == "" {
			return
		}
		switch s.Phase {
		case Idle:
			r.send(e.Text, e.At)
		case Starting, Busy:
			s.Queue = append(s.Queue, e.Text)
		}
	case Interrupt:
		switch {
		case s.Phase == Busy && !s.Turn.Cancelling:
			s.Turn.Cancelling = true
			r.emit(SendCancel{})
		case s.Phase == Idle && !e.InputEmpty:
			// The shell clears the input box; there is nothing to stop.
		default:
			r.emit(Quit{})
		}
	case EOF:
		if s.Phase == Busy && !s.Turn.Cancelling {
			s.Turn.Cancelling = true
			r.emit(SendCancel{})
		}
		r.emit(Quit{})
	case TerminalReady:
		s.Ready = true
	case Printed:
		if e.Upto >= s.Printed && e.Upto <= s.Asked {
			s.Printed = e.Upto
		}
	case Tick:
		// Changes nothing; the shell redraws the status line.
	}
}

// agentEvent applies an event that belongs to the current turn.
func (r *reducer) agentEvent(e Event) {
	s := &r.s
	switch e := e.(type) {
	case TextChunk:
		s.Turn.Activity = Writing
		r.appendText(e.MessageID, e.Text)
	case ThoughtChunk:
		s.Turn.Activity = Thinking
	case ToolStarted:
		s.Turn.Activity = Waiting
		r.add(Entry{Kind: Tool, ID: e.ID, Title: e.Title, Status: e.Status, Final: done(e.Status)})
	case ToolUpdated:
		s.Turn.Activity = Waiting
		i := r.findTool(e.ID)
		if i < 0 {
			// Some agents may skip tool_call; show the update anyway.
			r.add(Entry{Kind: Tool, ID: e.ID, Title: e.ID})
			i = len(s.Entries) - 1
		}
		t := &s.Entries[i]
		if t.Final {
			return
		}
		if e.Title != nil {
			t.Title = *e.Title
		}
		if e.Status != nil {
			t.Status = *e.Status
			t.Final = done(t.Status)
		}
		if e.Text != nil {
			t.Detail = *e.Text
		}
	}
}

func done(st ToolStatus) bool { return st == OK || st == Failed || st == Stopped }

// findTool returns the index of the latest tool entry with this id, or -1.
func (r *reducer) findTool(id string) int {
	for i := len(r.s.Entries) - 1; i >= 0; i-- {
		if e := r.s.Entries[i]; e.Kind == Tool && e.ID == id {
			return i
		}
	}
	return -1
}

// appendText adds a chunk to the open agent entry, or opens one, and then
// moves every complete block of it into its own final entry.
func (r *reducer) appendText(id, text string) {
	es := r.s.Entries
	if n := len(es); n > 0 && es[n-1].Kind == Agent && !es[n-1].Final && (id == "" || es[n-1].ID == id) {
		es[n-1].Text += text
	} else {
		r.add(Entry{Kind: Agent, ID: id, Text: text})
	}
	open := r.s.Entries[len(r.s.Entries)-1]
	r.s.Entries = r.s.Entries[:len(r.s.Entries)-1]
	blocks, tail := splitBlocks(open.Text)
	for _, b := range blocks {
		r.s.Entries = append(r.s.Entries, Entry{Kind: Agent, ID: open.ID, Text: b, Final: true})
	}
	if tail != "" {
		r.s.Entries = append(r.s.Entries, Entry{Kind: Agent, ID: open.ID, Text: tail})
	}
}

// add appends an entry. A new entry closes an open agent entry before it.
func (r *reducer) add(e Entry) {
	if n := len(r.s.Entries); n > 0 && r.s.Entries[n-1].Kind == Agent {
		r.s.Entries[n-1].Final = true
	}
	r.s.Entries = append(r.s.Entries, e)
}

// closeAll marks every entry final; a call still in flight is stopped.
func (r *reducer) closeAll() {
	for i := range r.s.Entries {
		e := &r.s.Entries[i]
		if !e.Final {
			e.Final = true
			if e.Kind == Tool {
				e.Status = Stopped
			}
		}
	}
}

func (r *reducer) endTurn(at time.Time, sendNext bool, note string) {
	r.closeAll()
	if note != "" {
		r.add(Entry{Kind: Note, Text: note, Final: true})
	}
	r.s.Phase = Idle
	r.s.Turn = Turn{}
	if sendNext {
		r.sendQueue(at)
		return
	}
	// Nothing goes out after the user asked to stop or the agent failed.
	r.restoreQueue()
}

// restoreQueue hands queued text back to the input box, so nothing typed
// is lost.
func (r *reducer) restoreQueue() {
	if len(r.s.Queue) > 0 {
		r.emit(RestoreInput{Text: strings.Join(r.s.Queue, "\n\n")})
		r.s.Queue = nil
	}
}

// sendQueue sends everything queued as one prompt.
func (r *reducer) sendQueue(at time.Time) {
	if len(r.s.Queue) > 0 {
		text := strings.Join(r.s.Queue, "\n\n")
		r.s.Queue = nil
		r.send(text, at)
	}
}

func (r *reducer) send(text string, at time.Time) {
	r.add(Entry{Kind: User, Text: text, Final: true})
	r.s.Phase = Busy
	r.s.Turn = Turn{Started: at, LastEvent: at}
	r.emit(SendPrompt{Text: text})
}

func stopNote(r StopReason) string {
	switch r {
	case EndTurn:
		return ""
	case MaxTokens:
		return "[max tokens]"
	case MaxTurnRequests:
		return "[max turn requests]"
	default:
		return "[" + string(r) + "]"
	}
}

// print asks for the longest final prefix that has not been asked for.
func (r *reducer) print() {
	if !r.s.Ready {
		return
	}
	end := r.s.Asked
	for end < len(r.s.Entries) && r.s.Entries[end].Final {
		end++
	}
	if end > r.s.Asked {
		r.emit(Print{From: r.s.Asked, To: end, Entries: append([]Entry(nil), r.s.Entries[r.s.Asked:end]...)})
		r.s.Asked = end
	}
}
