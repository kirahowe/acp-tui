// Package fakehub is a stand-in for relay's hub, for tests: it speaks the
// protocol of the design brief's section 4 (the roster, attach with
// replay, several sessions on one connection, permission requests that
// another device can settle, queued turns) over one connection, without
// relay. It plays one client; a second device is played from a control
// file that the test appends lines to.
//
// The config is JSON:
//
//	{"roles": [{"id": "dev", "title": "Dev", "labels": ["private"],
//	  "working": false, "holds": 0, "busy": false,
//	  "live": {"unread": true, "history": [{"user": "hi"}, {"agent": "hello"}]}}]}
//
// A role with "live" starts with a conversation holding that history. A
// role with "busy" queues the prompts it gets until it is told otherwise.
//
// A prompt is answered with "echo: " and its text, in two chunks. A
// prompt starting with "ask" first sends a permission request for a tool
// "send_mail"; its answer is logged as "permission <session> <outcome>"
// and said in the reply. A prompt "slow" runs until it is cancelled or
// the control file says "finish <role>".
//
// The control file takes one command per line:
//
//	other-prompt <role> <text>   another device prompted: the user text
//	                             and an echo arrive as live updates
//	ask <role> [name]            a permission request, from a prompt run
//	                             elsewhere
//	settle <role>                another device answered that request
//	busy <role>, unbusy <role>   queue prompts, or let them through
//	working <role> on|off        a scheduled wake runs, or ends
//	start <role>                 a conversation opens from elsewhere
//	end <role>                   the conversation ends elsewhere
//	finish <role>                a "slow" turn ends
//
// Everything the client does is logged, one line each: "new <role>
// attached=<bool>", "load <session>", "prompt <session> <json text>",
// "cancel <session>", "seen <session> <turn>", "end <session>",
// "permission <session> <outcome json>", "roles".
package fakehub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"
)

// Config is the hub's starting state.
type Config struct {
	Roles []RoleConfig `json:"roles"`
	// Embedded plays relay with no service running: initialize says
	// "relay/keeps": false.
	Embedded bool `json:"embedded"`
}

// RoleConfig is one role.
type RoleConfig struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Labels  []string `json:"labels"`
	Working bool     `json:"working"`
	Holds   int      `json:"holds"`
	Busy    bool     `json:"busy"`
	Live    *Live    `json:"live"`
}

// Live is a conversation the role already has.
type Live struct {
	// Truncated makes the replay end with "truncated": true.
	Truncated bool   `json:"truncated"`
	Unread    bool   `json:"unread"`
	History   []Said `json:"history"`
}

// Said is one entry of a conversation's history.
type Said struct {
	User  string `json:"user,omitempty"`
	Agent string `json:"agent,omitempty"`
}

type conv struct {
	session    string
	history    []Said
	turns      int
	seen       int
	turn       string
	openedAt   time.Time
	activity   time.Time
	approvals  int
	attached   bool
	truncated  bool
	busy       bool // a turn is running or queued: the next prompt is refused
	cancel     chan struct{}
	finish     chan struct{}
	pendingAsk *ask
}

type ask struct {
	callID string
	done   chan struct{}
}

type role struct {
	RoleConfig
	conv *conv
}

// Hub plays the hub.
type Hub struct {
	mu        sync.Mutex
	conn      *acp.AgentSideConnection
	log       io.Writer
	roles     []*role
	sessions  int
	calls     int
	subscribe bool
	unbusy    chan struct{} // closed and replaced when a role stops being busy
	now       func() time.Time
	keeps     bool
	// lag is how long after a prompt's answer the roster says the turn
	// ended, as relay's does.
	lag time.Duration
}

// New returns a hub with the config's roles. log receives one line per
// client action.
func New(cfg Config, log io.Writer) *Hub {
	h := &Hub{log: log, unbusy: make(chan struct{}), now: time.Now, keeps: !cfg.Embedded, lag: 30 * time.Millisecond}
	for _, rc := range cfg.Roles {
		r := &role{RoleConfig: rc}
		if rc.Live != nil {
			c := h.newConv(r)
			c.history = append(c.history, rc.Live.History...)
			c.truncated = rc.Live.Truncated
			c.turns = len(rc.Live.History) / 2
			if rc.Live.Unread {
				c.seen = c.turns - 1
			} else {
				c.seen = c.turns
			}
		}
		h.roles = append(h.roles, r)
	}
	return h
}

