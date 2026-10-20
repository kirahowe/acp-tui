package wire

import (
	"bytes"
	"encoding/json"
	"testing"

	"pgregory.net/rapid"

	"github.com/kirahowe/acp-tui/internal/jsoncases"
)

// sdkMethod is the method the SDK dispatches a line on: its own envelope,
// decoded as it decodes it, with encoding/json v1.
func sdkMethod(t *testing.T, line []byte) *string {
	t.Helper()
	var e envelope[json.RawMessage, json.RawMessage]
	if err := json.Unmarshal(line, &e); err != nil {
		t.Fatalf("the SDK cannot read %s: %v", line, err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(line, &fields)
	if e.Method == "" && fields["method"] == nil {
		return nil
	}
	return &e.Method
}

// The shared table (relay#58), through the screen and then as the SDK
// reads what the screen passes on: rejected lines go nowhere, and the SDK
// sees exactly the method the dialect sees.
func TestScreenSharedTable(t *testing.T) {
	for _, c := range jsoncases.Read(t) {
		t.Run(c.Name, func(t *testing.T) {
			out, err := Screen(c.Line)
			if c.Reject {
				if err == nil {
					t.Fatalf("passed on %q", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("dropped: %v", err)
			}
			got := sdkMethod(t, out)
			if (got == nil) != (c.Method == nil) || got != nil && *got != *c.Method {
				t.Fatalf("the SDK sees method %v, want %v (line %s)", deref(got), deref(c.Method), out)
			}
		})
	}
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func TestScreenRemovesWhatV1WouldFold(t *testing.T) {
	cases := []struct{ in, out string }{
		// Inside a session update: a _meta the SDK would read under
		// another name, so relay would not see it as _meta.
		{`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"c","title":"t","_META":{"agentloop/labels":["private"]}}}}`,
			`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"c","title":"t"}}}`},
		{`{"jsonrpc":"2.0","id":1,"method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"c","Title":"rm -rf"},"options":[]}}`,
			`{"jsonrpc":"2.0","id":1,"method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"c"},"options":[]}}`},
		// An answer to session/prompt.
		{`{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn","StopReason":"refusal"}}`,
			`{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn"}}`},
		{`{"jsonrpc":"2.0","id":3,"error":{"code":1,"message":"m","Message":"n"}}`,
			`{"jsonrpc":"2.0","id":3,"error":{"code":1,"message":"m"}}`},
		{`{"jsonrpc":"2.0","method":"_relay/cards/changed","params":{"Rev":1}}`,
			`{"jsonrpc":"2.0","method":"_relay/cards/changed","params":{"Rev":1}}`}, // raw: wire reads it strictly
	}
	for _, c := range cases {
		out, err := Screen([]byte(c.in))
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if string(out) != c.out {
			t.Errorf("%s\n got %s\nwant %s", c.in, out, c.out)
		}
	}
}

// What an encoder writes passes the screen byte for byte.
func TestScreenPassesEncodedLines(t *testing.T) {
	kinds := []string{"agent_message_chunk", "agent_thought_chunk", "tool_call", "tool_call_update",
		"notice", "plan", "usage_update", "session_info_update"}
	rapid.Check(t, func(t *rapid.T) {
		var msg any
		switch rapid.IntRange(0, 2).Draw(t, "shape") {
		case 0:
			msg = map[string]any{"jsonrpc": "2.0", "method": "session/update",
				"params": map[string]any{"sessionId": "s", "update": genUpdate(t, rapid.SampledFrom(kinds).Draw(t, "kind"))}}
		case 1:
			msg = map[string]any{"jsonrpc": "2.0", "id": 4, "method": "session/request_permission",
				"params": map[string]any{"sessionId": "s", "toolCall": genUpdate(t, "tool_call_update"),
					"options": []any{map[string]any{"optionId": "a", "name": "Allow", "kind": "allow_once"}}}}
		default:
			msg = map[string]any{"jsonrpc": "2.0", "id": 2, "result": genValue(t, 0)}
		}
		line, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Screen(line)
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if !bytes.Equal(out, line) {
			t.Fatalf("changed\n%s\n%s", line, out)
		}
	})
}
