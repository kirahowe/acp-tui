package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"
	"pgregory.net/rapid"

	"github.com/kirahowe/acp-tui/internal/core"
)

// genValue is a small arbitrary JSON value, biased towards the shapes ACP
// uses, so that the SDK accepts a fair share of generated updates.
func genValue(t *rapid.T, depth int) any {
	kinds := []string{"string", "number", "bool", "null"}
	if depth < 3 {
		kinds = append(kinds, "object", "array", "block", "content")
	}
	switch rapid.SampledFrom(kinds).Draw(t, "kind") {
	case "string":
		return rapid.SampledFrom([]string{"", "x", "pending", "in_progress", "completed", "failed",
			"warning", "2026-10-15T09:00:15Z", "not a time", "a\n\nb", "\x00\x1b["}).Draw(t, "str")
	case "number":
		return rapid.Float64Range(-1e9, 1e9).Draw(t, "num")
	case "bool":
		return rapid.Bool().Draw(t, "bool")
	case "null":
		return nil
	case "array":
		return rapid.SliceOfN(rapid.Custom(func(t *rapid.T) any { return genValue(t, depth+1) }), 0, 3).Draw(t, "arr")
	case "block":
		b := map[string]any{"type": rapid.SampledFrom([]string{"text", "image", "audio", "resource_link", "resource", "other"}).Draw(t, "type")}
		for _, k := range rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"text", "uri", "data", "mimeType", "name", "resource"}), 0, 4, rapid.ID[string]).Draw(t, "blockKeys") {
			b[k] = genValue(t, depth+1)
		}
		return b
	case "content":
		return []any{map[string]any{"type": "content", "content": genValue(t, depth+1)}}
	}
	obj := map[string]any{}
	for _, k := range rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"a", "retry_at", "message", "agentloop/retry", "text"}), 0, 3, rapid.ID[string]).Draw(t, "keys") {
		obj[k] = genValue(t, depth+1)
	}
	return obj
}

// genBlock is a content block, usually valid.
func genBlock(t *rapid.T) map[string]any {
	str := func(label string) string {
		return rapid.SampledFrom([]string{"", "x", "a\n\nb", "**b**", "\x1b[31m", "世界"}).Draw(t, label)
	}
	switch rapid.SampledFrom([]string{"text", "image", "audio", "resource_link", "resource", "other"}).Draw(t, "blockType") {
	case "text":
		return map[string]any{"type": "text", "text": str("text")}
	case "image", "audio":
		return map[string]any{"type": "image", "data": "", "mimeType": "image/png"}
	case "resource_link":
		return map[string]any{"type": "resource_link", "name": str("name"), "uri": str("uri")}
	case "resource":
		return map[string]any{"type": "resource", "resource": map[string]any{"uri": str("uri"), "text": str("text")}}
	}
	return map[string]any{"type": "other"}
}

// genUpdate is a session update built from a valid template for its kind,
// then possibly damaged: fields dropped or replaced with arbitrary values.
func genUpdate(t *rapid.T, kind string) map[string]any {
	str := func(label string) string { return rapid.SampledFrom([]string{"", "x", "c1", "echo {}"}).Draw(t, label) }
	status := func() string {
		return rapid.SampledFrom([]string{"pending", "in_progress", "completed", "failed", "weird"}).Draw(t, "status")
	}
	u := map[string]any{"sessionUpdate": kind}
	switch kind {
	case "agent_message_chunk", "agent_thought_chunk", "user_message_chunk":
		u["content"] = genBlock(t)
		u["messageId"] = str("messageId")
	case "tool_call", "tool_call_update":
		u["toolCallId"] = str("toolCallId")
		u["title"] = str("title")
		u["status"] = status()
		u["content"] = []any{map[string]any{"type": "content", "content": genBlock(t)}}
		u["rawInput"] = genValue(t, 1)
	case "notice":
		u["severity"] = rapid.SampledFrom([]string{"info", "warning", "error"}).Draw(t, "severity")
		u["title"] = str("title")
		u["description"] = str("description")
		u["_meta"] = map[string]any{"agentloop/retry": map[string]any{
			"retry_at": rapid.SampledFrom([]string{"2026-10-15T09:00:15Z", "soon", ""}).Draw(t, "retryAt"),
		}}
	case "plan":
		u["entries"] = []any{}
	case "usage_update":
		u["used"], u["size"] = 1, 2
	}
	for k := range u {
		if k == "sessionUpdate" {
			continue
		}
		switch rapid.IntRange(0, 9).Draw(t, "damage "+k) {
		case 0:
			delete(u, k)
		case 1:
			u[k] = genValue(t, 0)
		}
	}
	return u
}

