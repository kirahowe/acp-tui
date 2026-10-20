// Package agent runs the ACP agent as a child process and turns what it
// says into core events. It is part of the shell: it starts goroutines,
// reads the clock and talks to the SDK.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"
	"github.com/caelis-labs/acp-go-sdk/transport/stdio"

	"github.com/kirahowe/acp-tui/internal/core"
	"github.com/kirahowe/acp-tui/internal/wire"
)

// ShutdownGrace is how long the agent gets to exit after its stdin closes
// before its process tree is killed.
const ShutdownGrace = 3 * time.Second

// Agent is one agent process with one session.
type Agent struct {
	proc    *stdio.ClientProcess
	emit    func(core.Event)
	session acp.SessionId // set before Connected is emitted

	mu      sync.Mutex
	nextAsk uint64
	asks    map[uint64]chan core.AnswerPermission // requests waiting for the person

	// Calls to relay's extension methods run one at a time, in the order
	// they were asked, so an older answer cannot overwrite a newer one.
	extQueue   []func()
	extRunning bool
}

// Start launches command in dir and begins the handshake in the
// background. emit receives every event, from any goroutine, in order for
// each source; it may block (it does: Bubble Tea's Send). Connected
// follows a good handshake; AgentExited follows a failed one or the
// process ending.
func Start(command []string, dir string, stderr io.Writer, emit func(core.Event)) (*Agent, error) {
	if len(command) == 0 {
		return nil, errors.New("no agent command")
	}
	a := &Agent{emit: emit}
	proc, err := stdio.StartClient(context.Background(), client{a}, stdio.Command{
		Executable: command[0], Args: command[1:], Dir: dir, Stderr: stderr,
	}, acp.ConnectionOptions{})
	if err != nil {
		return nil, err
	}
	a.proc = proc
	var once sync.Once
	exited := func(err error) {
		once.Do(func() {
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			emit(core.AgentExited{At: time.Now(), Err: msg})
		})
	}
	go func() {
		if err := a.handshake(dir); err != nil {
			exited(fmt.Errorf("handshake: %w", err))
			_ = proc.Close()
		}
	}()
	go func() { exited(proc.Wait(context.Background())) }()
	return a, nil
}

func (a *Agent) handshake(dir string) error {
	ctx := context.Background()
	init, err := a.proc.Connection.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion(acp.ProtocolVersionNumber),
		ClientCapabilities: wire.Capabilities(),
		ClientInfo:         &acp.Implementation{Name: "acp-tui", Version: "0"},
	})
	if err != nil {
		return err
	}
	sess, err := a.proc.Connection.NewSession(ctx, acp.NewSessionRequest{Cwd: dir, McpServers: []acp.McpServer{}})
	if err != nil {
		return err
	}
	a.session = sess.SessionId
	a.emit(wire.Connected(init, sess, time.Now()))
	return nil
}

// Prompt sends text, with atts as resources, and, when the agent answers, emits PromptDone or
// PromptFailed. It does not block.
func (a *Agent) Prompt(text string, atts []core.Attachment) {
	go func() {
		resp, err := a.proc.Connection.Prompt(context.Background(), acp.PromptRequest{
			SessionId: a.session,
			Prompt:    wire.PromptBlocks(text, atts),
		})
		a.emit(wire.Response(resp, err, time.Now()))
	}()
}

// Cancel asks the agent to stop the current turn. It does not block.
func (a *Agent) Cancel() {
	go func() {
		_ = a.proc.Connection.Cancel(context.Background(), acp.CancelNotification{SessionId: a.session})
	}()
}

// Shutdown closes the agent's stdin and waits for it to exit, killing its
// process tree after ShutdownGrace.
func (a *Agent) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
	defer cancel()
	_ = a.proc.Shutdown(ctx)
}

// client answers the agent's calls.
type client struct{ a *Agent }

// SessionUpdate emits synchronously: the SDK runs a turn's notification
// handlers before that turn's Prompt returns, so every update reaches the
// core before its PromptDone.
func (c client) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	now := time.Now()
	if l, ok := wire.Labels(n, now); ok {
		c.a.emit(l)
	}
	c.a.emit(wire.Update(n, now))
	return nil
}