// Load reads a config file.
func Load(path string) (Config, error) {
	var cfg Config
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	return cfg, json.Unmarshal(b, &cfg)
}

func (h *Hub) logf(format string, args ...any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(h.log, format+"\n", args...)
}

func (h *Hub) newConv(r *role) *conv {
	h.sessions++
	c := &conv{session: fmt.Sprintf("fh-%s-%d", r.ID, h.sessions), turn: "idle", openedAt: h.now(), activity: h.now()}
	r.conv = c
	return c
}

func (h *Hub) role(id string) *role {
	for _, r := range h.roles {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func (h *Hub) byRole(session string) (*role, *conv) {
	for _, r := range h.roles {
		if r.conv != nil && r.conv.session == session {
			return r, r.conv
		}
	}
	return nil, nil
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// rosterJSON is the roster as the client sees it. The caller holds mu.
func (h *Hub) rosterJSON() map[string]any {
	var roles []map[string]any
	for _, r := range h.roles {
		labels := r.Labels
		if labels == nil {
			labels = []string{}
		}
		m := map[string]any{"id": r.ID, "title": r.Title, "labels": labels, "working": r.Working, "holds": r.Holds, "conversation": nil}
		if c := r.conv; c != nil {
			m["conversation"] = map[string]any{"sessionId": c.session, "openedAt": iso(c.openedAt), "lastActivity": iso(c.activity),
				"turn": c.turn, "turns": c.turns, "unread": c.turns > c.seen, "approvals": c.approvals}
		}
		roles = append(roles, m)
	}
	return map[string]any{"roles": roles}
}

// notifyRoster sends _relay/roles/changed to a client that asked for the
// roster. The caller does not hold mu.
func (h *Hub) notifyRoster() {
	h.mu.Lock()
	sub, conn, body := h.subscribe, h.conn, h.rosterJSON()
	h.mu.Unlock()
	if sub && conn != nil {
		_ = conn.NotifyExtension(context.Background(), "_relay/roles/changed", body)
	}
}

func (h *Hub) update(session string, u acp.SessionUpdate) {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn != nil {
		_ = conn.SessionUpdate(context.Background(), acp.SessionNotification{SessionId: acp.SessionId(session), Update: u})
	}
}

func (h *Hub) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	title := "relay"
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		AgentInfo:       &acp.Implementation{Name: "relay", Version: "fake", Title: &title},
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession:         true,
			PromptCapabilities:  acp.PromptCapabilities{EmbeddedContext: true},
			SessionCapabilities: acp.SessionCapabilities{List: &acp.SessionListCapabilities{}},
		},
		Meta: map[string]json.RawMessage{"relay/hub": json.RawMessage("1"), "relay/keeps": json.RawMessage(fmt.Sprint(h.keeps))},
	}, nil
}

// replay sends a conversation's history as updates.
func (h *Hub) replay(c *conv) {
	h.mu.Lock()
	hist := append([]Said(nil), c.history...)
	h.mu.Unlock()
	for _, s := range hist {
		if s.User != "" {
			h.update(c.session, acp.UpdateUserMessageText(s.User))
		}
		if s.Agent != "" {
			h.update(c.session, acp.UpdateAgentMessageText(s.Agent))
		}
	}
}

// replayed tells the client the replay is over.
func (h *Hub) replayed(c *conv) {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	h.mu.Lock()
	trunc := c.truncated
	h.mu.Unlock()
	if conn != nil {
		_ = conn.NotifyExtension(context.Background(), "_relay/replayed", map[string]any{"sessionId": c.session, "truncated": trunc})
	}
}

