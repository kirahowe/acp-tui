package core

import (
	"slices"
	"strings"
	"time"
)

// Hub mode: relay's hub answers one ACP connection with the whole roster
// of roles and a live conversation per role. Hub is the top-level state
// then. It holds the roster as relay last sent it, one State per
// conversation attached, and which role is in focus. ReduceHub is pure,
// as Reduce is: it applies an event to the hub, hands events for one
// conversation to Reduce, and returns effects for the shell.
//
// Nothing here is true of its own accord. The roster is relay's, and
// replaces itself whole whenever relay sends it. A conversation's
// entries are what its updates said, in the order they came, whether
// they were replayed or live.

// TurnState is where a conversation's turn stands, as the roster says.
type TurnState string

const (
	TurnIdle    TurnState = "idle"
	TurnRunning TurnState = "running"
	// TurnQueued means a prompt waits for the role's scheduled work to
	// finish: a role runs one turn at a time.
	TurnQueued TurnState = "queued"
)

// Role is one row of the roster.
type Role struct {
	ID, Title string
	Labels    Labels
	// Working says a scheduled wake of the role runs.
	Working bool
	// Holds is how many of the role's calls wait for the operator.
	Holds int
	// Conv is the role's live conversation, or nil.
	Conv *RosterConv
}

// RosterConv is the roster's account of a live conversation.
type RosterConv struct {
	Session                string
	OpenedAt, LastActivity time.Time
	Turn                   TurnState
	Turns                  int
	// Unread says a turn ended after the last one the operator saw.
	Unread bool
	// Approvals is how many permission requests are asked and not
	// answered.
	Approvals int
}

// Conv is a conversation this client has attached to, or is attaching
// to. Session is empty while a new one is being opened and relay has not
// yet said its id.
type Conv struct {
	Session, Role string
	// Attached says the attach has been answered.
	Attached bool
	// InRoster says the roster has listed the session. A listed session
	// the roster stops listing has ended.
	InRoster bool
	// Loaded says relay has finished the replay (_relay/replayed).
	Loaded bool
	// Closed says what was left open by the replay has been closed, once
	// the roster reported the conversation idle.
	Closed bool
	// SeenSent is the highest turn this client has told relay it saw.
	SeenSent int
	State    State
}

// Hub is the whole client state in hub mode.
type Hub struct {
	// Open is the role named by --open, focused at the start.
	Open string
	// Connected says initialize has answered; Agent, Title and Version
	// come from agentInfo.
	Connected             bool
	Agent, Title, Version string
	Embedded              bool
	Roles                 []Role
	RosterLoaded          bool
	Convs                 []Conv
	// Focus is the role whose conversation is on screen ("" for none).
	// Last is the role before it. Cursor is the role the list has
	// selected. List says the list has the keys: on a narrow screen it
	// is the screen.
	Focus, Last, Cursor string
	List                bool
	// EndAsk says the operator is being asked to confirm ending Focus's
	// conversation. Ending says the request is out.
	EndAsk, Ending bool
	// Ended are the sessions this client ended, so a roster that still
	// lists one is not taken for a new conversation.
	Ended []string
	// Notice is a note for the status line, cleared by the next key.
	Notice string
	Help   bool
	// Exit is why the connection went away, once Gone.
	Exit string
	Gone bool
}

// NewHub is the state at start: open names the role to focus, or "".
func NewHub(open string) Hub {
	return Hub{Open: open, List: open == ""}
}

// RoleByID is the role with this id, or nil.
func (h Hub) RoleByID(id string) *Role {
	for i := range h.Roles {
		if h.Roles[i].ID == id {
			return &h.Roles[i]
		}
	}
	return nil
}

// ConvOf is the conversation attached for a role, or nil. A conversation
// whose role is not yet known counts when the roster lists its session
// under the role.
func (h Hub) ConvOf(role string) *Conv {
	for i := range h.Convs {
		if h.Convs[i].Role == role {
			return &h.Convs[i]
		}
	}
	if r := h.RoleByID(role); r != nil && r.Conv != nil {
		for i := range h.Convs {
			if h.Convs[i].Role == "" && h.Convs[i].Session == r.Conv.Session {
				return &h.Convs[i]
			}
		}
	}
	return nil
}

// Focused is the conversation on screen, or nil.
func (h Hub) Focused() *Conv {
	if h.Focus == "" {
		return nil
	}
	return h.ConvOf(h.Focus)
}

