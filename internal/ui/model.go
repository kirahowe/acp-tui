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
	Prompt(text string, atts []core.Attachment)
	Cancel()
	Answer(core.AnswerPermission)
	// Items, Resolve and Trust call relay's extension methods; their
	// answers come back as events.
	Items()
	Resolve(ref string)
	Trust(path, version, sha string)
	Shutdown()
}

// EventMsg carries a core event from the agent into the program.
type EventMsg struct{ Event core.Event }

type (
	tickMsg    time.Time
	readyMsg   struct{}
	printedMsg struct{}
	// backMsg says the inline frame is on screen again after the
	// transcript view.
	backMsg struct{}
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
	// quitting is set once the shell begins shutting down, and stopping
	// once everything has printed and the program is told to stop; Died
	// says the agent exited before that.
	quitting, stopping bool
	died               bool
	// alt is set while the transcript view is on the alternate screen,
	// and back until the inline frame is drawn again after it. Nothing
	// is printed meanwhile: Bubble Tea would write the lines into the
	// alternate screen, and they would never reach scrollback.
	alt, back bool
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
		m.input.SetWidth(render.InputWidth(render.MainWidth(m.width())))
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
	case tea.MouseWheelMsg:
		if m.core.Transcript.Open {
			ev = m.wheel(msg, now)
		}
	case tea.PasteMsg:
		ev = core.Pasted{At: now, Text: msg.Content} // the core drops it when the input is not in play
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
	case backMsg:
		m.back = false
	}
	if ev != nil {
		var fx []core.Effect
		m.core, fx = core.Reduce(m.core, ev)
		for _, f := range fx {
			cmds = append(cmds, m.effect(f, now))
		}
	}
	cmds = append(cmds, m.screen(), m.print(now), m.tick(now), m.stop())
	return m, tea.Batch(cmds...)
}

// stop ends the program once it is quitting and nothing is left to
// print: lines the transcript view held back would be lost otherwise.
func (m *Model) stop() tea.Cmd {
	if !m.quitting || m.stopping || m.alt || m.back || m.printing {
		return nil
	}
	m.stopping = true
	conn := m.conn
	return func() tea.Msg {
		conn.Shutdown()
		return tea.Quit()
	}
}

// screen moves to the alternate screen once the transcript view is open
// and no print is in flight, and back when it closes. Leaving, it waits
// readyDelay for the inline frame to be drawn before anything prints.
func (m *Model) screen() tea.Cmd {
	switch open := m.core.Transcript.Open && len(m.core.Asks) == 0; {
	case open && !m.alt && !m.printing:
		m.alt = true
	case !open && m.alt:
		m.alt, m.back = false, true
		return tea.Tick(readyDelay, func(time.Time) tea.Msg { return backMsg{} })
	}
	return nil
}

// wheel scrolls the transcript view three lines at a time.
func (m *Model) wheel(w tea.MouseWheelMsg, now time.Time) core.Event {
	a := core.TranscriptPageUp
	switch w.Button {
	case tea.MouseWheelUp:
	case tea.MouseWheelDown:
		a = core.TranscriptPageDown
	default:
		return nil
	}
	return core.TranscriptKey{At: now, Action: a, Max: m.transcriptFrame(now).Max, Page: 3}
}

// key turns a key press into an event, or passes it to the input box.
func (m *Model) key(k tea.KeyPressMsg, now time.Time) (core.Event, tea.Cmd) {
	if len(m.core.Asks) > 0 {
		return askKey(ClassifyAsk(k), now), nil
	}
	empty := m.input.Value() == ""
	if m.core.Help {
		m.core, _ = core.Reduce(m.core, core.ToggleHelp{At: now})
		if closesHelp(k) {
			return nil, nil
		}
	}
	if m.core.Transcript.Open {
		return m.transcriptKey(k, empty, now), nil
	}
	if m.core.Pane.Focus {
		return m.paneKey(k, empty, now), nil
	}
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
	case ShowThinking:
		return core.ShowThinking{At: now}, nil
	case Help:
		return core.ToggleHelp{At: now}, nil
	case TrustPaste:
		return core.ToggleTrustedPaste{At: now}, nil
	case TogglePane:
		return core.PaneKey{At: now, Action: core.PaneToggle}, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return nil, cmd
}

// transcriptKey is the event for a key pressed in the transcript view.
// Ctrl-C stops a running turn and, otherwise, leaves the view; Ctrl-D
// quits only from an empty input.
func (m *Model) transcriptKey(k tea.KeyPressMsg, empty bool, now time.Time) core.Event {
	a, move := ClassifyTranscript(k, empty)
	switch a {
	case Move:
		tf := m.transcriptFrame(now)
		return core.TranscriptKey{At: now, Action: move, Max: tf.Max, Page: tf.Page}
	case ShowTools:
		return core.ShowTools{At: now}
	case ShowThinking:
		return core.ShowThinking{At: now}
	case Help:
		return core.ToggleHelp{At: now}
	case Interrupt:
		if m.core.Phase == core.Busy {
			return core.Interrupt{At: now, InputEmpty: true}
		}
		return core.TranscriptKey{At: now, Action: core.TranscriptClose}
	case EOF:
		return core.EOF{At: now}
	}
	return nil
}