func (h *Hub) NewSession(_ context.Context, req acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	var id string
	if raw, ok := req.Meta["relay/role"]; ok {
		_ = json.Unmarshal(raw, &id)
	}
	h.mu.Lock()
	r := h.role(id)
	if r == nil {
		h.mu.Unlock()
		return acp.NewSessionResponse{}, &acp.RequestError{Code: -32602, Message: "no such role: " + id}
	}
	if c := r.conv; c != nil {
		c.attached = true
		h.mu.Unlock()
		h.logf("new %s attached=true", id)
		// The replay follows the answer.
		time.AfterFunc(30*time.Millisecond, func() { h.replay(c); h.replayed(c) })
		return acp.NewSessionResponse{SessionId: acp.SessionId(c.session),
			Meta: map[string]json.RawMessage{"relay/attached": json.RawMessage("true")}}, nil
	}
	c := h.newConv(r)
	c.attached = true
	h.mu.Unlock()
	h.logf("new %s attached=false", id)
	// A fresh conversation has an empty replay, and it ends with the marker
	// too.
	time.AfterFunc(30*time.Millisecond, func() { h.replayed(c); h.notifyRoster() })
	return acp.NewSessionResponse{SessionId: acp.SessionId(c.session)}, nil
}

func (h *Hub) LoadSession(_ context.Context, req acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	h.mu.Lock()
	_, c := h.byRole(string(req.SessionId))
	if c == nil {
		h.mu.Unlock()
		return acp.LoadSessionResponse{}, &acp.RequestError{Code: -32602, Message: "not a live conversation: " + string(req.SessionId)}
	}
	c.attached = true
	h.mu.Unlock()
	h.logf("load %s", req.SessionId)
	h.replay(c)
	h.replayed(c)
	return acp.LoadSessionResponse{}, nil
}

func (h *Hub) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := acp.ListSessionsResponse{Sessions: []acp.SessionInfo{}}
	for _, r := range h.roles {
		if r.conv != nil {
			t, u := r.Title, iso(r.conv.activity)
			out.Sessions = append(out.Sessions, acp.SessionInfo{SessionId: acp.SessionId(r.conv.session), Cwd: "/", Title: &t, UpdatedAt: &u,
				Meta: map[string]json.RawMessage{"relay/role": json.RawMessage(fmt.Sprintf("%q", r.ID))}})
		}
	}
	return out, nil
}

func (h *Hub) Cancel(_ context.Context, req acp.CancelNotification) error {
	h.logf("cancel %s", req.SessionId)
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, c := h.byRole(string(req.SessionId)); c != nil && c.cancel != nil {
		close(c.cancel)
		c.cancel = nil
	}
	return nil
}

func (h *Hub) setTurn(c *conv, turn string) {
	h.mu.Lock()
	c.turn = turn
	c.activity = h.now()
	h.mu.Unlock()
	h.notifyRoster()
}

