package core

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

var t0 = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

// step is one Reduce call in a generated session.
type step struct {
	before, after State
	event         Event
	effects       []Effect
}

// genSession drives Reduce with a random mix of user, agent and shell
// events, including malformed ones: updates for unknown ids, chunks while
// idle, responses with no prompt, duplicated responses, bogus acks.
func genSession(t *rapid.T) []step {
	var (
		s     State
		now   = t0
		steps []step
		n     int // submissions so far, for unique texts
	)
	str := func(label string) string {
		return rapid.SampledFrom([]string{"", "x", "a b", "é", "世界", "\t"}).Draw(t, label)
	}
	gens := []func() Event{
		func() Event { return Connected{At: now, Agent: "fake"} },
		func() Event {
			return TextChunk{At: now, MessageID: rapid.SampledFrom([]string{"", "a1", "a2"}).Draw(t, "mid"),
				Text: rapid.SampledFrom([]string{"hi", " ", "\n", "\n\n", "```\n", "~~~", "é", "**b**"}).Draw(t, "text")}
		},
		func() Event { return ThoughtChunk{At: now, MessageID: "a1", Text: str("thought")} },
		func() Event {
			return ToolStarted{At: now, ID: toolID(t), Title: str("title"), Kind: "read",
				Status: ToolStatus(rapid.IntRange(int(Pending), int(Failed)).Draw(t, "status"))}
		},
		func() Event {
			u := ToolUpdated{At: now, ID: toolID(t)}
			if rapid.Bool().Draw(t, "hasTitle") {
				title := str("title")
				u.Title = &title
			}
			if rapid.Bool().Draw(t, "hasStatus") {
				st := ToolStatus(rapid.IntRange(int(Pending), int(Failed)).Draw(t, "status"))
				u.Status = &st
			}
			if rapid.Bool().Draw(t, "hasOutput") {
				out := str("output")
				u.Output = &out
			}
			return u
		},
		func() Event {
			e := Notice{At: now, Severity: rapid.SampledFrom([]string{"info", "warning", "error"}).Draw(t, "sev"), Title: str("title")}
			if rapid.Bool().Draw(t, "retry") {
				at := now.Add(time.Duration(rapid.IntRange(-5000, 20000).Draw(t, "retryMs")) * time.Millisecond)
				e.RetryAt = &at
			}
			return e
		},
		func() Event {
			return PromptDone{At: now, Stop: rapid.SampledFrom([]StopReason{EndTurn, EndTurn, MaxTokens, MaxTurnRequests, Refusal, Cancelled, "new_reason"}).Draw(t, "stop")}
		},
		func() Event { return PromptFailed{At: now, Message: "boom"} },
		func() Event { return Ignored{At: now} },
		func() Event {
			n := rapid.OneOf(rapid.SampledFrom([]uint64{0, 1, 15, 200000, 1<<64 - 1}), rapid.Uint64())
			u := Usage{At: now, Used: n.Draw(t, "used"), Size: n.Draw(t, "size")}
			if rapid.Bool().Draw(t, "hasCost") {
				u.Cost = &Cost{Amount: rapid.Float64Range(0, 100).Draw(t, "cost"), Currency: "USD"}
			}
			return u
		},
		func() Event {
			if rapid.IntRange(0, 4).Draw(t, "blank") == 0 {
				return Submit{At: now, Text: rapid.SampledFrom([]string{"", " ", "\n", "\t \n"}).Draw(t, "blankText")}
			}
			n++
			return Submit{At: now, Text: fmt.Sprintf("m%d%s", n, str("suffix"))}
		},
		func() Event { return Interrupt{At: now, InputEmpty: rapid.Bool().Draw(t, "empty")} },
		func() Event { return Tick{At: now} },
		func() Event { return TerminalReady{At: now} },
		func() Event {
			// Mostly a place at or after the cursor, as the shell sends;
			// sometimes anywhere, valid or not.
			c := Cursor{Entry: rapid.IntRange(-2, 50).Draw(t, "entry"), Line: rapid.IntRange(-1, 3).Draw(t, "line")}
			if rapid.IntRange(0, 4).Draw(t, "bogus") > 0 {
				c = Cursor{Entry: rapid.IntRange(s.Printed.Entry, len(s.Entries)).Draw(t, "entry"), Line: rapid.IntRange(0, 3).Draw(t, "line")}
			}
			return Printed{At: now, Upto: c}
		},
	}
	// Rare events: they end the session, so they are drawn less often.
	rare := []func() Event{
		func() Event { return AgentExited{At: now, Err: str("err")} },
		func() Event { return EOF{At: now} },
	}
	// Most sessions start the usual way, so that more of them get busy.
	var prelude []Event
	if rapid.IntRange(0, 3).Draw(t, "usualStart") > 0 {
		prelude = []Event{Connected{At: now}, TerminalReady{At: now}, Submit{At: now, Text: "m0"}}
	}
	count := rapid.IntRange(0, 120).Draw(t, "events")
	for i := range len(prelude) + count {
		now = now.Add(time.Duration(rapid.IntRange(0, 15000).Draw(t, "gapMs")) * time.Millisecond)
		var e Event
		switch {
		case i < len(prelude):
			e = prelude[i]
		case rapid.IntRange(0, 60).Draw(t, "rare") == 0:
			e = rapid.SampledFrom(rare).Draw(t, "rareKind")()
		default:
			e = gens[rapid.IntRange(0, len(gens)-1).Draw(t, "kind")]()
		}
		after, fx := Reduce(s, e)
		steps = append(steps, step{before: s, after: after, event: e, effects: fx})
		s = after
	}
	return steps
}

