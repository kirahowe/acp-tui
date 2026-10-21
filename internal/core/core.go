// Package core holds the TUI's state and the pure function that changes it.
//
// Reduce takes the state and one event and returns the new state and the
// effects the shell should carry out. Nothing here reads the clock, does
// IO, or knows about Bubble Tea or ACP: every event carries its own time,
// and every effect is plain data.
package core

import (
	"fmt"
	"strings"
	"time"
)

// Event is an input to Reduce. Only this package defines events.
type Event interface{ at() time.Time }

// Events from the agent, translated by package wire.
type (
	// Connected means initialize and session/new have finished. Agent,
	// Title and Version come from agentInfo; Model from a session config
	// option of category "model". Any may be empty.
	Connected struct {
		At                           time.Time
		Agent, Title, Version, Model string
		// Embedded says the agent takes embedded context in a prompt
		// (promptCapabilities.embeddedContext), so a paste can go as a
		// labelled resource.
		Embedded bool
	}
	// Usage is the agent's context window use, and what the session has
	// cost so far when the agent says. Size 0 means the window is unknown.
	Usage struct {
		At         time.Time
		Used, Size uint64
		Cost       *Cost
	}
	TextChunk struct {
		At              time.Time
		MessageID, Text string
	}
	ThoughtChunk struct {
		At              time.Time
		MessageID, Text string
	}
	// ToolStarted is a new call. Name is the tool's name, when the agent
	// gives it; Input is its arguments as JSON, when it gives them.
	ToolStarted struct {
		At                           time.Time
		ID, Name, Title, Kind, Input string
		Output                       string // when it arrives finished
		Status                       ToolStatus
		// Displays is what the call showed the operator, apart from its
		// output (see Display).
		Displays []Display
	}
	// ToolUpdated changes only the fields that are not nil.
	ToolUpdated struct {
		At       time.Time
		ID       string
		Title    *string
		Status   *ToolStatus
		Input    *string
		Output   *string // the call's text content
		Displays []Display
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
	// Printed says the shell has put everything before Upto into
	// scrollback, or committed to.
	Printed struct {
		At   time.Time
		Upto Cursor
	}
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
func (e Usage) at() time.Time         { return e.At }
func (e Submit) at() time.Time        { return e.At }
func (e Interrupt) at() time.Time     { return e.At }
func (e EOF) at() time.Time           { return e.At }
func (e Tick) at() time.Time          { return e.At }
func (e TerminalReady) at() time.Time { return e.At }
func (e Printed) at() time.Time       { return e.At }

// Effect is something Reduce asks the shell to do.
type Effect interface{ effect() }

type (
	// SendPrompt sends a prompt: the text, and the pastes as resources.
	SendPrompt struct {
		Text        string
		Attachments []Attachment
	}
	SendCancel struct{}
	// RestoreInput puts text that was queued but never sent back into the
	// input box, ahead of whatever is there.
	RestoreInput struct{ Text string }
	Quit         struct{}
)

func (SendPrompt) effect()   {}
func (SendCancel) effect()   {}
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
	// Tool is a run of tool calls, one after another with nothing
	// between them. It shows as one line.
	Tool
	Note
	// Banner is printed once, when the session starts: Title, and the
	// agent's name, version and model in Text.
	Banner
	// Thought is the agent's thinking: one line inline, the text itself
	// in the transcript view.
	Thought
	// Request is a permission request's whole input, shown at the user's
	// request: Text names the call, Detail is its arguments.
	Request
)

// Call is one tool call.
type Call struct {
	ID, Name, Title string
	Status          ToolStatus
	Input           string // the arguments as JSON, when the agent sent them
	Output          string // the call's text content
}

// Label is what to call the call: the tool's name, else the title.
func (c Call) Label() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Title
}

// Entry is one item of the transcript. An agent message is split into one
// entry per markdown block (see SplitStable), so a long reply can reach
// scrollback a block at a time.
type Entry struct {
	Kind   EntryKind
	ID     string // message id
	Text   string // user text, agent markdown, or note text
	Title  string // the banner's title
	Detail string // a notice's description
	Calls  []Call // a Tool run's calls
	Final  bool   // nothing will change it again
	// Defang says the session was contaminated when the agent wrote this,
	// so URLs in it show defanged. See render.Defang.
	Defang bool
	// Preview is relay's description of a request, shown above its
	// input, always defanged. PreviewDefang says the input's URLs show
	// defanged too.
	Preview       string
	PreviewDefang bool
}

