package fakehub

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"
)

type testClient struct {
	mu       sync.Mutex
	replayed chan json.RawMessage
}

func (c *testClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}
func (c *testClient) SessionUpdate(context.Context, acp.SessionNotification) error { return nil }
func (c *testClient) HandleExtensionMethod(_ context.Context, method string, params json.RawMessage) (any, error) {
	if method == "_relay/replayed" {
		c.replayed <- params
	}
	return nil, nil
}

// connect plays a client against the fake hub over pipes.
func connect(t *testing.T, cfg Config) (*acp.ClientSideConnection, *testClient) {
	t.Helper()
	h := New(cfg, io.Discard)
	cr, aw := io.Pipe()
	ar, cw := io.Pipe()
	agentConn := acp.NewAgentSideConnection(h, aw, ar)
	go func() { _ = Serve(context.Background(), h, agentConn, "") }()
	tc := &testClient{replayed: make(chan json.RawMessage, 4)}
	conn := acp.NewClientSideConnection(tc, cw, cr)
	t.Cleanup(func() { _ = conn.Close(); _ = agentConn.Close() })
	return conn, tc
}

func roleMeta(id string) map[string]json.RawMessage {
	b, _ := json.Marshal(id)
	return map[string]json.RawMessage{"relay/role": b}
}

func TestInitializeSaysWhetherRelayKeepsConversations(t *testing.T) {
	for _, embedded := range []bool{false, true} {
		conn, _ := connect(t, Config{Embedded: embedded})
		init, err := conn.Initialize(context.Background(), acp.InitializeRequest{ProtocolVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		want := "true"
		if embedded {
			want = "false"
		}
		if string(init.Meta["relay/keeps"]) != want || string(init.Meta["relay/hub"]) != "1" {
			t.Errorf("embedded=%v: meta %v", embedded, init.Meta)
		}
	}
}

// Every replay ends with _relay/replayed, a fresh conversation's too, and
// it says whether history was cut.
func TestEveryNewSessionEndsWithTheReplayedMarker(t *testing.T) {
	cfg := Config{Roles: []RoleConfig{
		{ID: "fresh", Title: "Fresh"},
		{ID: "old", Title: "Old", Live: &Live{Truncated: true, History: []Said{{User: "q"}, {Agent: "a"}}}},
	}}
	conn, tc := connect(t, cfg)
	ctx := context.Background()
	for id, want := range map[string]bool{"fresh": false, "old": true} {
		resp, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: "/", McpServers: []acp.McpServer{}, Meta: roleMeta(id)})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case p := <-tc.replayed:
			var got struct {
				SessionID string `json:"sessionId"`
				Truncated bool   `json:"truncated"`
			}
			if err := json.Unmarshal(p, &got); err != nil || got.SessionID != string(resp.SessionId) || got.Truncated != want {
				t.Errorf("%s: %s", id, p)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: no _relay/replayed", id)
		}
	}
}

// A second prompt while a turn runs is refused, as relay's hub does.
func TestSecondPromptWhileATurnRunsIsRefused(t *testing.T) {
	conn, _ := connect(t, Config{Roles: []RoleConfig{{ID: "dev", Title: "Dev"}}})
	ctx := context.Background()
	resp, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: "/", McpServers: []acp.McpServer{}, Meta: roleMeta("dev")})
	if err != nil {
		t.Fatal(err)
	}
	prompt := func(text string) error {
		_, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: resp.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
		return err
	}
	done := make(chan error, 1)
	go func() { done <- prompt("slow") }()
	time.Sleep(200 * time.Millisecond)
	err = prompt("again")
	var re *acp.RequestError
	if !asRequestError(err, &re) || re.Code != -32000 {
		t.Fatalf("second prompt: %v", err)
	}
	_ = conn.Cancel(ctx, acp.CancelNotification{SessionId: resp.SessionId})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := prompt("after"); err != nil {
		t.Fatalf("a prompt after the answer is refused: %v", err)
	}
}

func asRequestError(err error, target **acp.RequestError) bool {
	re, ok := err.(*acp.RequestError)
	if ok {
		*target = re
	}
	return ok
}

// The roster learns that a turn ended a moment after the prompt's answer,
// as relay's does, so clients meet that ordering.
func TestRosterTurnEndsAfterThePromptAnswer(t *testing.T) {
	conn, _ := connect(t, Config{Roles: []RoleConfig{{ID: "dev", Title: "Dev"}}})
	ctx := context.Background()
	resp, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: "/", McpServers: []acp.McpServer{}, Meta: roleMeta("dev")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: resp.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock("hi")}}); err != nil {
		t.Fatal(err)
	}
	turn := func() string {
		raw, err := conn.CallExtension(ctx, "_relay/roles", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			Roles []struct {
				Conversation struct {
					Turn string `json:"turn"`
				} `json:"conversation"`
			} `json:"roles"`
		}
		_ = json.Unmarshal(raw, &r)
		return r.Roles[0].Conversation.Turn
	}
	if got := turn(); got != "running" {
		t.Fatalf("right after the answer the roster says %q", got)
	}
	time.Sleep(200 * time.Millisecond)
	if got := turn(); got != "idle" {
		t.Fatalf("later it says %q", got)
	}
}