func toolID(t *rapid.T) string {
	return rapid.SampledFrom([]string{"c1", "c2", "c3"}).Draw(t, "toolID")
}

func final(steps []step) State {
	if len(steps) == 0 {
		return State{}
	}
	return steps[len(steps)-1].after
}

// P1: Reduce is total and keeps its invariants.
func TestReduceTotal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		outstanding := false
		for i, st := range genSession(t) {
			s := st.after
			// A response ends the prompt in flight before the reducer may
			// send the next one.
			switch st.event.(type) {
			case PromptDone, PromptFailed:
				if st.before.Phase == Busy {
					outstanding = false
				}
			case AgentExited:
				outstanding = false
			}
			for _, f := range st.effects {
				if _, ok := f.(SendPrompt); ok {
					outstanding = true
				}
			}
			if p := s.Printed; p.Entry < 0 || p.Line < 0 || p.Entry > len(s.Entries) || (p.Entry == len(s.Entries) && p.Line > 0) {
				t.Fatalf("step %d: Printed %+v with %d entries", i, p, len(s.Entries))
			}
			if st.after.Printed.Before(st.before.Printed) {
				t.Fatalf("step %d: Printed went back from %+v to %+v", i, st.before.Printed, s.Printed)
			}
			if (s.Phase == Busy) != outstanding {
				t.Fatalf("step %d: phase %v, prompt outstanding %v", i, s.Phase, outstanding)
			}
			for j, e := range s.Entries {
				if s.Phase != Busy && !e.Final {
					t.Fatalf("step %d: entry %d open while not busy: %+v", i, j, e)
				}
				if e.Kind == Agent && !e.Final && j != len(s.Entries)-1 {
					t.Fatalf("step %d: open agent entry %d is not last", i, j)
				}
				if e.Kind != Tool && !e.Final && e.Kind != Agent {
					t.Fatalf("step %d: entry %d of kind %v is open", i, j, e.Kind)
				}
				if e.Kind == Tool && e.Final && !allDone(e.Calls) {
					t.Fatalf("step %d: run %d is final with a call not done: %+v", i, j, e.Calls)
				}
				if e.Kind == Tool && !e.Final && j != len(s.Entries)-1 && allDone(e.Calls) {
					t.Fatalf("step %d: run %d is closed and done but not final", i, j)
				}
				if e.Kind == Tool && len(e.Calls) == 0 {
					t.Fatalf("step %d: run %d has no calls", i, j)
				}
			}
			if !s.Ready && s.Printed != (Cursor{}) {
				t.Fatalf("step %d: printed before the terminal was ready", i)
			}
		}
	})
}