// RequestPermission hands the request to the UI and waits for the
// person's answer, which arrives through Answer. A request nobody
// answers before the connection ends is cancelled.
func (c client) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	a := c.a
	reply := make(chan core.AnswerPermission, 1)
	a.mu.Lock()
	a.nextAsk++
	id := a.nextAsk
	if a.asks == nil {
		a.asks = map[uint64]chan core.AnswerPermission{}
	}
	a.asks[id] = reply
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.asks, id)
		a.mu.Unlock()
	}()
	now := time.Now()
	if l, ok := wire.PermissionLabels(req, now); ok {
		a.emit(l)
	}
	a.emit(wire.Permission(req, id, now))
	select {
	case ans := <-reply:
		return wire.Outcome(ans), nil
	case <-ctx.Done():
		return wire.Outcome(core.AnswerPermission{ID: id, Cancelled: true}), nil
	}
}

// Answer replies to a permission request. It does not block.
func (a *Agent) Answer(ans core.AnswerPermission) {
	a.mu.Lock()
	reply := a.asks[ans.ID]
	a.mu.Unlock()
	if reply != nil {
		select {
		case reply <- ans:
		default: // answered already
		}
	}
}

// extTimeout is how long a call to relay's extension methods may take.
const extTimeout = 15 * time.Second

// enqueue runs f after the calls queued before it.
func (a *Agent) enqueue(f func()) {
	a.mu.Lock()
	a.extQueue = append(a.extQueue, f)
	start := !a.extRunning
	a.extRunning = true
	a.mu.Unlock()
	if start {
		go a.extLoop()
	}
}

func (a *Agent) extLoop() {
	for {
		a.mu.Lock()
		if len(a.extQueue) == 0 {
			a.extRunning = false
			a.mu.Unlock()
			return
		}
		f := a.extQueue[0]
		a.extQueue = a.extQueue[1:]
		a.mu.Unlock()
		f()
	}
}

// call sends an extension method to the agent and waits for the answer.
func (a *Agent) call(method string, params any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), extTimeout)
	defer cancel()
	return a.proc.Connection.CallExtension(ctx, method, params)
}

// Items asks relay for the workspace's items and emits ItemsLoaded. A
// plain agent answers method-not-found. It does not block.
func (a *Agent) Items() {
	a.enqueue(func() {
		raw, err := a.call(wire.MethodItems, wire.ItemsParams(a.session))
		a.emit(wire.Items(raw, err, time.Now()))
	})
}

// Resolve asks relay about a reference and emits CitationResolved. It
// does not block.
func (a *Agent) Resolve(ref string) {
	a.enqueue(func() {
		raw, err := a.call(wire.MethodResolve, wire.ResolveParams(a.session, ref))
		a.emit(wire.Resolution(ref, raw, err, time.Now()))
	})
}

// Trust asks relay to trust the doc at path as it was listed and emits
// TrustDone. It does not block.
func (a *Agent) Trust(path, version, sha string) {
	a.enqueue(func() {
		raw, err := a.call(wire.MethodTrust, wire.TrustParams(a.session, path, version, sha))
		a.emit(wire.TrustOutcome(path, raw, err, time.Now()))
	})
}

// Cards asks relay for the board and emits CardsLoaded. It does not
// block.
func (a *Agent) Cards() {
	a.enqueue(func() {
		raw, err := a.call(wire.MethodCards, wire.CardsParams(a.session))
		a.emit(wire.Cards(raw, err, time.Now()))
	})
}

// CardOp sends relay an op on a card and emits CardOpDone. It does not
// block.
func (a *Agent) CardOp(card int64, op core.CardOp) {
	a.enqueue(func() {
		raw, err := a.call(wire.MethodCardOp, wire.CardOpParams(a.session, card, op))
		a.emit(wire.CardOpOutcome(card, raw, err, time.Now()))
	})
}

// HandleExtensionMethod takes relay's _relay/cards/changed notification.
// The client answers no extension request.
func (c client) HandleExtensionMethod(_ context.Context, method string, params json.RawMessage) (any, error) {
	if method == wire.MethodCardsChanged {
		if e, ok := wire.CardsChanged(params, time.Now()); ok {
			c.a.emit(e)
		}
		return nil, nil
	}
	return nil, acp.NewMethodNotFound(method)
}
