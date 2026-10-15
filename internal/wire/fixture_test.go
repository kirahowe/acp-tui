package wire

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kirahowe/acp-tui/internal/core"
)

var t0 = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

// replay sends "hi", feeds a recorded agentloop stream through wire and
// core, acknowledging every print at once, and returns the final state.
// check, if set, sees the state after each line.
func replay(t *testing.T, name string, check func(line int, s core.State)) core.State {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "agentloop", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	events, err := Replay(b, t0)
	if err != nil {
		t.Fatal(err)
	}

	var s core.State
	feed := func(e core.Event) {
		s, _ = core.Reduce(s, e)
		// Print every final entry, as the shell does once they scroll away.
		n := s.Printed.Entry
		for n < len(s.Entries) && s.Entries[n].Final {
			n++
		}
		s, _ = core.Reduce(s, core.Printed{At: t0, Upto: core.Cursor{Entry: n}})
	}
	feed(core.Connected{At: t0, Agent: "agentloop"})
	feed(core.TerminalReady{At: t0})
	feed(core.Submit{At: t0, Text: "hi"})
	for i, e := range events {
		feed(e)
		if check != nil {
			check(i+1, s)
		}
	}
	return s
}

func TestAgentloopFixtures(t *testing.T) {
	banner := core.Entry{Kind: core.Banner, Title: "agentloop", Text: "agentloop", Final: true}
	user := core.Entry{Kind: core.User, Text: "hi", Final: true}
	agent := func(id, text string) core.Entry {
		return core.Entry{Kind: core.Agent, ID: id, Text: text, Final: true}
	}
	note := func(text string) core.Entry { return core.Entry{Kind: core.Note, Text: text, Final: true} }
	echo := func(st core.ToolStatus, output string) core.Entry {
		return core.Entry{Kind: core.Tool, Final: true, Calls: []core.Call{{
			ID: "call_1", Name: "echo", Title: `echo {"text":"hi"}`, Status: st, Input: `{"text":"hi"}`, Output: output}}}
	}
	cases := map[string][]core.Entry{
		"simple_reply":       {banner, user, agent("a3", "Hello there.")},
		"tool_loop":          {banner, user, agent("a3", "Let me echo that."), echo(core.OK, "echo: hi"), agent("a7", "It said hi.")},
		"retry_then_success": {banner, user, agent("a4", "Sorry for the wait.")},
		"model_error": {banner, user,
			{Kind: core.Note, Text: "! The model call failed: unauthorized", Detail: "invalid x-api-key", Final: true},
			note("error: unauthorized (401): invalid x-api-key")},
		"cancelled_call": {banner, user, agent("a3", "Running it."),
			echo(core.Failed, "The run stopped while this call was running, so it may or may not have taken effect. It was not run again."),
			note("[cancelled]")},
		"denied_call": {banner, user, echo(core.Failed, "Echo is switched off."), agent("a6", "I was not allowed.")},
		"held_call": {banner, user, agent("a3", "I need to check."), echo(core.Stopped, ""),
			note("error: tool calls are waiting for a decision this agent cannot ask the client for")},
		"refusal": {banner, user, agent("a3", "I can't help with that.\n\n"),
			agent("a3", "The request was declined by the safety system."), note("[refusal]")},
		"turn_limit": {banner, user, echo(core.OK, "echo: hi"), note("[max turn requests]")},
	}
	files, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "agentloop", "*.jsonl"))
	if len(files) != len(cases) {
		t.Fatalf("%d fixtures, %d cases", len(files), len(cases))
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			s := replay(t, name, nil)
			if !reflect.DeepEqual(s.Entries, want) {
				t.Fatalf("entries\n%#v\nwant\n%#v", s.Entries, want)
			}
			if s.Phase != core.Idle || s.Printed != (core.Cursor{Entry: len(s.Entries)}) {
				t.Fatalf("phase %v, printed to %+v of %d entries", s.Phase, s.Printed, len(s.Entries))
			}
		})
	}
}

// The retry notice shows as a countdown and is not printed.
func TestAgentloopRetryCountdown(t *testing.T) {
	replay(t, "retry_then_success", func(line int, s core.State) {
		if line != 1 {
			return
		}
		l := core.Status(s, t0.Add(6*time.Second))
		if l.Kind != core.StatusRetrying || l.RetryIn != 9*time.Second || l.Reason != "rate limit exceeded" {
			t.Fatalf("status %+v", l)
		}
	})
}