// ConvBySession is the conversation with this session id, or nil.
func (h Hub) ConvBySession(session string) *Conv {
	if session == "" {
		return nil
	}
	for i := range h.Convs {
		if h.Convs[i].Session == session {
			return &h.Convs[i]
		}
	}
	return nil
}

// Approvals is how many requests wait in a role's conversation: what
// relay counts, or, if more, what this client holds.
func (h Hub) Approvals(r Role) int {
	n := 0
	if r.Conv != nil {
		n = r.Conv.Approvals
	}
	if c := h.ConvOf(r.ID); c != nil {
		n = max(n, len(c.State.Asks))
	}
	return n
}

// Events from the hub's connection, translated by package wire.
type (
	// HubConnected means initialize answered with _meta["relay/hub"].
	HubConnected struct {
		At                    time.Time
		Agent, Title, Version string
		Embedded              bool
	}
	// RosterUpdate is the whole roster, from _relay/roles or
	// _relay/roles/changed. Err is set when the roster could not be had.
	RosterUpdate struct {
		At    time.Time
		Roles []Role
		Err   string
	}
	// Routed is an event for the conversation with this session id.
	Routed struct {
		Session string
		Event   Event
	}
	// Attached is the answer to an attach. Session is the conversation's
	// id; Role the role it was asked for.
	Attached struct {
		At            time.Time
		Role, Session string
		Err           string
	}
	// PermissionSettled is _relay/permission/settled: another device
	// answered the request for CallID.
	PermissionSettled struct {
		At                       time.Time
		Session, CallID, Outcome string
	}
	// Replayed is _relay/replayed: the replay of a conversation is over.
	Replayed struct {
		At      time.Time
		Session string
	}
	// ConversationEnded is the answer to ending a conversation.
	ConversationEnded struct {
		At      time.Time
		Session string
		Err     string
	}
)

// Events from the user in hub mode. Events of the single-session kind
// (Submit, Interrupt, PaneKey and so on) apply to the conversation in
// focus.
type (
	FocusMove struct {
		At    time.Time
		Delta int
	} // ctrl+n, ctrl+p
	FocusIndex struct {
		At    time.Time
		Index int
	} // alt+1 is Index 0
	FocusLast  struct{ At time.Time } // ctrl+l
	ToggleList struct{ At time.Time } // ctrl+g
	ListMove   struct {
		At    time.Time
		Delta int
	}
	ListOpen          struct{ At time.Time }
	StartConversation struct{ At time.Time }
	AskEnd            struct{ At time.Time } // ctrl+s
	ConfirmEnd        struct {
		At  time.Time
		Yes bool
	}
)

func (e HubConnected) at() time.Time      { return e.At }
func (e RosterUpdate) at() time.Time      { return e.At }
func (e Routed) at() time.Time            { return e.Event.at() }
func (e Attached) at() time.Time          { return e.At }
func (e PermissionSettled) at() time.Time { return e.At }
func (e Replayed) at() time.Time          { return e.At }
func (e ConversationEnded) at() time.Time { return e.At }
func (e FocusMove) at() time.Time         { return e.At }
func (e FocusIndex) at() time.Time        { return e.At }
func (e FocusLast) at() time.Time         { return e.At }
func (e ToggleList) at() time.Time        { return e.At }
func (e ListMove) at() time.Time          { return e.At }
func (e ListOpen) at() time.Time          { return e.At }
func (e StartConversation) at() time.Time { return e.At }
func (e AskEnd) at() time.Time            { return e.At }
func (e ConfirmEnd) at() time.Time        { return e.At }

// Effects for the hub's shell.
type (
	// ForSession is an effect of Reduce for the conversation with this
	// session id.
	ForSession struct {
		Session string
		Effect  Effect
	}
	// Attach opens a role's conversation: session/load of Session when
	// relay listed one, else session/new for Role.
	Attach struct{ Role, Session string }
	// EndConversation asks relay to end the conversation.
	EndConversation struct{ Session string }
	// SendSeen tells relay the operator has seen the conversation
	// through Turn.
	SendSeen struct {
		Session string
		Turn    int
	}
)

func (ForSession) effect()      {}
func (Attach) effect()          {}
func (EndConversation) effect() {}
func (SendSeen) effect()        {}