// Cost is an amount of money: the session's spend so far.
type Cost struct {
	Amount   float64
	Currency string // ISO 4217, such as USD
}

type Retry struct {
	At     time.Time
	Reason string // the notice's description, else its title
}

type Turn struct {
	Started, LastEvent time.Time
	Activity           Activity
	Cancelling         bool
	Retry              *Retry
}

// Cursor is a place in the transcript: line Line of entry Entry, as
// rendered with the blank line above it, if any. Entries scroll into
// scrollback a line at a time, so a long one can be part printed.
type Cursor struct{ Entry, Line int }

// Before reports whether c comes before d.
func (c Cursor) Before(d Cursor) bool {
	return c.Entry < d.Entry || (c.Entry == d.Entry && c.Line < d.Line)
}

type State struct {
	// Title, set by the shell before the first event, names the session
	// in the banner. Empty means use the agent's own title or name.
	Title   string
	Phase   Phase
	Agent   string
	Usage   *Usage // the last context use the agent reported
	Spend   *Cost  // the last session cost the agent reported
	Entries []Entry
	Printed Cursor   // everything before it is in scrollback
	Turn    Turn     // meaningful only while Busy
	Queue   []string // submitted while starting or busy, oldest first
	Ready   bool     // the terminal has drawn its first frame
	Exit    string   // why the agent went away, once Exited
	// Labels is everything the session has seen, as far as the agent has
	// said. It only grows.
	Labels Labels
	// Asks is the permission requests waiting for the person, oldest
	// first; the first is on offer and Pick is its highlighted option.
	Asks []Permission
	Pick int
	// Embedded says the agent takes embedded context. Pastes holds the
	// pastes whose placeholders may still be in the input, or in a queued
	// message; NextPaste numbers them. TrustNext says the next paste goes
	// in as trusted text.
	Embedded  bool
	Pastes    []Paste
	NextPaste int
	TrustNext bool
	// Pane is the workspace pane.
	Pane Pane
	// Transcript is the full-screen view; Help says the list of keys
	// shows.
	Transcript Transcript
	Help       bool
	// Shared says this is one conversation among several on a hub
	// connection; see shared.go.
	Shared bool
}