// paneKey is the event for a key pressed while the workspace pane has
// focus. The input box never sees it. Ctrl-C still stops a running turn
// and, otherwise, leaves the pane; Ctrl-D quits only from an empty input.
func (m *Model) paneKey(k tea.KeyPressMsg, empty bool, now time.Time) core.Event {
	switch k.String() {
	case "ctrl+c":
		if m.core.Phase == core.Busy {
			return core.Interrupt{At: now, InputEmpty: true}
		}
		return core.PaneKey{At: now, Action: core.PaneToggle}
	case "ctrl+d":
		if empty {
			return core.EOF{At: now}
		}
		return nil
	}
	a := ClassifyPane(k)
	e := core.PaneKey{At: now, Action: a}
	if m.core.Pane.Detail {
		e.Max = m.frame(now).DetailMax
	}
	return e
}

// askKey is the event for a key pressed while a permission request waits.
func askKey(a AskAction, now time.Time) core.Event {
	switch a {
	case AskUp:
		return core.PermissionMove{At: now, Delta: -1}
	case AskDown:
		return core.PermissionMove{At: now, Delta: 1}
	case AskConfirm:
		return core.PermissionConfirm{At: now}
	case AskAllow:
		return core.PermissionQuick{At: now, Kind: core.AllowOnce}
	case AskReject:
		return core.PermissionQuick{At: now, Kind: core.RejectOnce}
	case AskCancel:
		return core.PermissionCancel{At: now}
	case AskShowAll:
		return core.PermissionShowAll{At: now}
	case AskInterrupt:
		return core.Interrupt{At: now, InputEmpty: true}
	case AskEOF:
		return core.EOF{At: now}
	}
	return nil
}

// effect carries out one effect of Reduce.
func (m *Model) effect(f core.Effect, now time.Time) tea.Cmd {
	switch f := f.(type) {
	case core.SendPrompt:
		m.conn.Prompt(f.Text, f.Attachments)
	case core.SendCancel:
		m.conn.Cancel()
	case core.AnswerPermission:
		m.conn.Answer(f)
	case core.RequestItems:
		m.conn.Items()
	case core.RequestResolve:
		m.conn.Resolve(f.Ref)
	case core.SendTrust:
		m.conn.Trust(f.Path, f.Version, f.SHA256)
	case core.InsertInput:
		m.input.InsertString(f.Text)
	case core.RestoreInput:
		text := f.Text
		if v := m.input.Value(); v != "" {
			text += "\n" + v
		}
		m.input.SetValue(text)
	case core.Quit:
		m.quitting = true // stop ends the program
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
	if !m.core.Ready || m.printing || m.alt || m.back || m.core.Transcript.Open {
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
func (m *Model) tick(now time.Time) tea.Cmd {
	if m.ticking || (m.core.Phase != core.Busy && m.core.Phase != core.Starting && !m.trustArming(now)) {
		return nil
	}
	m.ticking = true
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// trustArming says a trust confirmation is waiting out its delay, so the
// pane must redraw when it arms.
func (m *Model) trustArming(now time.Time) bool {
	t := m.core.Pane.Trust
	return t != nil && !t.Sent && now.Sub(t.Since) < core.ArmDelay+tickEvery
}

// width is the width everything is drawn at: one column narrower than
// the screen, so a wide grapheme measured differently cannot wrap (Bubble
// Tea #1802), and printed boxes line up with the input box.
func (m Model) width() int { return max(m.w-1, 1) }

func (m Model) frame(now time.Time) render.Frame {
	return render.View(m.core, m.input.View(), NewlineHint(m.caps), now, m.width(), max(m.h, 2))
}

func (m Model) transcriptFrame(now time.Time) render.TranscriptFrame {
	return render.TranscriptView(m.core, now, m.width(), max(m.h, 2))
}

func (m Model) View() tea.View {
	if m.alt {
		v := tea.NewView(m.transcriptFrame(time.Now()).Content)
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}
	f := m.frame(time.Now())
	v := tea.NewView(f.Content)
	if c := m.input.Cursor(); c != nil && len(m.core.Asks) == 0 && !m.core.Pane.Focus && !m.core.Help {
		c.Position.X += f.InputX
		c.Position.Y += f.InputY
		v.Cursor = c
	}
	return v
}