func (h *Hub) Prompt(ctx context.Context, req acp.PromptRequest) (acp.PromptResponse, error) {
	var parts []string
	for _, b := range req.Prompt {
		if b.Text != nil {
			parts = append(parts, b.Text.Text)
		}
	}
	text := strings.Join(parts, "")
	enc, _ := json.Marshal(text)
	h.logf("prompt %s %s", req.SessionId, enc)

	h.mu.Lock()
	r, c := h.byRole(string(req.SessionId))
	if c == nil {
		h.mu.Unlock()
		return acp.PromptResponse{}, &acp.RequestError{Code: -32602, Message: "no such session"}
	}
	if c.busy {
		h.mu.Unlock()
		h.logf("refused %s", req.SessionId)
		return acp.PromptResponse{}, &acp.RequestError{Code: -32000, Message: "a turn is already running or queued"}
	}
	c.busy = true
	cancel := make(chan struct{})
	c.cancel = cancel
	c.finish = make(chan struct{})
	finish := c.finish
	h.mu.Unlock()

	// A role that is busy holds the prompt until it is not.
	for {
		h.mu.Lock()
		busy, wake := r.Busy, h.unbusy
		h.mu.Unlock()
		if !busy {
			break
		}
		h.setTurn(c, "queued")
		select {
		case <-wake:
		case <-cancel:
			h.endTurn(c)
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
	}
	h.mu.Lock()
	c.history = append(c.history, Said{User: text})
	h.mu.Unlock()
	h.setTurn(c, "running")

	reply := "echo: " + text
	if strings.HasPrefix(text, "ask") {
		outcome, err := h.askPermission(ctx, c, "send_mail")
		if err != nil {
			h.endTurn(c)
			return acp.PromptResponse{}, err
		}
		reply = "permission " + outcome
	}
	stop := acp.StopReasonEndTurn
	if text == "slow" {
		select {
		case <-cancel:
			stop = acp.StopReasonCancelled
		case <-finish:
		case <-ctx.Done():
			stop = acp.StopReasonCancelled
		}
	}
	if stop != acp.StopReasonCancelled {
		half := len(reply) / 2
		for _, chunk := range []string{reply[:half], reply[half:]} {
			h.update(string(req.SessionId), acp.UpdateAgentMessageText(chunk))
		}
		h.mu.Lock()
		c.history = append(c.history, Said{Agent: reply})
		h.mu.Unlock()
	}
	h.mu.Lock()
	c.turns++
	c.cancel = nil
	h.mu.Unlock()
	h.endTurn(c)
	return acp.PromptResponse{StopReason: stop}, nil
}

// endTurn frees the conversation for the next prompt at once and tells the
// roster a moment later, after the prompt's answer, as relay does.
func (h *Hub) endTurn(c *conv) {
	h.mu.Lock()
	c.busy = false
	h.mu.Unlock()
	time.AfterFunc(h.lag, func() { h.setTurn(c, "idle") })
}

// askPermission sends a permission request for c and waits for an answer,
// or for the control file to settle it.
func (h *Hub) askPermission(ctx context.Context, c *conv, name string) (string, error) {
	h.mu.Lock()
	h.calls++
	a := &ask{callID: fmt.Sprintf("call-%d", h.calls), done: make(chan struct{})}
	c.pendingAsk = a
	c.approvals++
	conn := h.conn
	h.mu.Unlock()
	h.notifyRoster()
	resp, err := conn.RequestPermission(context.WithoutCancel(ctx), acp.RequestPermissionRequest{
		SessionId: acp.SessionId(c.session),
		Options: []acp.PermissionOption{
			{Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once", OptionId: "allow"},
			{Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject", OptionId: "reject"},
		},
		ToolCall: acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(a.callID), Name: &name, RawInput: map[string]any{"to": "a@example.com"}},
	})
	h.mu.Lock()
	settled := c.pendingAsk != a // another device answered first
	if c.pendingAsk == a {
		c.pendingAsk = nil
		c.approvals--
	}
	h.mu.Unlock()
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(resp.Outcome)
	if settled {
		h.logf("permission-late %s %s", c.session, out)
		return "settled elsewhere", nil
	}
	h.logf("permission %s %s", c.session, out)
	h.notifyRoster()
	if resp.Outcome.Selected != nil {
		return string(resp.Outcome.Selected.OptionId), nil
	}
	return "cancelled", nil
}

// HandleExtensionMethod answers the hub's extension methods.
func (h *Hub) HandleExtensionMethod(_ context.Context, method string, params json.RawMessage) (any, error) {
	var p struct {
		SessionID string `json:"sessionId"`
		Turn      int    `json:"turn"`
	}
	_ = json.Unmarshal(params, &p)
	switch method {
	case "_relay/roles":
		h.logf("roles")
		h.mu.Lock()
		h.subscribe = true
		body := h.rosterJSON()
		h.mu.Unlock()
		return body, nil
	case "_relay/seen":
		h.logf("seen %s %d", p.SessionID, p.Turn)
		h.mu.Lock()
		if _, c := h.byRole(p.SessionID); c != nil && p.Turn > c.seen {
			c.seen = min(p.Turn, c.turns)
		}
		h.mu.Unlock()
		h.notifyRoster()
		return map[string]any{}, nil
	case "_relay/conversations/end":
		h.logf("end %s", p.SessionID)
		h.mu.Lock()
		if r, c := h.byRole(p.SessionID); c != nil {
			if c.cancel != nil {
				close(c.cancel)
				c.cancel = nil
			}
			r.conv = nil
		}
		h.mu.Unlock()
		h.notifyRoster()
		return map[string]any{}, nil
	}
	return nil, acp.NewMethodNotFound(method)
}

// Serve runs h over the connection until the peer goes away. A control
// file, when given, is read for commands while it runs.
func Serve(ctx context.Context, h *Hub, conn *acp.AgentSideConnection, control string) error {
	h.mu.Lock()
	h.conn = conn
	h.mu.Unlock()
	if control != "" {
		cctx, stop := context.WithCancel(ctx)
		defer stop()
		go h.watch(cctx, control)
	}
	err := conn.Wait(ctx)
	if errors.Is(err, acp.ErrPeerClosed) || errors.Is(err, acp.ErrConnectionClosed) {
		return nil
	}
	return err
}

// watch applies the control file's lines as they are appended.
func (h *Hub) watch(ctx context.Context, path string) {
	done := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// The last piece is empty, or a line still being written.
		lines := strings.Split(string(b), "\n")
		lines = lines[:len(lines)-1]
		for ; done < len(lines); done++ {
			if line := strings.TrimSpace(lines[done]); line != "" {
				h.control(ctx, line)
			}
		}
	}
}

func (h *Hub) control(ctx context.Context, line string) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return
	}
	cmd, id := fields[0], fields[1]
	h.mu.Lock()
	r := h.role(id)
	h.mu.Unlock()
	if r == nil {
		h.logf("control: no role %q", id)
		return
	}
	switch cmd {
	case "other-prompt":
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(line, cmd)), id))
		h.mu.Lock()
		c := r.conv
		if c == nil {
			h.mu.Unlock()
			return
		}
		c.history = append(c.history, Said{User: text}, Said{Agent: "echo: " + text})
		c.turns++
		c.activity = h.now()
		attached, session := c.attached, c.session
		h.mu.Unlock()
		if attached {
			h.update(session, acp.UpdateUserMessageText(text))
			h.update(session, acp.UpdateAgentMessageText("echo: "+text))
		}
		h.notifyRoster()
	case "ask":
		name := "send_mail"
		if len(fields) > 2 {
			name = fields[2]
		}
		h.mu.Lock()
		c := r.conv
		h.mu.Unlock()
		if c != nil {
			go func() { _, _ = h.askPermission(ctx, c, name) }()
		}
	case "settle":
		h.mu.Lock()
		c := r.conv
		var a *ask
		if c != nil {
			a = c.pendingAsk
			if a != nil {
				c.pendingAsk = nil
				c.approvals--
			}
		}
		conn := h.conn
		h.mu.Unlock()
		if a != nil {
			_ = conn.NotifyExtension(ctx, "_relay/permission/settled", map[string]any{
				"sessionId": c.session, "toolCallId": a.callID, "outcome": map[string]any{"outcome": "selected", "optionId": "allow"}})
			h.notifyRoster()
		}
	case "busy", "unbusy":
		h.mu.Lock()
		r.Busy = cmd == "busy"
		if cmd == "unbusy" {
			close(h.unbusy)
			h.unbusy = make(chan struct{})
		}
		h.mu.Unlock()
	case "working":
		h.mu.Lock()
		r.Working = len(fields) > 2 && fields[2] == "on"
		h.mu.Unlock()
		h.notifyRoster()
	case "start":
		h.mu.Lock()
		if r.conv == nil {
			h.newConv(r)
		}
		h.mu.Unlock()
		h.notifyRoster()
	case "end":
		h.mu.Lock()
		r.conv = nil
		h.mu.Unlock()
		h.notifyRoster()
	case "finish":
		h.mu.Lock()
		if c := r.conv; c != nil && c.finish != nil {
			select {
			case <-c.finish:
			default:
				close(c.finish)
			}
		}
		h.mu.Unlock()
	}
}
