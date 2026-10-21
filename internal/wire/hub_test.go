package wire

import (
	"encoding/json"
	"testing"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/kirahowe/acp-tui/internal/core"
)

func TestUserMessageChunkIsAnEvent(t *testing.T) {
	id := acp.MessageId("m1")
	n := acp.SessionNotification{SessionId: "s", Update: acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{
		Content: acp.TextBlock("what's new"), MessageId: &id}}}
	got, ok := Update(n, t0).(core.UserChunk)
	if !ok || got.Text != "what's new" || got.MessageID != "m1" {
		t.Fatalf("%#v", Update(n, t0))
	}
}

func TestIsHub(t *testing.T) {
	for meta, want := range map[string]bool{`1`: true, `2`: true, `0`: false, `"1"`: false, `true`: false, `null`: false, `1.5`: false, ``: false} {
		init := acp.InitializeResponse{}
		if meta != "" {
			init.Meta = map[string]json.RawMessage{"relay/hub": json.RawMessage(meta)}
		}
		if got := IsHub(init); got != want {
			t.Errorf("relay/hub %s: %v, want %v", meta, got, want)
		}
	}
	if IsHub(acp.InitializeResponse{}) {
		t.Error("a plain agent is not a hub")
	}
}

func TestRoster(t *testing.T) {
	raw := json.RawMessage(`{"roles":[
	 {"id":"dev","title":"Dev","labels":["private","weird"],"working":true,"holds":2,
	  "conversation":{"sessionId":"s1","openedAt":"2026-10-21T09:00:00Z","lastActivity":"2026-10-21T09:05:00.5Z",
	   "turn":"queued","turns":3,"unread":true,"approvals":1}},
	 {"id":"mail","title":"","labels":[],"working":false,"holds":0,"conversation":null},
	 {"id":"dev","title":"again"}]}`)
	u := Roster(raw, nil, t0)
	if u.Err != "" || len(u.Roles) != 2 {
		t.Fatalf("%+v", u)
	}
	d := u.Roles[0]
	if d.ID != "dev" || !d.Labels.Private || d.Labels.Contaminated || !d.Working || d.Holds != 2 ||
		d.Conv == nil || d.Conv.Session != "s1" || d.Conv.Turn != core.TurnQueued || d.Conv.Turns != 3 ||
		!d.Conv.Unread || d.Conv.Approvals != 1 || d.Conv.LastActivity.Sub(d.Conv.OpenedAt).Seconds() != 300.5 {
		t.Fatalf("%+v %+v", d, d.Conv)
	}
	if m := u.Roles[1]; m.Title != "mail" || m.Conv != nil {
		t.Fatalf("%+v", m)
	}
	// The same shape arrives as a notification, and a bad one is an error.
	if c, ok := RosterChanged(raw, t0); !ok || len(c.Roles) != 2 {
		t.Fatal("changed")
	}
	if _, ok := RosterChanged(json.RawMessage(`{"roles":3}`), t0); ok {
		t.Fatal("bad roster accepted")
	}
	if u := Roster(nil, &acp.RequestError{Code: -32601, Message: "no"}, t0); u.Err == "" {
		t.Fatal("error lost")
	}
}

func TestSettled(t *testing.T) {
	e, ok := Settled(json.RawMessage(`{"sessionId":"s1","toolCallId":"c1","outcome":{"outcome":"selected"}}`), t0)
	if !ok || e.Session != "s1" || e.CallID != "c1" {
		t.Fatalf("%+v %v", e, ok)
	}
	if _, ok := Settled(json.RawMessage(`{"sessionId":"s1"}`), t0); ok {
		t.Fatal("without a call")
	}
}

func TestRoleMeta(t *testing.T) {
	b, _ := json.Marshal(RoleMeta(`we"ird`))
	if string(b) != `{"relay/role":"we\"ird"}` {
		t.Fatal(string(b))
	}
}