// ReduceHub applies one event. It never mutates h's slices in place.
func ReduceHub(h Hub, e Event) (Hub, []Effect) {
	h.Roles = slices.Clone(h.Roles)
	h.Convs = slices.Clone(h.Convs)
	h.Ended = slices.Clone(h.Ended)
	r := &hubReducer{h: h}
	r.apply(e)
	r.markSeen(e.at())
	return r.h, r.fx
}

type hubReducer struct {
	h  Hub
	fx []Effect
}

func (r *hubReducer) emit(f Effect) { r.fx = append(r.fx, f) }

// convIndex is where the conversation for a role is in h.Convs, or -1.
func (r *hubReducer) convIndex(role string) int {
	c := r.h.ConvOf(role)
	if c == nil {
		return -1
	}
	return slices.IndexFunc(r.h.Convs, func(x Conv) bool { return x.Session == c.Session && x.Role == c.Role })
}

func (r *hubReducer) sessionIndex(session string) int {
	if session == "" {
		return -1
	}
	return slices.IndexFunc(r.h.Convs, func(x Conv) bool { return x.Session == session })
}

func newConv(session, role string) Conv {
	return Conv{Session: session, Role: role, State: State{Shared: true, Transcript: Transcript{Open: true}}}
}

func (r *hubReducer) apply(e Event) {
	h := &r.h
	switch e.(type) {
	case Tick, TerminalReady, Printed, HubConnected, RosterUpdate, Routed, Attached, PermissionSettled, Replayed,
		ConversationEnded, CardsChanged, AgentExited:
	default:
		h.Notice = "" // a key was pressed
	}
	switch e := e.(type) {
	case HubConnected:
		h.Connected = true
		h.Agent, h.Title, h.Version, h.Embedded = e.Agent, e.Title, e.Version, e.Embedded
		if h.Open != "" {
			r.focus(h.Open, true, e.At)
		}
	case RosterUpdate:
		r.roster(e)
	case Routed:
		r.routed(e)
	case CardsChanged:
		for i := range h.Convs {
			r.toConv(i, e, false)
		}
	case Attached:
		r.attached(e)
	case Replayed:
		if i := r.sessionIndex(e.Session); i >= 0 {
			h.Convs[i].Loaded = true
			// A call still open when the replay ends is running if the
			// turn is (it was started on another device and its update will
			// arrive live). It stops only when the roster says no turn runs;
			// before the roster has come, the roster going idle closes it.
			if r.turnIdle(h.Convs[i]) {
				r.toConv(i, ReplayEnd{At: e.At}, false)
			}
		}
	case PermissionSettled:
		if i := r.sessionIndex(e.Session); i >= 0 {
			r.toConv(i, Settled{At: e.At, CallID: e.CallID, Outcome: e.Outcome}, false)
		}
	case ConversationEnded:
		r.ended(e)
	case AgentExited:
		if h.Gone {
			return
		}
		h.Gone, h.Exit = true, e.Err
		for i := range h.Convs {
			r.toConv(i, e, false)
		}
		r.emit(Quit{})
	case Tick, TerminalReady, Printed:
	case FocusMove:
		r.move(e.Delta, e.At)
	case FocusIndex:
		if e.Index >= 0 && e.Index < len(h.Roles) {
			r.focus(h.Roles[e.Index].ID, false, e.At)
		}
	case FocusLast:
		if h.Last != "" && h.RoleByID(h.Last) != nil {
			r.focus(h.Last, false, e.At)
		}
	case ToggleList:
		if h.List {
			if h.Focus != "" {
				h.List = false
			}
			return
		}
		h.List = true
		h.Cursor = h.Focus
		r.clampCursor()
	case ListMove:
		if h.List {
			r.moveCursor(e.Delta)
		}
	case ListOpen:
		if h.List && h.RoleByID(h.Cursor) != nil {
			r.focus(h.Cursor, false, e.At)
		}
	case StartConversation:
		if h.Focus != "" && h.Focused() == nil && !h.List {
			r.ensureAttached(h.Focus, true)
		}
	case AskEnd:
		if c := h.Focused(); c != nil && c.Attached && !h.Ending && !h.List {
			h.EndAsk = true
		}
	case ConfirmEnd:
		if !h.EndAsk {
			return
		}
		h.EndAsk = false
		if c := h.Focused(); e.Yes && c != nil && c.Session != "" {
			h.Ending = true
			r.emit(EndConversation{Session: c.Session})
		}
	case ToggleHelp:
		h.Help = !h.Help
	case Submit:
		if strings.TrimSpace(e.Text) == "" || h.Focus == "" || h.List {
			return
		}
		if h.Focused() == nil {
			r.ensureAttached(h.Focus, true)
		}
		r.toFocused(e)
	case Interrupt:
		r.interrupt(e)
	case EOF:
		for i := range h.Convs {
			r.cancelAsks(i)
		}
		r.emit(Quit{})
	default:
		r.toFocused(e)
	}
}

