package ui_test

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/vt"
)

// terminal is an x/vt emulator wired as a program's terminal, with its
// own lock (SafeEmulator leaves String and Scrollback unguarded). The
// emulator answers queries by writing to an unbuffered pipe; if nothing
// read it, a query at shutdown (Bubble Tea sends one) would block the
// emulator forever. So a goroutine always drains it into an inbox that
// the program reads.
type terminal struct {
	mu  sync.Mutex
	emu *vt.Emulator
	in  *inbox
	out bytes.Buffer // everything the program wrote
}

func newTerminal(w, h int) *terminal {
	t := &terminal{emu: vt.NewEmulator(w, h), in: newInbox()}
	go func() {
		_, _ = io.Copy(t.in, t.emu)
		t.in.Close()
	}()
	return t
}

// Write takes the program's output. With no tty, Bubble Tea assumes the
// terminal turns a line feed into CR LF, as a tty's ONLCR does, so this
// does the same.
func (t *terminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.out.Write(p)
	if _, err := t.emu.Write(bytes.ReplaceAll(p, []byte("\n"), []byte("\r\n"))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// String is the screen as text.
func (t *terminal) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.emu.String()
}

// written is everything the program has written so far.
func (t *terminal) written() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out.String()
}

// scrollback is the lines that scrolled off the top, as text.
func (t *terminal) scrollback() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []string
	for _, l := range t.emu.Scrollback().Lines() {
		out = append(out, strings.TrimRight(l.String(), " "))
	}
	return out
}

// SendText types raw bytes, as a terminal would send them.
func (t *terminal) SendText(s string) { _, _ = io.WriteString(t.emu.InputPipe(), s) }

// close stops the drain. Call it once the program has stopped.
func (t *terminal) close() { _ = t.emu.InputPipe().(io.Closer).Close() }

// inbox is an unbounded pipe: Write never blocks.
type inbox struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newInbox() *inbox {
	b := &inbox{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *inbox) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	b.cond.Broadcast()
	return len(p), nil
}

func (b *inbox) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.buf) == 0 && !b.closed {
		b.cond.Wait()
	}
	if len(b.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.buf)
	b.buf = b.buf[n:]
	return n, nil
}

func (b *inbox) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.cond.Broadcast()
}

// bare is a minimal inline program: a two-row view, and a print of n
// lines when it gets printMsg.
type bare struct{ n int }

type printMsg struct{}

func (b bare) Init() tea.Cmd { return nil }

func (b bare) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(printMsg); ok {
		var lines []string
		for i := range b.n {
			lines = append(lines, fmt.Sprintf("printed %d", i))
		}
		return b, tea.Println(strings.Join(lines, "\n"))
	}
	return b, nil
}

func (b bare) View() tea.View { return tea.NewView("VIEW TOP\nVIEW BOTTOM") }

// waitScreen polls the emulator until cond holds or the time is up.
func waitScreen(t *testing.T, emu *terminal, d time.Duration, cond func(string) bool) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		s := emu.String()
		if cond(s) {
			return s, true
		}
		if time.Now().After(deadline) {
			return s, false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Bubble Tea #1822: one Println taller than the room above the view
// removes the view. If x/vt reproduces it, the emulator is faithful enough
// for the inline regression tests. If this starts failing, upstream fixed
// the bug and printChunks can go.
func TestVTReproduces1822(t *testing.T) {
	emu := newTerminal(40, 10)
	p := tea.NewProgram(bare{n: 15}, tea.WithInput(emu.in), tea.WithOutput(emu), tea.WithWindowSize(40, 10), tea.WithoutSignals())
	done := make(chan struct{})
	go func() { _, _ = p.Run(); close(done) }()
	defer func() { p.Kill(); <-done; emu.close() }()

	if s, ok := waitScreen(t, emu, 3*time.Second, func(s string) bool { return strings.Contains(s, "VIEW BOTTOM") }); !ok {
		t.Fatalf("view never drawn:\n%s", s)
	}
	p.Send(printMsg{})
	if _, ok := waitScreen(t, emu, 3*time.Second, func(s string) bool { return strings.Contains(s, "printed 14") }); !ok {
		t.Log("print never appeared")
	}
	time.Sleep(200 * time.Millisecond)
	s := emu.String()
	t.Logf("screen after a 15-line print at 40x10:\n%s", s)
	if strings.Contains(s, "VIEW TOP") && strings.Contains(s, "VIEW BOTTOM") {
		t.Fatal("the view survived: x/vt does not reproduce #1822")
	}
}
