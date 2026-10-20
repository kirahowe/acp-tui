package agent

import (
	"bytes"
	"context"
	"io"
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