// Reduce applies one event. It never mutates s's slices in place, so a
// caller may keep the old state.
func Reduce(s State, e Event) (State, []Effect) {
	s.Entries = append([]Entry(nil), s.Entries...)
	for i := range s.Entries {
		if !s.Entries[i].Final { // a final entry's calls never change
			s.Entries[i].Calls = append([]Call(nil), s.Entries[i].Calls...)
		}
	}
	s.Queue = append([]string(nil), s.Queue...)
	s.Asks = append([]Permission(nil), s.Asks...)
	s.Pastes = append([]Paste(nil), s.Pastes...)
	s.Pane.Server = append([]Item(nil), s.Pane.Server...)
	s.Pane.Local = append([]Item(nil), s.Pane.Local...)
	s.Pane.Cites = append([]Citation(nil), s.Pane.Cites...)
	s.Pane.Board.Cards = append([]Card(nil), s.Pane.Board.Cards...)
	r := &reducer{s: s}
	r.apply(e)
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
			s.Embedded = e.Embedded
			s.Agent = e.Agent
			r.add(banner(s.Title, e))
			r.refresh()
			r.askCards()
			r.sendQueue(e.At)
		}
	case Usage:
		if s.Phase == Busy {
			s.Turn.LastEvent = e.At
			s.Turn.Retry = nil
		}
		if s.Phase != Exited {
			s.Usage = &e
			if e.Cost != nil {
				c := *e.Cost
				s.Spend = &c
			}
		}
	case Pasted:
		r.pasted(e)
	case ToggleTrustedPaste:
		s.TrustNext = !s.TrustNext
	case ItemsLoaded, CitationResolved, TrustDone, PaneKey, CardsLoaded, CardsChanged, CardOpDone:
		r.pane(e)
	case PermissionAsked:
		r.closeTranscript()
		r.permission(e)
	case LabelsSeen, PermissionMove, PermissionConfirm, PermissionQuick, PermissionCancel, PermissionShowAll:
		r.permission(e)
	case UserChunk, ConvAttached, Settled, RemoteIdle, Focused:
		r.shared(e)
	case TextChunk, ThoughtChunk, ToolStarted, ToolUpdated, Ignored:
		if s.Phase == Busy || (s.Shared && s.Phase != Exited) {
			if s.Phase == Busy {
				s.Turn.LastEvent = e.at()
				s.Turn.Retry = nil
			}
			r.agentEvent(e)
		}
	case Notice:
		if s.Phase == Busy {
			s.Turn.LastEvent = e.At
			s.Turn.Retry = nil
			if e.Severity == "warning" && e.RetryAt != nil {
				reason := e.Detail
				if reason == "" {
					reason = e.Title
				}
				s.Turn.Retry = &Retry{At: *e.RetryAt, Reason: reason}
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
			r.cancelAsks()
			r.closeAll()
			r.closeTranscript()
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
			r.cancelAsks()
			r.emit(SendCancel{})
		case s.Phase == Idle && !e.InputEmpty:
			// The shell clears the input box; there is nothing to stop.
		default:
			r.cancelAsks()
			r.closeTranscript()
			r.emit(Quit{})
		}
	case ShowTools:
		r.show(&s.Transcript.Tools)
	case ShowThinking:
		r.show(&s.Transcript.Thinking)
	case TranscriptKey:
		r.transcriptKey(e)
	case ToggleHelp:
		s.Help = !s.Help
	case EOF:
		r.cancelAsks()
		r.closeTranscript() // what it held back from scrollback prints before the end
		if s.Phase == Busy && !s.Turn.Cancelling {
			s.Turn.Cancelling = true
			r.emit(SendCancel{})
		}
		r.emit(Quit{})
	case TerminalReady:
		s.Ready = true
	case Printed:
		if s.Ready && !e.Upto.Before(s.Printed) && s.printable(e.Upto) {
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
		r.citeLatest()
	case ThoughtChunk:
		s.Turn.Activity = Thinking
		r.appendThought(e.MessageID, e.Text)
	case ToolStarted:
		s.Turn.Activity = Waiting
		r.startCall(Call{ID: e.ID, Name: e.Name, Title: e.Title, Status: e.Status, Input: e.Input, Output: e.Output})
		r.displays(e.ID, e.Displays)
	case ToolUpdated:
		s.Turn.Activity = Waiting
		r.displays(e.ID, e.Displays)
		i, j := r.findCall(e.ID)
		if i < 0 {
			// Some agents may skip tool_call; show the update anyway.
			r.startCall(Call{ID: e.ID, Title: e.ID})
			i, j = r.findCall(e.ID)
		}
		run := &s.Entries[i]
		if run.Final {
			return
		}
		run.Defang = run.Defang || s.Labels.Contaminated // its output may be what contaminated it
		c := &run.Calls[j]
		if e.Title != nil {
			c.Title = *e.Title
		}
		if e.Status != nil {
			c.Status = *e.Status
		}
		if e.Input != nil {
			c.Input = *e.Input
		}
		if e.Output != nil {
			c.Output = *e.Output
		}
		// A run still taking calls stays open; one that something else
		// has followed is final once its calls are.
		run.Final = i != len(s.Entries)-1 && allDone(run.Calls)
	}
}

func done(st ToolStatus) bool { return st == OK || st == Failed || st == Stopped }

func allDone(cs []Call) bool {
	for _, c := range cs {
		if !done(c.Status) {
			return false
		}
	}
	return true
}

// startCall adds a call to the open run, or starts a run.
func (r *reducer) startCall(c Call) {
	es := r.s.Entries
	if n := len(es); n > 0 && es[n-1].Kind == Tool && !es[n-1].Final {
		es[n-1].Calls = append(es[n-1].Calls, c)
		es[n-1].Defang = es[n-1].Defang || r.s.Labels.Contaminated
		return
	}
	r.add(Entry{Kind: Tool, Calls: []Call{c}})
}