// toFocused applies a single-session event to the conversation in focus.
func (r *hubReducer) toFocused(e Event) {
	if r.h.List {
		return
	}
	if i := r.convIndex(r.h.Focus); i >= 0 {
		r.toConv(i, e, true)
	}
}

// toConv reduces one conversation and maps its effects. user says the
// event came from the operator, so Quit means quit.
func (r *hubReducer) toConv(i int, e Event, user bool) {
	c := &r.h.Convs[i]
	var fx []Effect
	c.State, fx = Reduce(c.State, e)
	for _, f := range fx {
		r.mapEffect(*c, f, user)
	}
}

func (r *hubReducer) mapEffect(c Conv, f Effect, user bool) {
	switch f := f.(type) {
	case AnswerPermission:
		r.emit(f)
	case InsertInput, RestoreInput:
		if c.Role == r.h.Focus && c.Role != "" {
			r.emit(f)
		} else if ri, ok := f.(RestoreInput); ok {
			// Not the conversation on screen, so there is no input to put
			// it in. It stays where the operator will find it.
			i := slices.IndexFunc(r.h.Convs, func(x Conv) bool { return x.Session == c.Session && x.Role == c.Role })
			r.h.Convs[i].State.Entries = append(slices.Clone(r.h.Convs[i].State.Entries),
				Entry{Kind: Note, Text: "not sent: " + oneLineText(ri.Text), Final: true})
		}
	case Quit:
		if user {
			r.emit(f)
		}
	default:
		r.emit(ForSession{Session: c.Session, Effect: f})
	}
}

// oneLineText is s on one line, for a note.
func oneLineText(s string) string { return strings.Join(strings.Fields(s), " ") }

func (r *hubReducer) cancelAsks(i int) {
	c := &r.h.Convs[i]
	var fx []Effect
	rd := &reducer{s: c.State}
	rd.cancelAsks()
	c.State, fx = rd.s, rd.fx
	for _, f := range fx {
		r.mapEffect(*c, f, false)
	}
}

// interrupt is Ctrl-C: it stops what is running in the conversation in
// focus, here or on another device, and otherwise quits.
func (r *hubReducer) interrupt(e Interrupt) {
	h := &r.h
	c := h.Focused()
	if c == nil && !h.List && !e.InputEmpty {
		return // Ctrl-C clears the line; there is nothing else to stop
	}
	if c == nil || h.List {
		for i := range h.Convs {
			r.cancelAsks(i)
		}
		r.emit(Quit{})
		return
	}
	if role := h.RoleByID(h.Focus); c.State.Phase == Idle && e.InputEmpty && role != nil && role.Conv != nil &&
		role.Conv.Session == c.Session && role.Conv.Turn != TurnIdle && role.Conv.Turn != "" {
		// A turn another device started, or a prompt waiting its turn.
		r.emit(ForSession{Session: c.Session, Effect: SendCancel{}})
		return
	}
	r.toFocused(e)
}

// move focuses the role delta places from the one in focus, wrapping.
func (r *hubReducer) move(delta int, at time.Time) {
	h := &r.h
	n := len(h.Roles)
	if n == 0 || delta == 0 {
		return
	}
	from := slices.IndexFunc(h.Roles, func(x Role) bool { return x.ID == h.Focus })
	if from < 0 {
		from = slices.IndexFunc(h.Roles, func(x Role) bool { return x.ID == h.Cursor })
		if from < 0 {
			from = 0
			if delta > 0 {
				delta--
			}
		}
	}
	to := ((from+delta)%n + n) % n
	r.focus(h.Roles[to].ID, false, at)
}