// P15: Update never panics for any session update the SDK accepts, and
// each kind of update becomes the event it should. (The SDK rejects an
// unknown sessionUpdate itself, so none reaches Update.)
func TestWireTotal(t *testing.T) {
	kinds := map[string]string{
		"agent_message_chunk": "core.TextChunk",
		"agent_thought_chunk": "core.ThoughtChunk",
		"user_message_chunk":  "core.Ignored",
		"tool_call":           "core.ToolStarted",
		"tool_call_update":    "core.ToolUpdated",
		"notice":              "core.Notice",
		"plan":                "core.Ignored",
		"usage_update":        "core.Usage",
	}
	for kind, want := range kinds {
		t.Run(kind, func(t *testing.T) {
			seen := map[string]int{}
			rapid.Check(t, func(t *rapid.T) {
				b, err := json.Marshal(genUpdate(t, kind))
				if err != nil {
					t.Fatalf("not JSON: %v", err)
				}
				var su acp.SessionUpdate
				if su.UnmarshalJSON(b) != nil {
					return
				}
				e := Update(acp.SessionNotification{SessionId: "s", Update: su}, time.Time{})
				if e == nil {
					t.Fatalf("nil event for %s", b)
				}
				seen[fmt.Sprintf("%T", e)]++
			})
			// The generator is only useful if the SDK accepts some of it.
			if seen[want] == 0 {
				t.Errorf("no update became a %s: %v", want, seen)
			}
		})
	}
}

// Response is total too, for any error and any stop reason.
func TestResponseTotal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var err error
		switch rapid.IntRange(0, 2).Draw(t, "errKind") {
		case 1:
			err = errors.New(rapid.String().Draw(t, "msg"))
		case 2:
			err = fmt.Errorf("wrapped: %w", &acp.RequestError{Code: -32603, Message: rapid.String().Draw(t, "msg"), Data: genValue(t, 0)})
		}
		e := Response(acp.PromptResponse{StopReason: acp.StopReason(rapid.String().Draw(t, "stop"))}, err, time.Time{})
		switch e.(type) {
		case core.PromptDone:
			if err != nil {
				t.Fatalf("PromptDone for error %v", err)
			}
		case core.PromptFailed:
			if err == nil {
				t.Fatal("PromptFailed with no error")
			}
		default:
			t.Fatalf("event %#v", e)
		}
	})
}

func TestResponseAddsDataMessage(t *testing.T) {
	err := &acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"message": "disk full"}}
	if e := Response(acp.PromptResponse{}, err, time.Time{}).(core.PromptFailed); e.Message != "Internal error: disk full" {
		t.Fatalf("message %q", e.Message)
	}
}

func decode(t *testing.T, s string) acp.SessionNotification {
	t.Helper()
	var u acp.SessionUpdate
	if err := u.UnmarshalJSON([]byte(s)); err != nil {
		t.Fatal(err)
	}
	return acp.SessionNotification{SessionId: "s", Update: u}
}

