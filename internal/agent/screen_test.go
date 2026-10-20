package agent

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/kirahowe/acp-tui/internal/core"
	"github.com/kirahowe/acp-tui/internal/jsoncases"
)

// The shared table (relay#58) through the real reader: the screen, then
// the SDK's connection and acp-tui's client. Only the accepted lines whose
// exact method is _relay/cards/changed reach the board; the capitalised
// and folded ones, which encoding/json v1 alone would read as that method,
// do not. Every rejected line is dropped, counted and logged.
func TestSharedTableThroughTheConnection(t *testing.T) {
	cases := jsoncases.Read(t)
	var stream bytes.Buffer
	wantChanged, wantDropped := 0, 0
	for _, c := range cases {
		stream.Write(c.Line)
		stream.WriteByte('\n')
		if c.Reject {
			wantDropped++
		} else if c.Method != nil && *c.Method == "_relay/cards/changed" {
			wantChanged++
		}
	}
	stream.WriteString("\n   \n") // blank lines are not counted
	// Notifications run in order, so when this one arrives every line
	// before it has been handled.
	stream.WriteString(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"end"}}}}` + "\n")

	var mu sync.Mutex
	var events []core.Event
	end := make(chan struct{})
	a := &Agent{emit: func(e core.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
		if c, ok := e.(core.TextChunk); ok && c.Text == "end" {
			close(end)
		}
	}}
	var log bytes.Buffer
	pr, pw := io.Pipe()
	src := newScreened(pr, &log)
	conn, err := acp.NewClientSideConnectionWithOptions(client{a}, io.Discard, src, acp.ConnectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = pw.Write(stream.Bytes()) }()
	select {
	case <-end:
	case <-time.After(10 * time.Second):
		t.Fatal("the last line never arrived")
	}
	_ = pw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = conn.Wait(ctx)

	mu.Lock()
	defer mu.Unlock()
	changed := 0
	for _, e := range events {
		if _, ok := e.(core.CardsChanged); ok {
			changed++
		}
	}
	if changed != wantChanged {
		t.Errorf("%d _relay/cards/changed reached the board, want %d", changed, wantChanged)
	}
	if got := int(src.dropped.Load()); got != wantDropped {
		t.Errorf("dropped %d lines, want %d", got, wantDropped)
	}
	if n := strings.Count(log.String(), "dropped a line from the agent"); n != wantDropped {
		t.Errorf("logged %d drops, want %d:\n%s", n, wantDropped, log.String())
	}
}

// A line longer than the SDK takes ends the stream with the error the SDK
// recognises, rather than being cut or skipped.
func TestScreenedLineTooLong(t *testing.T) {
	long := `{"jsonrpc":"2.0","method":"x","params":"` + strings.Repeat("a", maxLine) + `"}` + "\n"
	src := newScreened(strings.NewReader(long), io.Discard)
	_, err := io.ReadAll(src)
	if err == nil || !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("err = %v", err)
	}
}

// Valid lines pass in order and whole, through reads of any size.
func TestScreenedPassesLines(t *testing.T) {
	in := "{\"jsonrpc\":\"2.0\",\"method\":\"a\"}\r\n{\"a\":1,\"a\":2}\n\n {\"jsonrpc\":\"2.0\",\"method\":\"b\"} \n{\"jsonrpc\":\"2.0\",\"method\":\"c\"}"
	src := newScreened(strings.NewReader(in), io.Discard)
	var out bytes.Buffer
	buf := make([]byte, 3)
	for {
		n, err := src.Read(buf)
		out.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	want := "{\"jsonrpc\":\"2.0\",\"method\":\"a\"}\n {\"jsonrpc\":\"2.0\",\"method\":\"b\"} \n{\"jsonrpc\":\"2.0\",\"method\":\"c\"}\n"
	if out.String() != want {
		t.Fatalf("got %q\nwant %q", out.String(), want)
	}
}

// relay#52: the permission prompt shows the tool's input with its numbers
// as written. The SDK decodes rawInput into any, rounding each number
// through a float64, so acp-tui reads it from the request's own params.
func TestPermissionShowsTheInputAsWritten(t *testing.T) {
	asked := make(chan core.PermissionAsked, 8)
	a := &Agent{emit: func(e core.Event) {
		if p, ok := e.(core.PermissionAsked); ok {
			asked <- p
		}
	}}
	pr, pw := io.Pipe()
	conn, err := acp.NewClientSideConnectionWithOptions(client{a}, io.Discard, newScreened(pr, io.Discard), acp.ConnectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ask := func(id int, method, call, input, options string) string {
		return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"` + method + `","params":{"sessionId":"s","toolCall":{"toolCallId":"` + call +
			`","rawInput":` + input + `},"options":` + options + `}}` + "\n"
	}
	const allow = `[{"optionId":"allow","name":"Allow","kind":"allow_once"}]`
	exact := `{"text":"hello","n":9007199254740993,"big":123456789012345678901234567890,"d":0.1000000000000000055511151231257827}`
	want := `{"big":123456789012345678901234567890,"d":0.1000000000000000055511151231257827,"n":9007199254740993,"text":"hello"}`
	expect := func(what, want string) {
		t.Helper()
		select {
		case p := <-asked:
			if p.Input != want {
				t.Errorf("%s: input %s, want %s", what, p.Input, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: no permission request", what)
		}
	}
	write := func(line string) {
		go func() { _, _ = pw.Write([]byte(line)) }()
	}
	write(ask(1, "session/request_permission", "c1", exact, allow))
	expect("plain", want)
	// The method spelled with an escape is the same method.
	write(ask(2, `session\/request_permission`, "c2", exact, allow))
	expect("escaped method", want)
	// A request the SDK cannot decode (options of the wrong type) never
	// reaches the prompt, and lends its input to no later request with
	// the same tool call, though both inputs round to the same float64.
	write(ask(3, "session/request_permission", "c3", `{"n":9007199254740992}`, `"x"`))
	write(ask(4, "session/request_permission", "c3", `{"n":9007199254740993}`, allow))
	expect("after a refused request", `{"n":9007199254740993}`)
	// 1e400 is past a float64: the SDK cannot decode this one at all.
	write(ask(5, "session/request_permission", "c4", `{"l":[1e400]}`, allow))
	select {
	case p := <-asked:
		t.Fatalf("asked %q", p.Input)
	case <-time.After(500 * time.Millisecond):
	}
	_ = pw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = conn.Wait(ctx)
}