// P2: at most one prompt is ever in flight.
func TestOnePromptInFlight(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		outstanding := false
		for i, st := range genSession(t) {
			switch st.event.(type) {
			case PromptDone, PromptFailed:
				if st.before.Phase == Busy {
					outstanding = false
				}
			case AgentExited:
				outstanding = false
			}
			for _, f := range st.effects {
				if _, ok := f.(SendPrompt); ok {
					if outstanding {
						t.Fatalf("step %d: SendPrompt while a prompt is in flight", i)
					}
					outstanding = true
				}
			}
		}
	})
}

// P3: every accepted submission is sent exactly once, in order, or handed
// back to the input, or is still queued.
func TestQueueLossless(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genSession(t)
		var submitted, out []string
		for _, st := range steps {
			if e, ok := st.event.(Submit); ok && strings.TrimSpace(e.Text) != "" && st.before.Phase != Exited {
				submitted = append(submitted, e.Text)
			}
			for _, f := range st.effects {
				switch f := f.(type) {
				case SendPrompt:
					out = append(out, strings.Split(f.Text, "\n\n")...)
				case RestoreInput:
					out = append(out, strings.Split(f.Text, "\n\n")...)
				}
			}
		}
		out = append(out, final(steps).Queue...)
		if !reflect.DeepEqual(out, submitted) {
			t.Fatalf("submitted %q\nbut sent, restored or queued %q", submitted, out)
		}
	})
}

// P4: everything before the print cursor is final and never changes.
func TestPrintedNeverChanges(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		for i, st := range genSession(t) {
			p := st.after.Printed
			end := p.Entry
			if p.Line > 0 {
				end++
			}
			for j, e := range st.after.Entries[:end] {
				if !e.Final {
					t.Fatalf("step %d: printed entry %d is open: %+v", i, j, e)
				}
			}
			b := st.before.Printed.Entry
			if st.before.Printed.Line > 0 {
				b++
			}
			if !sameEntries(st.after.Entries[:b], st.before.Entries[:b]) {
				t.Fatalf("step %d: a printed entry changed", i)
			}
		}
	})
}

func sameEntries(a, b []Entry) bool {
	return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b))
}

// P5: however agent text is split into chunks, the entries hold exactly
// the text so far, and the final entries are the same.
func TestChunkingInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		text := genMarkdown(t)
		chunks := splitRunes(t, text)
		got := streamText(t, chunks)
		want := streamText(t, []string{text})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("chunks %q gave\n%+v\nbut whole text gave\n%+v", chunks, got, want)
		}
	})
}

// streamText sends chunks in one turn, checking the entries hold exactly
// the text so far after every chunk, and returns the final agent entries.
func streamText(t *rapid.T, chunks []string) []Entry {
	s := busy()
	sofar := ""
	for _, c := range chunks {
		s, _ = Reduce(s, TextChunk{At: t0, MessageID: "a1", Text: c})
		sofar += c
		if got := agentText(s); got != sofar {
			t.Fatalf("entries hold %q, sent %q", got, sofar)
		}
	}
	s, _ = Reduce(s, PromptDone{At: t0, Stop: EndTurn})
	var agent []Entry
	for _, e := range s.Entries {
		if e.Kind == Agent {
			agent = append(agent, e)
		}
	}
	if agentText(s) != sofar {
		t.Fatalf("final entries hold %q, sent %q", agentText(s), sofar)
	}
	return agent
}

