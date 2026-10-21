package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kirahowe/acp-tui/internal/core"
	"github.com/kirahowe/acp-tui/internal/render"
)

// Hub mode: the agent is relay's hub, and the program is the hub's whole
// screen. This file is the shell for it. As in the single-session mode,
// Update turns a message into core events, runs core.ReduceHub, and turns
// the effects into commands; everything drawn comes from render.HubView.

func (m Model) updateHub(msg tea.Msg) (tea.Model, tea.Cmd) {
	now := time.Now()
	var evs []core.Event
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.MaxHeight = max(m.h/2-2, 1)
		m.sized = true
	case tea.KeyboardEnhancementsMsg:
		m.caps.Disambiguation = msg.SupportsKeyDisambiguation()
	case tea.KeyPressMsg:
		var cmd tea.Cmd
		evs, cmd = m.hubKey(msg, now)
		cmds = append(cmds, cmd)
	case tea.MouseWheelMsg:
		evs = m.hubWheel(msg, now)
	case tea.PasteMsg:
		evs = []core.Event{core.Pasted{At: now, Text: msg.Content}} // the core drops it when the input is not in play
	case EventMsg:
		evs = []core.Event{msg.Event}
		if _, ok := msg.Event.(core.AgentExited); ok && !m.quitting {
			m.died = true
		}
	case tickMsg:
		m.ticking = false
		evs = []core.Event{core.Tick{At: time.Time(msg)}}
	}
	before := m.hub.Focus
	for _, e := range evs {
		var fx []core.Effect
		m.hub, fx = core.ReduceHub(m.hub, e)
		for _, f := range fx {
			m.hubEffect(f)
		}
	}
	m.swapDraft(before)
	// The text area is the inside of the box drawn, whatever the layout is
	// now: the pane beside it, the sidebar, a narrow screen.
	m.input.SetWidth(render.HubInputWidth(m.hub, m.width()))
	if c := m.hub.Focused(); m.hub.TooSmall || (c != nil && len(c.State.Asks) > 0) {
		// Whether the window shows the request whole decides whether the
		// keys that approve do anything.
		if f := m.hubFrame(now); f.TooSmall != m.hub.TooSmall {
			m.hub, _ = core.ReduceHub(m.hub, core.Viewport{At: now, TooSmall: f.TooSmall})
		}
	}
	cmds = append(cmds, m.hubTick(now), m.stop())
	return m, tea.Batch(cmds...)
}

// swapDraft keeps what was typed for the role that was in focus, and
// brings back what was typed for the one that is now.
func (m *Model) swapDraft(before string) {
	after := m.hub.Focus
	if before == after {
		return
	}
	if m.drafts == nil {
		m.drafts = map[string]string{}
	}
	if before != "" {
		m.drafts[before] = m.input.Value()
	}
	m.input.SetValue(m.drafts[after])
	delete(m.drafts, after)
}

// hubEffect carries out one effect of ReduceHub.
func (m *Model) hubEffect(f core.Effect) {
	switch f := f.(type) {
	case core.ForSession:
		m.sessionEffect(f.Session, f.Effect)
	case core.Attach:
		m.conn.Attach(f.Role, f.Session)
	case core.EndConversation:
		m.conn.End(f.Session)
	case core.SendSeen:
		m.conn.Seen(f.Session, f.Turn)
	default:
		m.effect(f, time.Now())
	}
}

// focusedState is the conversation in focus, or the zero State.
func (m Model) focusedState() core.State {
	if c := m.hub.Focused(); c != nil && !m.hub.List {
		return c.State
	}
	return core.State{}
}

// hubKey turns a key press into events. Moving between conversations
// works whatever else is going on, even while a request waits.
func (m *Model) hubKey(k tea.KeyPressMsg, now time.Time) ([]core.Event, tea.Cmd) {
	if m.hub.Help {
		// Any key closes the list of keys; all but Esc and ? then do what
		// they do.
		evs := []core.Event{core.ToggleHelp{At: now}}
		if closesHelp(k) {
			return evs, nil
		}
		more, cmd := m.hubKeyBody(k, now)
		return append(evs, more...), cmd
	}
	return m.hubKeyBody(k, now)
}