func (r *hubReducer) moveCursor(delta int) {
	h := &r.h
	n := len(h.Roles)
	if n == 0 {
		return
	}
	from := slices.IndexFunc(h.Roles, func(x Role) bool { return x.ID == h.Cursor })
	if from < 0 {
		from = 0
		delta = 0
	}
	h.Cursor = h.Roles[((from+delta)%n+n)%n].ID
}

func (r *hubReducer) clampCursor() {
	h := &r.h
	if h.RoleByID(h.Cursor) == nil {
		h.Cursor = ""
		if len(h.Roles) > 0 {
			h.Cursor = h.Roles[0].ID
		}
	}
}

// focus brings a role's conversation on screen, attaching to it when
// relay lists one. force opens one when there is none.
func (r *hubReducer) focus(role string, force bool, at time.Time) {
	h := &r.h
	if h.Focus != role && h.Focus != "" {
		h.Last = h.Focus
	}
	h.Focus, h.Cursor, h.List = role, role, false
	h.EndAsk, h.Ending = false, false
	r.ensureAttached(role, force)
	if i := r.convIndex(role); i >= 0 {
		r.toConv(i, Focused{At: at}, false)
	}
}

// ensureAttached makes sure a conversation is attached or attaching for
// the role: a live one in the roster, or, with force, a new one.
func (r *hubReducer) ensureAttached(role string, force bool) {
	h := &r.h
	if h.ConvOf(role) != nil {
		return
	}
	if rl := h.RoleByID(role); rl != nil && rl.Conv != nil && !slices.Contains(h.Ended, rl.Conv.Session) {
		h.Convs = append(h.Convs, newConv(rl.Conv.Session, role))
		r.emit(Attach{Role: role, Session: rl.Conv.Session})
		return
	}
	if force {
		h.Convs = append(h.Convs, newConv("", role))
		r.emit(Attach{Role: role})
	}
}

// routed hands an event to the conversation it names, which a replay may
// meet before its attach has been answered.
func (r *hubReducer) routed(e Routed) {
	i := r.sessionIndex(e.Session)
	if i < 0 {
		if e.Session == "" || slices.Contains(r.h.Ended, e.Session) {
			return
		}
		r.h.Convs = append(r.h.Convs, newConv(e.Session, ""))
		i = len(r.h.Convs) - 1
	}
	// More of the conversation has come in, so whatever the roster said
	// about it closing is out of date until the roster says it again.
	r.h.Convs[i].Closed = false
	r.toConv(i, e.Event, false)
}

func (r *hubReducer) attached(e Attached) {
	h := &r.h
	if e.Err != "" {
		i := slices.IndexFunc(h.Convs, func(x Conv) bool {
			return x.Role == e.Role && !x.Attached && (x.Session == "" || x.Session == e.Session)
		})
		if i >= 0 {
			q := h.Convs[i].State.Queue
			h.Convs = slices.Delete(h.Convs, i, i+1)
			if len(q) > 0 && h.Focus == e.Role {
				r.emit(RestoreInput{Text: strings.Join(q, "\n\n")})
			}
		}
		if e.Session != "" {
			// Not live any more, whatever a roster still in flight says.
			h.Ended = append(h.Ended, e.Session)
		}
		h.Notice = "could not open the conversation: " + oneLineText(e.Err)
		return
	}
	if slices.Contains(h.Ended, e.Session) {
		return
	}
	byRole := slices.IndexFunc(h.Convs, func(x Conv) bool { return x.Role == e.Role && x.Session == "" })
	bySession := r.sessionIndex(e.Session)
	switch {
	case bySession >= 0 && byRole >= 0:
		// The replay got here first and made a conversation of its own;
		// the one that was waiting contributes its queue.
		h.Convs[bySession].State.Queue = append(slices.Clone(h.Convs[byRole].State.Queue), h.Convs[bySession].State.Queue...)
		h.Convs = slices.Delete(h.Convs, byRole, byRole+1)
		bySession = r.sessionIndex(e.Session)
	case bySession < 0 && byRole >= 0:
		h.Convs[byRole].Session = e.Session
		bySession = byRole
	case bySession < 0:
		h.Convs = append(h.Convs, newConv(e.Session, e.Role))
		bySession = len(h.Convs) - 1
	}
	c := &h.Convs[bySession]
	c.Role, c.Attached = e.Role, true
	r.toConv(bySession, ConvAttached{At: e.At, Embedded: h.Embedded}, false)
	if e.Role == h.Focus {
		r.toConv(bySession, Focused{At: e.At}, false)
	}
}