func agentText(s State) string {
	var b strings.Builder
	for _, e := range s.Entries {
		if e.Kind == Agent {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

// splitRunes cuts s into chunks at random rune boundaries.
func splitRunes(t *rapid.T, s string) []string {
	var chunks []string
	cur := ""
	for _, r := range s {
		cur += string(r)
		if rapid.IntRange(0, 3).Draw(t, "cut") == 0 {
			chunks = append(chunks, cur)
			cur = ""
		}
	}
	if cur != "" {
		chunks = append(chunks, cur)
	}
	return chunks
}

// busy returns a state with one prompt in flight and the terminal ready.
func busy() State {
	s, _ := Reduce(State{}, Connected{At: t0})
	s, _ = Reduce(s, TerminalReady{At: t0})
	s, _ = Reduce(s, Submit{At: t0, Text: "hi"})
	return s
}

// P14: the first Ctrl-C while busy cancels once, the second quits, and
// nothing is cancelled while not busy.
func TestCancelOnce(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		for i, st := range genSession(t) {
			var cancels, quits int
			for _, f := range st.effects {
				switch f.(type) {
				case SendCancel:
					cancels++
				case Quit:
					quits++
				}
			}
			if cancels > 0 && st.before.Phase != Busy {
				t.Fatalf("step %d: SendCancel in phase %v", i, st.before.Phase)
			}
			if cancels > 1 || quits > 1 {
				t.Fatalf("step %d: %d cancels, %d quits", i, cancels, quits)
			}
			if _, ok := st.event.(Interrupt); !ok || st.before.Phase != Busy {
				continue
			}
			if !st.before.Turn.Cancelling && (cancels != 1 || quits != 0 || !st.after.Turn.Cancelling) {
				t.Fatalf("step %d: first interrupt gave %d cancels, %d quits", i, cancels, quits)
			}
			if st.before.Turn.Cancelling && (cancels != 0 || quits != 1) {
				t.Fatalf("step %d: second interrupt gave %d cancels, %d quits", i, cancels, quits)
			}
		}
	})
}

// run feeds events to Reduce from the zero state and returns the final
// state and every effect.
func run(events ...Event) (State, []Effect) {
	var s State
	var all []Effect
	for _, e := range events {
		var fx []Effect
		s, fx = Reduce(s, e)
		all = append(all, fx...)
	}
	return s, all
}

func TestQueuedMessagesAreJoinedIntoOnePrompt(t *testing.T) {
	_, fx := run(
		Connected{At: t0}, Submit{At: t0, Text: "a"},
		Submit{At: t0, Text: "b"}, Submit{At: t0, Text: "c"},
		PromptDone{At: t0, Stop: EndTurn},
	)
	want := []Effect{SendPrompt{Text: "a"}, SendPrompt{Text: "b\n\nc"}}
	if !reflect.DeepEqual(fx, want) {
		t.Fatalf("effects %#v, want %#v", fx, want)
	}
}

func TestSubmitWhileStartingIsSentOnConnect(t *testing.T) {
	s, fx := run(Submit{At: t0, Text: "early"}, Connected{At: t0.Add(time.Second)})
	if !reflect.DeepEqual(fx, []Effect{SendPrompt{Text: "early"}}) || s.Phase != Busy {
		t.Fatalf("effects %#v, phase %v", fx, s.Phase)
	}
	if !s.Turn.Started.Equal(t0.Add(time.Second)) {
		t.Fatalf("turn started at %v", s.Turn.Started)
	}
}

func TestCancelRestoresQueueAndSendsNothing(t *testing.T) {
	s, fx := run(
		Connected{At: t0}, Submit{At: t0, Text: "a"}, Submit{At: t0, Text: "b"},
		Interrupt{At: t0, InputEmpty: true}, Submit{At: t0, Text: "c"},
		PromptDone{At: t0, Stop: Cancelled},
	)
	want := []Effect{SendPrompt{Text: "a"}, SendCancel{}, RestoreInput{Text: "b\n\nc"}}
	if !reflect.DeepEqual(fx, want) {
		t.Fatalf("effects %#v, want %#v", fx, want)
	}
	if last := s.Entries[len(s.Entries)-1]; last.Text != "[cancelled]" {
		t.Fatalf("last entry %+v", last)
	}
}

func TestFailureRestoresQueue(t *testing.T) {
	_, fx := run(Connected{At: t0}, Submit{At: t0, Text: "a"}, Submit{At: t0, Text: "b"}, PromptFailed{At: t0, Message: "x"})
	if want := (RestoreInput{Text: "b"}); fx[len(fx)-1] != want {
		t.Fatalf("effects %#v", fx)
	}
}

// Regression: the next turn started at the zero time when queued text was
// sent from the end of a turn, so Elapsed showed decades.
func TestQueuedTurnStartsWhenThePreviousEnds(t *testing.T) {
	end := t0.Add(time.Minute)
	s, _ := run(Connected{At: t0}, Submit{At: t0, Text: "a"}, Submit{At: t0, Text: "b"}, PromptDone{At: end, Stop: EndTurn})
	if l := Status(s, end.Add(time.Second)); l.Elapsed != time.Second {
		t.Fatalf("elapsed %v, want 1s", l.Elapsed)
	}
}

func TestStillRunningToolIsStoppedAtTurnEnd(t *testing.T) {
	s, _ := run(Connected{At: t0}, Submit{At: t0, Text: "a"},
		ToolStarted{At: t0, ID: "c1", Title: "echo", Status: Running},
		PromptDone{At: t0, Stop: Cancelled})
	if run := s.Entries[2]; run.Calls[0].Status != Stopped || !run.Final {
		t.Fatalf("run %+v", run)
	}
}

// Calls one after another make one run; anything between them starts a
// new one. A run is final once something follows it and its calls are
// done.
func TestToolCallsGroupIntoRuns(t *testing.T) {
	ok := OK
	s, _ := run(Connected{At: t0}, Submit{At: t0, Text: "a"},
		ToolStarted{At: t0, ID: "c1", Name: "read", Status: Running},
		ToolStarted{At: t0, ID: "c2", Name: "read", Status: Running},
		ToolUpdated{At: t0, ID: "c1", Status: &ok},
		TextChunk{At: t0, MessageID: "m", Text: "Now"},
		ToolStarted{At: t0, ID: "c3", Name: "write", Status: Running})
	if n := len(s.Entries); n != 5 {
		t.Fatalf("%d entries: %+v", n, s.Entries)
	}
	first, second := s.Entries[2], s.Entries[4]
	if len(first.Calls) != 2 || len(second.Calls) != 1 || first.Final || second.Final {
		t.Fatalf("runs %+v and %+v", first, second)
	}
	s, _ = Reduce(s, ToolUpdated{At: t0, ID: "c2", Status: &ok})
	if !s.Entries[2].Final || s.Entries[4].Final {
		t.Fatalf("after c2: %+v", s.Entries)
	}
}

// ctrl+o shows the latest run in full once, and only when idle.
func TestShowTools(t *testing.T) {
	ok := OK
	s, _ := run(Connected{At: t0}, Submit{At: t0, Text: "a"},
		ToolStarted{At: t0, ID: "c1", Name: "read", Input: `{"path":"a"}`, Status: Running},
		ShowTools{At: t0})
	if n := len(s.Entries); n != 3 {
		t.Fatalf("shown while busy: %+v", s.Entries)
	}
	s, _ = Reduce(s, ToolUpdated{At: t0, ID: "c1", Status: &ok})
	s, _ = Reduce(s, PromptDone{At: t0, Stop: EndTurn})
	if !Status(s, t0).ToolsHidden {
		t.Fatal("no hint that tools can be shown")
	}
	s, _ = Reduce(s, ShowTools{At: t0})
	s, _ = Reduce(s, ShowTools{At: t0})
	if n := len(s.Entries); n != 4 || s.Entries[3].Kind != Details || s.Entries[3].Calls[0].Input != `{"path":"a"}` {
		t.Fatalf("entries %+v", s.Entries)
	}
	if Status(s, t0).ToolsHidden {
		t.Fatal("hint shown after the tools were shown")
	}
}

func TestUpdateForUnknownToolAddsIt(t *testing.T) {
	ok := OK
	s, _ := run(Connected{At: t0}, Submit{At: t0, Text: "a"}, ToolUpdated{At: t0, ID: "c9", Status: &ok})
	if c := s.Entries[2].Calls[0]; c.Title != "c9" || c.Status != OK {
		t.Fatalf("call %+v", c)
	}
}

func TestNothingPrintsBeforeTheTerminalIsReady(t *testing.T) {
	s, _ := run(Connected{At: t0}, Submit{At: t0, Text: "a"}, Printed{At: t0, Upto: Cursor{Entry: 2}})
	if s.Printed != (Cursor{}) {
		t.Fatalf("printed %+v before the terminal was ready", s.Printed)
	}
	s, _ = Reduce(s, TerminalReady{At: t0})
	s, _ = Reduce(s, Printed{At: t0, Upto: Cursor{Entry: 2}})
	if s.Printed != (Cursor{Entry: 2}) {
		t.Fatalf("printed %+v", s.Printed)
	}
}

// A final block of a reply can go to scrollback a line at a time; the
// open block after it cannot.
func TestOnlyFinalLinesPrint(t *testing.T) {
	s := busy()
	s, _ = Reduce(s, TextChunk{At: t0, MessageID: "a1", Text: "one\n\ntw"})
	s, _ = Reduce(s, Printed{At: t0, Upto: Cursor{Entry: 2, Line: 1}})
	if want := (Cursor{Entry: 2, Line: 1}); s.Printed != want {
		t.Fatalf("printed %+v, want %+v", s.Printed, want)
	}
	s, _ = Reduce(s, Printed{At: t0, Upto: Cursor{Entry: 3, Line: 1}})
	if want := (Cursor{Entry: 2, Line: 1}); s.Printed != want {
		t.Fatalf("printed into the open block: %+v", s.Printed)
	}
	s, _ = Reduce(s, Printed{At: t0, Upto: Cursor{Entry: 1}})
	if want := (Cursor{Entry: 2, Line: 1}); s.Printed != want {
		t.Fatalf("print cursor went back: %+v", s.Printed)
	}
}

func TestBannerTitle(t *testing.T) {
	cases := []struct {
		shell string
		c     Connected
		title string
		body  string
	}{
		{"relay · hub", Connected{Agent: "agentloop", Title: "relay · ops", Version: "0.3"}, "relay · hub", "agentloop 0.3"},
		{"", Connected{Agent: "agentloop", Title: "relay · ops", Version: "0.3"}, "relay · ops", "agentloop 0.3"},
		{"", Connected{Agent: "agentloop", Model: "claude-opus-5-5"}, "agentloop", "agentloop · claude-opus-5-5"},
		{" ", Connected{}, "agent", ""},
	}
	for _, c := range cases {
		s, _ := Reduce(State{Title: c.shell}, c.c)
		if b := s.Entries[0]; b.Kind != Banner || b.Title != c.title || b.Text != c.body || !b.Final {
			t.Errorf("title %q, %+v: banner %+v", c.shell, c.c, b)
		}
	}
}

// The banner comes before anything queued while starting, and only once.
func TestBannerOnceBeforeQueuedText(t *testing.T) {
	s, _ := run(Submit{At: t0, Text: "early"}, Connected{At: t0, Agent: "a"}, Connected{At: t0, Agent: "b"})
	if len(s.Entries) != 2 || s.Entries[0].Kind != Banner || s.Entries[1].Text != "early" {
		t.Fatalf("entries %+v", s.Entries)
	}
}

// Spend: the last cost the agent reported, kept when a later update has
// none, and nothing until the first.
func TestSpendIsLastReported(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genSession(t)
		var last *Cost
		for _, st := range steps {
			if u, ok := st.event.(Usage); ok && st.before.Phase != Exited && u.Cost != nil {
				last = u.Cost
			}
		}
		got := Status(final(steps), t0).Spend
		if (got == nil) != (last == nil) || (got != nil && *got != *last) {
			t.Fatalf("spend %v, last reported %v", got, last)
		}
	})
}

