// Package ui is the Bubble Tea program: a thin shell around the core. Its
// Update turns a message into at most one core event, runs core.Reduce,
// and turns the effects into commands. keys.go and print.go are pure.
package ui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/kirahowe/acp-tui/internal/core"
	"github.com/kirahowe/acp-tui/internal/render"
)

// Conn is the agent, as the UI uses it. None of its methods may block
// except Shutdown.
type Conn interface {
	Prompt(text string)
	Cancel()
	Shutdown()
}

// EventMsg carries a core event from the agent into the program.
type EventMsg struct{ Event core.Event }

type (
	tickMsg    time.Time
	readyMsg   struct{}
	printedMsg struct{}
)

// tickEvery is how often the status line redraws while the agent works.
const tickEvery = 100 * time.Millisecond

// readyDelay is how long after the first size message the first frame is
// taken to be on screen. Bubble Tea #1740: a print before the first flush
// moves the cursor down a whole screen.
const readyDelay = 50 * time.Millisecond

type Model struct {
	core    core.State
	input   textarea.Model
	caps    Caps
	w, h    int
	conn    Conn
	sized   bool
	ticking bool
	// printing is set while lines are on their way to scrollback; more
	// wait until they land, so they arrive in order.
	printing bool
	// pinned is set once a first print has moved the frame to the bottom
	// row of the screen.
	pinned bool
	// quitting is set once the shell begins shutting down; Died says the
	// agent exited before that.
	quitting bool
	died     bool
}

// New returns the model for a conversation with conn. title names the
// session in the banner; empty means use the agent's own title or name.
func New(conn Conn, title string) Model {
	ta := textarea.New()
	ta.SetStyles(textarea.Styles{})
	ta.SetVirtualCursor(false)
	ta.SetPromptFunc(2, func(p textarea.PromptInfo) string {
		if p.LineNumber == 0 {
			return "› "
		}
		return "  "
	})
	ta.ShowLineNumbers = false
	ta.Placeholder = ""
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 6              // until the first size message
	ta.MaxContentHeight = 1 << 20 // the box scrolls past MaxHeight, without a cap
	ta.KeyMap.InsertNewline.SetEnabled(false)
	ta.Focus()
	return Model{input: ta, conn: conn, core: core.State{Title: title}}
}

// Died reports whether the agent exited on its own, and why.
func (m Model) Died() (bool, string) { return m.died, m.core.Exit }

// Init does nothing: the first size message starts the ticks.
func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	now := time.Now()
	var ev core.Event
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.MaxHeight = max(m.h/2-2, 1) // grow to about half the screen
		m.input.SetWidth(render.InputWidth(m.width()))
		if !m.sized {
			m.sized = true
			cmds = append(cmds, tea.Tick(readyDelay, func(time.Time) tea.Msg { return readyMsg{} }))
		}
	case tea.KeyboardEnhancementsMsg:
		m.caps.Disambiguation = msg.SupportsKeyDisambiguation()
	case tea.KeyPressMsg:
		var cmd tea.Cmd
		ev, cmd = m.key(msg, now)
		cmds = append(cmds, cmd)
	case tea.PasteMsg:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	case EventMsg:
		ev = msg.Event
		if _, ok := ev.(core.AgentExited); ok && !m.quitting {
			m.died = true
		}
	case tickMsg:
		m.ticking = false
		ev = core.Tick{At: time.Time(msg)}
	case readyMsg:
		ev = core.TerminalReady{At: now}
	case printedMsg:
		m.printing = false
	}
	if ev != nil {
		var fx []core.Effect
		m.core, fx = core.Reduce(m.core, ev)
		for _, f := range fx {
			cmds = append(cmds, m.effect(f, now))
		}
	}
	cmds = append(cmds, m.print(now), m.tick())
	return m, tea.Batch(cmds...)
}

// key turns a key press into an event, or passes it to the input box.
func (m *Model) key(k tea.KeyPressMsg, now time.Time) (core.Event, tea.Cmd) {
	empty := m.input.Value() == ""
	switch Classify(k, empty) {
	case Send:
		text := m.input.Value()
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		m.input.Reset()
		return core.Submit{At: now, Text: text}, nil
	case Newline:
		m.input.InsertString("\n")
		return nil, nil
	case Interrupt:
		if m.core.Phase == core.Idle && !empty {
			m.input.Reset() // like a shell: Ctrl-C clears the line
		}
		return core.Interrupt{At: now, InputEmpty: empty}, nil
	case EOF:
		return core.EOF{At: now}, nil
	case ShowTools:
		return core.ShowTools{At: now}, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return nil, cmd
}

// effect carries out one effect of Reduce.
func (m *Model) effect(f core.Effect, now time.Time) tea.Cmd {
	switch f := f.(type) {
	case core.SendPrompt:
		m.conn.Prompt(f.Text)
	case core.SendCancel:
		m.conn.Cancel()
	case core.RestoreInput:
		text := f.Text
		if v := m.input.Value(); v != "" {
			text += "\n" + v
		}
		m.input.SetValue(text)
	case core.Quit:
		if m.quitting {
			return nil
		}
		m.quitting = true
		conn := m.conn
		return func() tea.Msg {
			conn.Shutdown()
			return tea.Quit()
		}
	}
	return nil
}

// print puts the lines that have scrolled off the top of the frame into
// scrollback. The frame fills all but the last row of the screen, so
// each line is its own tea.Println (see PrintChunks). The core moves its
// cursor at once: the frame no longer shows those lines either way.
// Nothing is printed before the first frame is on screen. The first
// print starts with a blank line: on a cleared screen the frame starts at
// the top row and ends one row short of the bottom, and a print scrolls
// it down there.
func (m *Model) print(now time.Time) tea.Cmd {
	if !m.core.Ready || m.printing {
		return nil
	}
	f := m.frame(now)
	lines := f.Print
	if len(lines) > 0 {
		m.core, _ = core.Reduce(m.core, core.Printed{At: now, Upto: f.PrintTo})
		if m.core.Printed != f.PrintTo {
			lines = nil // the core refused it; printing would duplicate lines
		}
	}
	if !m.pinned {
		m.pinned = true
		lines = append([]string{""}, lines...)
	}
	if len(lines) == 0 {
		return nil
	}
	m.printing = true
	var cmds []tea.Cmd
	for _, chunk := range PrintChunks(lines, m.h-strings.Count(f.Content, "\n")-1) {
		cmds = append(cmds, tea.Println(chunk))
	}
	cmds = append(cmds, func() tea.Msg { return printedMsg{} })
	return tea.Sequence(cmds...)
}

// tick keeps the status line moving while the agent works, and stops when
// it is idle, so an idle screen is never redrawn.
func (m *Model) tick() tea.Cmd {
	if m.ticking || (m.core.Phase != core.Busy && m.core.Phase != core.Starting) {
		return nil
	}
	m.ticking = true
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// width is the width everything is drawn at: one column narrower than
// the screen, so a wide grapheme measured differently cannot wrap (Bubble
// Tea #1802), and printed boxes line up with the input box.
func (m Model) width() int { return max(m.w-1, 1) }

func (m Model) frame(now time.Time) render.Frame {
	return render.View(m.core, m.input.View(), NewlineHint(m.caps), now, m.width(), max(m.h, 2))
}

func (m Model) View() tea.View {
	f := m.frame(time.Now())
	v := tea.NewView(f.Content)
	if c := m.input.Cursor(); c != nil {
		c.Position.X += f.InputX
		c.Position.Y += f.InputY
		v.Cursor = c
	}
	return v
}