func (r *hubReducer) ended(e ConversationEnded) {
	h := &r.h
	h.Ending = false
	if e.Err != "" {
		h.Notice = "could not end the conversation: " + oneLineText(e.Err)
		return
	}
	if i := r.sessionIndex(e.Session); i >= 0 {
		h.Convs = slices.Delete(h.Convs, i, i+1)
	}
	h.Ended = append(h.Ended, e.Session)
	h.EndAsk = false
	h.Notice = "conversation ended"
}

// turnIdle says the roster is loaded and lists no running or queued turn
// for the conversation.
func (r *hubReducer) turnIdle(c Conv) bool {
	if !r.h.RosterLoaded {
		return false
	}
	for _, rl := range r.h.Roles {
		if rl.Conv != nil && rl.Conv.Session == c.Session {
			return rl.Conv.Turn != TurnRunning && rl.Conv.Turn != TurnQueued
		}
	}
	return true
}

// roster replaces the roster and settles the conversations against it.
func (r *hubReducer) roster(e RosterUpdate) {
	h := &r.h
	if e.Err != "" {
		h.Notice = "no roster: " + oneLineText(e.Err)
		return
	}
	old := h.Roles
	h.Roles = slices.Clone(e.Roles)
	h.RosterLoaded = true
	r.clampCursor()
	r.reconcile(old)
	if h.Focus != "" && h.RoleByID(h.Focus) == nil {
		h.Notice = "no role " + oneLineText(h.Focus)
		h.Focus, h.List = "", true
	}
}

// reconcile settles the conversations against the roster. old is the
// roster before this update, or nil.
func (r *hubReducer) reconcile(old []Role) {
	h := &r.h
	for i := len(h.Convs) - 1; i >= 0; i-- {
		c := &h.Convs[i]
		if c.Role == "" { // adopt a replay's conversation once a role lists it
			for _, rl := range h.Roles {
				if rl.Conv != nil && rl.Conv.Session == c.Session {
					c.Role = rl.ID
				}
			}
		}
		rl := h.RoleByID(c.Role)
		if rl != nil && rl.Conv != nil && rl.Conv.Session == c.Session && c.Session != "" {
			c.InRoster = true
			continue
		}
		if c.InRoster {
			// Listed before, listed no more: it ended, perhaps on another
			// device.
			if c.Role == h.Focus {
				h.Notice = "the conversation ended"
			}
			h.Convs = slices.Delete(h.Convs, i, i+1)
		}
	}
	// What a conversation left open can be closed once relay says no turn
	// runs: after one ran, or after the attach.
	for i := range h.Convs {
		c := &h.Convs[i]
		rl := h.RoleByID(c.Role)
		if rl == nil || rl.Conv == nil || rl.Conv.Session != c.Session || !c.Attached {
			continue
		}
		was := TurnIdle
		for _, o := range old {
			if o.ID == rl.ID && o.Conv != nil && o.Conv.Session == c.Session {
				was = o.Conv.Turn
			}
		}
		if rl.Conv.Turn == TurnIdle && (!c.Closed || was != TurnIdle) {
			c.Closed = true
			r.toConv(i, RemoteIdle{}, false)
		}
	}
	// A conversation opened elsewhere, in the role on screen.
	if h.Focus != "" && !h.List {
		r.ensureAttached(h.Focus, false)
	}
}

// markSeen tells relay the operator has seen the conversation in focus
// through its latest turn, once that turn is on screen: the conversation
// is attached, shown, scrolled to its end, and no turn runs.
func (r *hubReducer) markSeen(time.Time) {
	h := &r.h
	if h.List || h.Focus == "" {
		return
	}
	i := r.convIndex(h.Focus)
	rl := h.RoleByID(h.Focus)
	if i < 0 || rl == nil || rl.Conv == nil {
		return
	}
	c := &h.Convs[i]
	rc := rl.Conv
	if !c.Attached || c.Session != rc.Session || c.State.Phase == Exited || c.State.Transcript.Scroll != 0 ||
		!rc.Unread || rc.Turn != TurnIdle || rc.Turns <= c.SeenSent {
		return
	}
	c.SeenSent = rc.Turns
	r.emit(SendSeen{Session: c.Session, Turn: rc.Turns})
}