func TestUpdate(t *testing.T) {
	ok := func(got, want core.Event) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got  %#v\nwant %#v", got, want)
		}
	}
	ok(Update(decode(t, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}`), t0),
		core.TextChunk{At: t0, Text: "hi"})
	ok(Update(decode(t, `{"sessionUpdate":"agent_message_chunk","content":{"type":"image","data":"","mimeType":"image/png"}}`), t0),
		core.TextChunk{At: t0, Text: "[image]"})
	ok(Update(decode(t, `{"sessionUpdate":"agent_message_chunk","content":{"type":"resource_link","name":"n","uri":"file:///a"}}`), t0),
		core.TextChunk{At: t0, Text: "[resource: file:///a]"})
	ok(Update(decode(t, `{"sessionUpdate":"tool_call","toolCallId":"c","title":"t","status":"in_progress"}`), t0),
		core.ToolStarted{At: t0, ID: "c", Title: "t", Status: core.Running})
	ok(Update(decode(t, `{"sessionUpdate":"tool_call","toolCallId":"c","title":"t","name":"read","rawInput":{"path":"a"},"status":"in_progress"}`), t0),
		core.ToolStarted{At: t0, ID: "c", Name: "read", Title: "t", Input: `{"path":"a"}`, Status: core.Running})
	ok(Update(decode(t, `{"sessionUpdate":"usage_update","used":1,"size":2}`), t0), core.Usage{At: t0, Used: 1, Size: 2})
	ok(Update(decode(t, `{"sessionUpdate":"usage_update","used":1,"size":0,"cost":{"amount":0.42,"currency":"USD"}}`), t0),
		core.Usage{At: t0, Used: 1, Cost: &core.Cost{Amount: 0.42, Currency: "USD"}})
	ok(Update(decode(t, `{"sessionUpdate":"plan","entries":[]}`), t0), core.Ignored{At: t0})

	upd := Update(decode(t, `{"sessionUpdate":"tool_call_update","toolCallId":"c","status":"failed",
		"content":[{"type":"content","content":{"type":"text","text":"\n  no such file  \nmore"}}]}`), t0).(core.ToolUpdated)
	if upd.Output == nil || *upd.Output != "\n  no such file  \nmore" || *upd.Status != core.Failed {
		t.Errorf("update %#v", upd)
	}
	if n := Update(decode(t, `{"sessionUpdate":"notice","severity":"warning","title":"t",
		"_meta":{"agentloop/retry":{"retry_at":"soon"}}}`), t0).(core.Notice); n.RetryAt != nil {
		t.Errorf("a malformed retry_at gave %v", n.RetryAt)
	}
}

func TestPermission(t *testing.T) {
	var req acp.RequestPermissionRequest
	if err := json.Unmarshal([]byte(`{"sessionId":"s","toolCall":{"toolCallId":"call_1","name":"web_fetch",
		"title":"web_fetch {\"url\":\"https://x.test\"}","rawInput":{"url":"https://x.test"}},
		"options":[{"optionId":"allow","name":"Allow once","kind":"allow_once"},
		{"optionId":"reject","name":"Reject","kind":"reject_once"},{"optionId":"x","name":"Odd","kind":"future_kind"}]}`), &req); err != nil {
		t.Fatal(err)
	}
	got := Permission(req, 7, t0)
	want := core.PermissionAsked{At: t0, Permission: core.Permission{
		ID: 7, Name: "web_fetch", Title: `web_fetch {"url":"https://x.test"}`, Input: `{"url":"https://x.test"}`,
		Options: []core.PermOption{{ID: "allow", Name: "Allow once", Kind: core.AllowOnce}, {ID: "reject", Name: "Reject", Kind: core.RejectOnce}, {ID: "x", Name: "Odd", Kind: "future_kind"}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
	if k := got.Options[2].Kind; k.Allows() || k.Rejects() {
		t.Errorf("an unknown kind %q counts as allow or reject", k)
	}
	// Bare request: no name, no title, no input.
	bare := Permission(acp.RequestPermissionRequest{ToolCall: acp.ToolCallUpdate{ToolCallId: "c"}}, 1, t0)
	if bare.Name != "" || bare.Title != "" || bare.Input != "" || len(bare.Options) != 0 {
		t.Errorf("bare request %#v", bare)
	}
}

func TestOutcome(t *testing.T) {
	cases := map[string]core.AnswerPermission{
		`{"optionId":"allow","outcome":"selected"}`: {OptionID: "allow"},
		`{"outcome":"cancelled"}`:                   {Cancelled: true, OptionID: "ignored"},
	}
	for want, a := range cases {
		b, err := json.Marshal(Outcome(a).Outcome)
		if err != nil || string(b) != want {
			t.Errorf("outcome %s (%v), want %s", b, err, want)
		}
	}
}

func TestLabels(t *testing.T) {
	for name, c := range map[string]struct {
		update string
		want   core.Labels
		ok     bool
	}{
		"tool_call":   {`{"sessionUpdate":"tool_call","toolCallId":"c","title":"t","_meta":{"agentloop/labels":["private"]}}`, core.Labels{Private: true}, true},
		"update":      {`{"sessionUpdate":"tool_call_update","toolCallId":"c","_meta":{"agentloop/labels":["contaminated","private"]}}`, core.Labels{Private: true, Contaminated: true}, true},
		"usage":       {`{"sessionUpdate":"usage_update","used":1,"size":2,"_meta":{"agentloop/labels":[]}}`, core.Labels{}, true},
		"unknown":     {`{"sessionUpdate":"usage_update","used":1,"size":2,"_meta":{"agentloop/labels":["secret"]}}`, core.Labels{}, true},
		"none":        {`{"sessionUpdate":"usage_update","used":1,"size":2}`, core.Labels{}, false},
		"other_meta":  {`{"sessionUpdate":"usage_update","used":1,"size":2,"_meta":{"x":1}}`, core.Labels{}, false},
		"malformed":   {`{"sessionUpdate":"usage_update","used":1,"size":2,"_meta":{"agentloop/labels":"private"}}`, core.Labels{}, false},
		"not_carried": {`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"x"},"_meta":{"agentloop/labels":["private"]}}`, core.Labels{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := Labels(decode(t, c.update), t0)
			if ok != c.ok || got.Labels != c.want {
				t.Fatalf("got %+v %v, want %+v %v", got.Labels, ok, c.want, c.ok)
			}
		})
	}
}

func TestConnected(t *testing.T) {
	model := acp.SessionConfigOptionCategoryModel
	title := "relay · hub"
	opts := acp.SessionConfigSelectOptionsUngrouped{{Name: "Opus 5.5", Value: "claude-opus-5-5"}}
	init := acp.InitializeResponse{AgentInfo: &acp.Implementation{Name: "agentloop", Title: &title, Version: "0.3\n"}}
	sess := acp.NewSessionResponse{ConfigOptions: []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{
		Category: &model, CurrentValue: "claude-opus-5-5", Options: acp.SessionConfigSelectOptions{Ungrouped: &opts},
	}}}}
	want := core.Connected{At: t0, Agent: "agentloop", Title: "relay · hub", Version: "0.3", Model: "Opus 5.5"}
	if got := Connected(init, sess, t0); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if got := Connected(acp.InitializeResponse{}, acp.NewSessionResponse{}, t0); got != (core.Connected{At: t0}) {
		t.Fatalf("no agentInfo: %+v", got)
	}
}
