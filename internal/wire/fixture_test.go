package wire

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/kirahowe/acp-tui/internal/core"
)

var t0 = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

// replay sends "hi", feeds a recorded agentloop stream through wire and
// core, acknowledging every print at once, and returns the final state.
// check, if set, sees the state after each line.
func replay(t *testing.T, name string, check func(line int, s core.State)) core.State {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "agentloop", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var s core.State
	feed := func(e core.Event) {
		var fx []core.Effect
		s, fx = core.Reduce(s, e)
		for _, f := range fx {
			if p, ok := f.(core.Print); ok {
				s, _ = core.Reduce(s, core.Printed{At: t0, Upto: p.To})
			}
		}
	}
	feed(core.Connected{At: t0, Agent: "agentloop"})
	feed(core.TerminalReady{At: t0})
	feed(core.Submit{At: t0, Text: "hi"})

	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(sc.Bytes(), &keys); err != nil {
			t.Fatalf("line %d: %v", line, err)
		}
		switch {
		case keys["sessionUpdate"] != nil:
			var u acp.SessionUpdate
			if err := u.UnmarshalJSON(sc.Bytes()); err != nil {
				t.Fatalf("line %d: %v", line, err)
			}
			feed(Update(acp.SessionNotification{SessionId: "s1", Update: u}, t0))
		case keys["stopReason"] != nil:
			var r acp.PromptResponse
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				t.Fatalf("line %d: %v", line, err)
			}
			feed(Response(r, nil, t0))
		case keys["error"] != nil:
			var re acp.RequestError
			if err := json.Unmarshal(keys["error"], &re); err != nil {
				t.Fatalf("line %d: %v", line, err)
			}
			feed(Response(acp.PromptResponse{}, &re, t0))
		default:
			t.Fatalf("line %d: unknown shape %s", line, sc.Bytes())
		}
		if check != nil {
			check(line, s)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAgentloopFixtures(t *testing.T) {
	user := core.Entry{Kind: core.User, Text: "hi", Final: true}
	agent := func(id, text string) core.Entry {
		return core.Entry{Kind: core.Agent, ID: id, Text: text, Final: true}
	}
	note := func(text string) core.Entry { return core.Entry{Kind: core.Note, Text: text, Final: true} }
	echo := func(st core.ToolStatus, detail string) core.Entry {
		return core.Entry{Kind: core.Tool, ID: "call_1", Title: `echo {"text":"hi"}`, Status: st, Detail: detail, Final: true}
	}
	cases := map[string][]core.Entry{
		"simple_reply":       {user, agent("a3", "Hello there.")},
		"tool_loop":          {user, agent("a3", "Let me echo that."), echo(core.OK, ""), agent("a7", "It said hi.")},
		"retry_then_success": {user, agent("a4", "Sorry for the wait.")},
		"model_error": {user,
			{Kind: core.Note, Text: "! The model call failed: unauthorized", Detail: "invalid x-api-key", Final: true},
			note("error: unauthorized (401): invalid x-api-key")},
		"cancelled_call": {user, agent("a3", "Running it."),
			echo(core.Failed, "The run stopped while this call was running, so it may or may not have taken effect. It was not run again."),
			note("[cancelled]")},
		"denied_call": {user, echo(core.Failed, "Echo is switched off."), agent("a6", "I was not allowed.")},
		"held_call": {user, agent("a3", "I need to check."), echo(core.Stopped, ""),
			note("error: tool calls are waiting for a decision this agent cannot ask the client for")},
		"refusal": {user, agent("a3", "I can't help with that.\n\n"),
			agent("a3", "The request was declined by the safety system."), note("[refusal]")},
		"turn_limit": {user, echo(core.OK, ""), note("[max turn requests]")},
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
			if s.Phase != core.Idle || s.Printed != len(s.Entries) {
				t.Fatalf("phase %v, %d of %d entries printed", s.Phase, s.Printed, len(s.Entries))
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
		if l.Kind != core.StatusRetrying || l.RetryIn != 9*time.Second || l.Reason != "Rate limited, retrying in 12 s" {
			t.Fatalf("status %+v", l)
		}
	})
}