// findCall returns where the latest call with this id is: its run's
// index and its index in the run, or -1, -1.
func (r *reducer) findCall(id string) (int, int) {
	for i := len(r.s.Entries) - 1; i >= 0; i-- {
		e := r.s.Entries[i]
		if e.Kind != Tool {
			continue
		}
		for j := len(e.Calls) - 1; j >= 0; j-- {
			if e.Calls[j].ID == id {
				return i, j
			}
		}
	}
	return -1, -1
}

// appendText adds a chunk to the open agent entry, or opens one, and then
// moves every complete block of it into its own final entry.
func (r *reducer) appendText(id, text string) {
	es := r.s.Entries
	if n := len(es); n > 0 && es[n-1].Kind == Agent && !es[n-1].Final && (id == "" || es[n-1].ID == id) {
		es[n-1].Text += text
		es[n-1].Defang = es[n-1].Defang || r.s.Labels.Contaminated
	} else {
		r.add(Entry{Kind: Agent, ID: id, Text: text})
	}
	open := r.s.Entries[len(r.s.Entries)-1]
	r.s.Entries = r.s.Entries[:len(r.s.Entries)-1]
	blocks, tail := splitBlocks(open.Text)
	for _, b := range blocks {
		r.s.Entries = append(r.s.Entries, Entry{Kind: Agent, ID: open.ID, Text: b, Final: true, Defang: open.Defang})
	}
	if tail != "" {
		r.s.Entries = append(r.s.Entries, Entry{Kind: Agent, ID: open.ID, Text: tail, Defang: open.Defang})
	}
}

// add appends an entry. A new entry closes the open agent entry or run
// of tool calls before it; the run is final once its calls are.
func (r *reducer) add(e Entry) {
	if e.Kind != User && e.Kind != Banner && e.Kind != Request { // the operator's own words, and what is being approved, show as they are
		e.Defang = e.Defang || r.s.Labels.Contaminated
	}
	if n := len(r.s.Entries); n > 0 {
		switch last := &r.s.Entries[n-1]; last.Kind {
		case Agent, Thought:
			last.Final = true
		case Tool:
			last.Final = allDone(last.Calls)
		}
	}
	r.s.Entries = append(r.s.Entries, e)
}

// closeAll marks every entry final; a call still in flight is stopped.
func (r *reducer) closeAll() {
	for i := range r.s.Entries {
		e := &r.s.Entries[i]
		if !e.Final {
			e.Final = true
			for j := range e.Calls {
				if !done(e.Calls[j].Status) {
					e.Calls[j].Status = Stopped
				}
			}
		}
	}
}

func (r *reducer) endTurn(at time.Time, sendNext bool, note string) {
	r.cancelAsks()
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
	sent, atts, inlined := r.resolve(text)
	if inlined > 0 {
		r.add(Entry{Kind: Note, Text: fmt.Sprintf("! %d paste(s) went in as plain text: the agent takes no attachments, so they were not labelled untrusted", inlined), Final: true})
	}
	r.emit(SendPrompt{Text: sent, Attachments: atts})
}

// banner names the session: the shell's title, else the agent's title,
// else its name. The body is the agent's name and version, and the model
// when the agent said which.
func banner(title string, c Connected) Entry {
	for _, t := range []string{title, c.Title, c.Agent, "agent"} {
		if strings.TrimSpace(t) != "" {
			title = t
			break
		}
	}
	var body []string
	if who := strings.TrimSpace(c.Agent + " " + c.Version); who != "" {
		body = append(body, who)
	}
	if c.Model != "" {
		body = append(body, c.Model)
	}
	return Entry{Kind: Banner, Title: title, Text: strings.Join(body, " · "), Final: true}
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

// printable reports whether everything before c is final, so it can go
// into scrollback.
func (s *State) printable(c Cursor) bool {
	if c.Entry < 0 || c.Line < 0 || c.Entry > len(s.Entries) || (c.Entry == len(s.Entries) && c.Line > 0) {
		return false
	}
	end := c.Entry
	if c.Line > 0 {
		end++
	}
	for _, e := range s.Entries[s.Printed.Entry:end] {
		if !e.Final {
			return false
		}
	}
	return true
}
