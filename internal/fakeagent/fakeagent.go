// Package fakeagent is a scripted ACP agent for tests. A script is JSON
// lines, played in order across prompts. Each line is one step:
//
//	{"sessionUpdate": ...}          send this session update as it is
//	{"stopReason": "end_turn"}      answer the prompt
//	{"error": {"code": ..., ...}}   fail the prompt
//	{"sleep_ms": 1500}              wait (a cancel ends the wait)
//	{"wait_cancel": true}           wait for session/cancel
//	{"retry_in_ms": 3000, "title": ..., "description": ...}
//	                                send an agentloop retry notice that
//	                                many milliseconds ahead
//
//	{"request_permission": {"toolCallId": ..., "name": ..., "title": ...,
//	  "rawInput": ..., "options": [...]}}
//	                                ask the client; the answer is logged as
//	                                "permission <outcome JSON>". The options
//	                                default to allow_once and reject_once.
//
//	{"request_permission": {..., "content": [...], "_meta": {...}}}
//	                                content and _meta go on the request's
//	                                tool call, so a script can give a
//	                                request relay's preview.
//
//	{"extension": {"method": "_relay/items", "result": {...}}}
//	                                answer a relay extension method. These
//	                                lines are not steps: they are read when
//	                                the script loads, and an agent without
//	                                one for a method answers method-not-
//	                                found, as a plain agentloop does.
//	                                "error": {"code": ..., "message": ...}
//	                                answers with an error instead. For
//	                                _relay/resolve, "ref" picks the
//	                                reference it answers. Several lines for
//	                                _relay/items answer in turn, the last
//	                                one from then on; "after_trust": true
//	                                marks the answer that takes over once
//	                                a _relay/trust has succeeded. Each call
//	                                is logged as "extension <method>
//	                                <params>". Several lines for
//	                                _relay/cards or _relay/cards/op also
//	                                answer in turn, the last one from then
//	                                on; "card": n limits a line to calls
//	                                about card n, which count apart.
//	                                "notify": {"method": ...,
//	                                "params": ...} on a line sends that
//	                                notification just before the answer,
//	                                as relay announces a changed card.
//
//	{"notify": {"method": "_relay/cards/changed", "params": {...}}}
//	                                send an extension notification, such
//	                                as a card another process changed
//
// So an agentloop fixture is a script as it stands. After a cancel the
// prompt answers "cancelled" at once, and the script carries on after
// the next stopReason or error. A prompt that finds the script used up
// answers end_turn. Every prompt and cancel is written to the log.
package fakeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"
)