func (m *Model) hubKeyBody(k tea.KeyPressMsg, now time.Time) ([]core.Event, tea.Cmd) {
	one := func(e core.Event) ([]core.Event, tea.Cmd) { return []core.Event{e}, nil }
	if m.hub.EndAsk {
		if answered, yes := ClassifyEnd(k); answered {
			return one(core.ConfirmEnd{At: now, Yes: yes})
		}
		return nil, nil
	}
	a, index := ClassifyHub(k)
	switch a {
	case HubNext, HubPrev:
		d := 1
		if a == HubPrev {
			d = -1
		}
		if m.hub.List {
			return one(core.ListMove{At: now, Delta: d})
		}
		return one(core.FocusMove{At: now, Delta: d})
	case HubIndex:
		return one(core.FocusIndex{At: now, Index: index})
	case HubLast:
		return one(core.FocusLast{At: now})
	case HubList:
		return one(core.ToggleList{At: now})
	case HubEnd:
		return one(core.AskEnd{At: now})
	}
	if m.hub.List {
		switch ClassifyList(k) {
		case ListUp:
			return one(core.ListMove{At: now, Delta: -1})
		case ListDown:
			return one(core.ListMove{At: now, Delta: 1})
		case ListOpen:
			return one(core.ListOpen{At: now})
		case ListBack:
			return one(core.ToggleList{At: now})
		case ListHelp:
			return one(core.ToggleHelp{At: now})
		case ListInterrupt:
			return one(core.Interrupt{At: now, InputEmpty: true})
		case ListEOF:
			return one(core.EOF{At: now})
		}
		return nil, nil
	}
	st := m.focusedState()
	// Scrolling is never the request's: a long one is read by scrolling to
	// what `v` put in the transcript. Only the approval keys are its. The
	// workspace pane, when it has the focus, scrolls itself with these.
	if !st.Pane.Focus {
		switch k.String() {
		case "pgup":
			return one(m.scroll(core.TranscriptPageUp, now))
		case "pgdown":
			return one(m.scroll(core.TranscriptPageDown, now))
		}
	}
	if len(st.Asks) > 0 {
		if e := askKey(ClassifyAsk(k), now); e != nil {
			return one(e)
		}
		return nil, nil
	}
	empty := m.input.Value() == ""
	if st.Pane.Focus {
		if e := paneEvent(st, k, empty, now, func() int { return m.hubFrame(now).DetailMax }); e != nil {
			return one(e)
		}
		return nil, nil
	}
	switch Classify(k, empty) {
	case Send:
		text := m.input.Value()
		if isBlank(text) {
			if m.hub.Focus != "" && m.hub.Focused() == nil {
				return one(core.StartConversation{At: now})
			}
			return nil, nil
		}
		m.input.Reset()
		return one(core.Submit{At: now, Text: text})
	case Newline:
		m.input.InsertString("\n")
		return nil, nil
	case Interrupt:
		if (m.hub.Focused() == nil || st.Phase == core.Idle) && !empty {
			m.input.Reset() // like a shell: Ctrl-C clears the line
		}
		return one(core.Interrupt{At: now, InputEmpty: empty})
	case EOF:
		return one(core.EOF{At: now})
	case ShowTools:
		return one(core.ShowTools{At: now})
	case ShowThinking:
		return one(core.ShowThinking{At: now})
	case Help:
		return one(core.ToggleHelp{At: now})
	case TrustPaste:
		return one(core.TrustPaste{At: now, Draft: m.input.Value()})
	case TogglePane:
		return one(core.PaneKey{At: now, Action: core.PaneToggle})
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return nil, cmd
}

func isBlank(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

// scroll is the event that moves the conversation as drawn.
func (m *Model) scroll(a core.TranscriptAction, now time.Time) core.Event {
	f := m.hubFrame(now)
	page := f.Page
	return core.TranscriptKey{At: now, Action: a, Max: f.Max, Page: page}
}

// hubWheel scrolls the conversation three lines at a time.
func (m *Model) hubWheel(w tea.MouseWheelMsg, now time.Time) []core.Event {
	a := core.TranscriptPageUp
	switch w.Button {
	case tea.MouseWheelUp:
	case tea.MouseWheelDown:
		a = core.TranscriptPageDown
	default:
		return nil
	}
	return []core.Event{core.TranscriptKey{At: now, Action: a, Max: m.hubFrame(now).Max, Page: 3}}
}

// hubTick keeps the status line moving while a turn runs in the
// conversation on screen, and is quiet otherwise.
func (m *Model) hubTick(now time.Time) tea.Cmd {
	if m.ticking || m.hub.Gone {
		return nil
	}
	busy := false
	if c := m.hub.Focused(); c != nil && !m.hub.List {
		st := c.State
		busy = st.Phase == core.Busy || st.Phase == core.Starting
		if r := m.hub.RoleByID(m.hub.Focus); r != nil && r.Conv != nil && r.Conv.Turn == core.TurnRunning {
			busy = true
		}
		if t := st.Pane.Trust; t != nil && !t.Sent && now.Sub(t.Since) < core.ArmDelay+tickEvery {
			busy = true
		}
	}
	if !busy {
		return nil
	}
	m.ticking = true
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) hubFrame(now time.Time) render.HubFrame {
	return render.HubView(m.hub, m.input.View(), NewlineHint(m.caps), now, m.width(), max(m.h, 2))
}

func (m Model) hubView(now time.Time) tea.View {
	f := m.hubFrame(now)
	v := tea.NewView(f.Content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	if f.Cursor && !m.hub.List && !m.hub.Help && !m.hub.EndAsk && m.hub.Focus != "" {
		if c := m.input.Cursor(); c != nil {
			c.Position.X += f.InputX
			c.Position.Y += f.InputY
			v.Cursor = c
		}
	}
	return v
}