// Context use: hidden until the agent reports a window size, then the
// share of it in use, floored and capped at 100, with no overflow.
func TestContextShare(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genSession(t)
		var last *Usage
		for _, st := range steps {
			if u, ok := st.event.(Usage); ok && st.before.Phase != Exited {
				last = &u
			}
		}
		l := Status(final(steps), t0)
		if last == nil || last.Size == 0 {
			if l.HasContext {
				t.Fatalf("context shown with no window size: %+v", l)
			}
			return
		}
		if !l.HasContext || l.Context < 0 || l.Context > 100 {
			t.Fatalf("usage %+v gave %+v", *last, l)
		}
		used := min(last.Used, last.Size)
		if used <= 1<<50 && l.Context != int(used*100/last.Size) {
			t.Fatalf("usage %+v gave %d%%", *last, l.Context)
		}
	})
}

// Reduce leaves the state it was given alone: replaying every step at the
// end of a session, from the state recorded before it, gives the state
// recorded after it. Shared slices that a later step wrote to would make
// the two differ.
func TestReduceKeepsOldStates(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		for i, st := range genSession(t) {
			if again, _ := Reduce(st.before, st.event); !reflect.DeepEqual(again, st.after) {
				t.Fatalf("step %d (%T): an old state changed", i, st.event)
			}
		}
	})
}