type step struct {
	SessionUpdate *string           `json:"sessionUpdate"`
	StopReason    *string           `json:"stopReason"`
	Error         *acp.RequestError `json:"error"`
	SleepMS       *int              `json:"sleep_ms"`
	WaitCancel    bool              `json:"wait_cancel"`
	RetryInMS     *int              `json:"retry_in_ms"`
	Permission    json.RawMessage   `json:"request_permission"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Extension     *extension        `json:"extension"`
	Notify        *notification     `json:"notify"`
	raw           json.RawMessage
}

// notification is an extension notification to send the client.
type notification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// extension is a canned answer to an extension method.
type extension struct {
	Method     string            `json:"method"`
	Ref        string            `json:"ref"`
	AfterTrust bool              `json:"after_trust"`
	Result     json.RawMessage   `json:"result"`
	Error      *acp.RequestError `json:"error"`
	Notify     *notification     `json:"notify"`
	Card       *int64            `json:"card"`
}

// Info is what the agent says about itself: Title goes in agentInfo,
// Model, when set, is offered as a session config option of category
// model, and Embedded advertises promptCapabilities.embeddedContext.
type Info struct {
	Title, Model string
	Embedded     bool
}

// Agent plays a script.
type Agent struct {
	info      Info
	mu        sync.Mutex
	conn      *acp.AgentSideConnection
	log       io.Writer
	steps     []step
	pos       int
	cancelled chan struct{}
	ext       []extension
	itemsCall int            // how many _relay/items calls have been answered
	calls     map[string]int // how many calls of other in-turn methods have been answered
	trusted   bool           // a _relay/trust has succeeded
}

// New parses a script. log receives one line per prompt and cancel.
func New(script []byte, log io.Writer, info Info) (*Agent, error) {
	a := &Agent{log: log, info: info}
	for i, line := range strings.Split(string(script), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var s step
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			return nil, fmt.Errorf("script line %d: %w", i+1, err)
		}
		s.raw = json.RawMessage(line)
		if s.Extension != nil {
			a.ext = append(a.ext, *s.Extension)
			continue
		}
		a.steps = append(a.steps, s)
	}
	return a, nil
}

func (a *Agent) logf(format string, args ...any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fmt.Fprintf(a.log, format+"\n", args...)
}

func (a *Agent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	impl := &acp.Implementation{Name: "fakeagent", Version: "0"}
	if a.info.Title != "" {
		impl.Title = &a.info.Title
	}
	return acp.InitializeResponse{
		AgentInfo:       impl,
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		AgentCapabilities: acp.AgentCapabilities{
			PromptCapabilities: acp.PromptCapabilities{EmbeddedContext: a.info.Embedded},
		},
	}, nil
}

func (a *Agent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	resp := acp.NewSessionResponse{SessionId: "fake-session"}
	if a.info.Model != "" {
		cat := acp.SessionConfigOptionCategoryModel
		opts := acp.SessionConfigSelectOptionsUngrouped{{Name: a.info.Model, Value: acp.SessionConfigValueId(a.info.Model)}}
		resp.ConfigOptions = []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{
			Id: "model", Name: "Model", Type: "select", Category: &cat,
			CurrentValue: acp.SessionConfigValueId(a.info.Model), Options: acp.SessionConfigSelectOptions{Ungrouped: &opts},
		}}}
	}
	return resp, nil
}

func (a *Agent) Cancel(context.Context, acp.CancelNotification) error {
	a.logf("cancel")
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelled != nil {
		close(a.cancelled)
		a.cancelled = nil
	}
	return nil
}

func (a *Agent) Prompt(ctx context.Context, req acp.PromptRequest) (acp.PromptResponse, error) {
	var text, resources []string
	for _, b := range req.Prompt {
		if b.Text != nil {
			text = append(text, b.Text.Text)
		}
		if r := b.Resource; r != nil && r.Resource.TextResourceContents != nil {
			t := r.Resource.TextResourceContents
			mime := ""
			if t.MimeType != nil {
				mime = *t.MimeType
			}
			res, _ := json.Marshal(map[string]string{"uri": t.Uri, "mimeType": mime, "text": t.Text})
			resources = append(resources, string(res))
		}
	}
	prompt, _ := json.Marshal(strings.Join(text, ""))
	a.logf("prompt %s", prompt)
	for _, r := range resources {
		a.logf("resource %s", r)
	}

	cancelled := make(chan struct{})
	a.mu.Lock()
	a.cancelled = cancelled
	a.mu.Unlock()
	stop := func() (acp.PromptResponse, error) {
		a.skipTurn()
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}

	for {
		s, ok := a.next()
		if !ok {
			return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
		}
		switch {
		case s.SessionUpdate != nil:
			if err := a.send(ctx, req.SessionId, s.raw); err != nil {
				return acp.PromptResponse{}, err
			}
		case s.Permission != nil:
			if err := a.askPermission(ctx, req.SessionId, s.Permission); err != nil {
				return acp.PromptResponse{}, err
			}
		case s.RetryInMS != nil:
			if err := a.sendRetry(ctx, req.SessionId, s); err != nil {
				return acp.PromptResponse{}, err
			}
		case s.Notify != nil:
			if err := a.notify(ctx, *s.Notify); err != nil {
				return acp.PromptResponse{}, err
			}
		case s.StopReason != nil:
			return acp.PromptResponse{StopReason: acp.StopReason(*s.StopReason)}, nil
		case s.Error != nil:
			return acp.PromptResponse{}, s.Error
		case s.SleepMS != nil:
			select {
			case <-time.After(time.Duration(*s.SleepMS) * time.Millisecond):
			case <-cancelled:
				return stop()
			case <-ctx.Done():
				return stop()
			}
		case s.WaitCancel:
			select {
			case <-cancelled:
			case <-ctx.Done():
			}
			return stop()
		}
	}
}

func (a *Agent) next() (step, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pos >= len(a.steps) {
		return step{}, false
	}
	a.pos++
	return a.steps[a.pos-1], true
}

// skipTurn moves past the rest of the current turn after a cancel.
func (a *Agent) skipTurn() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for a.pos < len(a.steps) {
		s := a.steps[a.pos]
		a.pos++
		if s.StopReason != nil || s.Error != nil {
			return
		}
	}
}

func (a *Agent) send(ctx context.Context, session acp.SessionId, update json.RawMessage) error {
	params, err := json.Marshal(map[string]any{"sessionId": session, "update": update})
	if err != nil {
		return err
	}
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	return conn.SessionUpdateRaw(ctx, params)
}

// askPermission sends session/request_permission and logs the outcome. A cancel
// does not abort the wait: the test wants to see how the client answered.
func (a *Agent) askPermission(ctx context.Context, session acp.SessionId, spec json.RawMessage) error {
	var p struct {
		ToolCallID string                     `json:"toolCallId"`
		Name       *string                    `json:"name"`
		Title      *string                    `json:"title"`
		RawInput   any                        `json:"rawInput"`
		Options    json.RawMessage            `json:"options"`
		Content    []acp.ToolCallContent      `json:"content"`
		Meta       map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(spec, &p); err != nil {
		return err
	}
	opts := []acp.PermissionOption{
		{Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow once", OptionId: "allow"},
		{Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject", OptionId: "reject"},
	}
	if p.Options != nil {
		opts = nil
		if err := json.Unmarshal(p.Options, &opts); err != nil {
			return err
		}
	}
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	resp, err := conn.RequestPermission(context.WithoutCancel(ctx), acp.RequestPermissionRequest{
		SessionId: session,
		Options:   opts,
		ToolCall: acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(p.ToolCallID), Name: p.Name, Title: p.Title, RawInput: p.RawInput,
			Content: p.Content, Meta: p.Meta},
	})
	if err != nil {
		return err
	}
	out, _ := json.Marshal(resp.Outcome)
	a.logf("permission %s", out)
	return nil
}

func (a *Agent) sendRetry(ctx context.Context, session acp.SessionId, s step) error {
	at := time.Now().Add(time.Duration(*s.RetryInMS) * time.Millisecond).UTC().Format(time.RFC3339Nano)
	update, err := json.Marshal(map[string]any{
		"sessionUpdate": "notice", "severity": "warning", "title": s.Title, "description": s.Description,
		"_meta": map[string]any{"agentloop/retry": map[string]any{"retry_at": at}},
	})
	if err != nil {
		return err
	}
	return a.send(ctx, session, update)
}

// notify sends an extension notification to the client.
func (a *Agent) notify(ctx context.Context, n notification) error {
	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()
	return conn.NotifyExtension(ctx, n.Method, n.Params)
}

// Serve runs a over the connection until the peer goes away.
func Serve(ctx context.Context, a *Agent, conn *acp.AgentSideConnection) error {
	a.mu.Lock()
	a.conn = conn
	a.mu.Unlock()
	err := conn.Wait(ctx)
	if errors.Is(err, acp.ErrPeerClosed) || errors.Is(err, acp.ErrConnectionClosed) {
		return nil
	}
	return err
}

// HandleExtensionMethod answers relay's extension methods from the
// script's canned answers, and logs the call.
func (a *Agent) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	a.logf("extension %s %s", method, params)
	want := a.answer(method, params)
	if want == nil {
		return nil, acp.NewMethodNotFound(method)
	}
	if want.Notify != nil {
		if err := a.notify(ctx, *want.Notify); err != nil {
			return nil, err
		}
	}
	if want.Error != nil {
		return nil, want.Error
	}
	return want.Result, nil
}

// answer picks the canned answer to a call, or nil.
func (a *Agent) answer(method string, params json.RawMessage) *extension {
	a.mu.Lock()
	defer a.mu.Unlock()
	var want *extension
	switch method {
	case "_relay/items":
		var turn, base []extension
		for _, e := range a.ext {
			if e.Method == method {
				if e.AfterTrust {
					turn = append(turn, e)
				} else {
					base = append(base, e)
				}
			}
		}
		list := base
		if a.trusted && len(turn) > 0 {
			list = turn
		}
		if len(list) > 0 {
			want = &list[min(a.itemsCall, len(list)-1)]
			a.itemsCall++
		}
	case "_relay/resolve":
		var p struct {
			Ref string `json:"ref"`
		}
		_ = json.Unmarshal(params, &p)
		for i, e := range a.ext {
			if e.Method == method && (e.Ref == "" || e.Ref == p.Ref) {
				want = &a.ext[i]
				break
			}
		}
	case "_relay/cards", "_relay/cards/op":
		var p struct {
			Card *int64 `json:"card"`
		}
		_ = json.Unmarshal(params, &p)
		var list []extension
		for _, e := range a.ext {
			if e.Method == method && (e.Card == nil || (p.Card != nil && *e.Card == *p.Card)) {
				list = append(list, e)
			}
		}
		if len(list) > 0 {
			if a.calls == nil {
				a.calls = map[string]int{}
			}
			k := method
			if p.Card != nil {
				k += fmt.Sprint(" ", *p.Card)
			}
			want = &list[min(a.calls[k], len(list)-1)]
			a.calls[k]++
		}
	default:
		for i, e := range a.ext {
			if e.Method == method {
				want = &a.ext[i]
				break
			}
		}
	}
	if want != nil && want.Error == nil && method == "_relay/trust" {
		a.trusted = true
	}
	return want
}
