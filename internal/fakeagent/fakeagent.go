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
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	raw           json.RawMessage
}

// Agent plays a script.
type Agent struct {
	mu        sync.Mutex
	conn      *acp.AgentSideConnection
	log       io.Writer
	steps     []step
	pos       int
	cancelled chan struct{}
}

// New parses a script. log receives one line per prompt and cancel.
func New(script []byte, log io.Writer) (*Agent, error) {
	a := &Agent{log: log}
	for i, line := range strings.Split(string(script), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var s step
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			return nil, fmt.Errorf("script line %d: %w", i+1, err)
		}
		s.raw = json.RawMessage(line)
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
	return acp.InitializeResponse{
		AgentInfo:       &acp.Implementation{Name: "fakeagent", Version: "0"},
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
	}, nil
}

func (a *Agent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	return acp.NewSessionResponse{SessionId: "fake-session"}, nil
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
	var text []string
	for _, b := range req.Prompt {
		if b.Text != nil {
			text = append(text, b.Text.Text)
		}
	}
	prompt, _ := json.Marshal(strings.Join(text, ""))
	a.logf("prompt %s", prompt)

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
		case s.RetryInMS != nil:
			if err := a.sendRetry(ctx, req.SessionId, s); err != nil {
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
