package core

import (
	"strings"
	"time"
)

// Labels is what the session has seen, as the agent reports it. They only
// grow: a session that has seen private data or untrusted content never
// forgets it, so Union is all the core ever does to them.
type Labels struct{ Private, Contaminated bool }

// Union is the labels of l and o together.
func (l Labels) Union(o Labels) Labels {
	return Labels{Private: l.Private || o.Private, Contaminated: l.Contaminated || o.Contaminated}
}

// Empty reports whether there is no label.
func (l Labels) Empty() bool { return l == Labels{} }

// OptionKind is ACP's permission option kind. Kinds a newer agent might
// send pass through unchanged and are never taken for an allow.
type OptionKind string

const (
	AllowOnce    OptionKind = "allow_once"
	AllowAlways  OptionKind = "allow_always"
	RejectOnce   OptionKind = "reject_once"
	RejectAlways OptionKind = "reject_always"
)

// Allows reports whether choosing an option of this kind lets the call run.
func (k OptionKind) Allows() bool { return k == AllowOnce || k == AllowAlways }

// Rejects reports whether choosing an option of this kind refuses the call.
func (k OptionKind) Rejects() bool { return k == RejectOnce || k == RejectAlways }

// PermOption is one choice the agent offers.
type PermOption struct {
	ID, Name string
	Kind     OptionKind
}

// Permission is a request from the agent to run a tool call. ID is the
// shell's handle for answering it.
type Permission struct {
	ID          uint64
	Name, Title string
	Input       string // the call's arguments as JSON, when the agent sent them
	Options     []PermOption
	Since       time.Time // when it became the one on offer; see ArmDelay
}

// Label is what to call the call: the tool's name, else the title.
func (p Permission) Label() string {
	if strings.TrimSpace(p.Name) != "" {
		return p.Name
	}
	if strings.TrimSpace(p.Title) != "" {
		return p.Title
	}
	return "tool call"
}

// ArmDelay is how long a request is on screen before a key can answer it
// other than by cancelling. The request can appear while the person is
// typing, and a y or an enter meant for the input must not approve a call.
const ArmDelay = 600 * time.Millisecond

// onceOptions keeps the options that last for this one call. The person
// is never offered an allow_always or reject_always, nor a kind this
// client does not know: acp-tui does not grant standing permission.
func onceOptions(opts []PermOption) []PermOption {
	var out []PermOption
	for _, o := range opts {
		if o.Kind == AllowOnce || o.Kind == RejectOnce {
			out = append(out, o)
		}
	}
	return out
}

// DefaultPick is the option that starts highlighted: the safest one, so a
// bare enter refuses. That is the first reject_once, else the first
// reject_always, else the first option.
func DefaultPick(opts []PermOption) int {
	for _, k := range []OptionKind{RejectOnce, RejectAlways} {
		if i := PickKind(opts, k); i >= 0 {
			return i
		}
	}
	return 0
}

// PickKind is the index of the first option of kind k, or -1.
func PickKind(opts []PermOption, k OptionKind) int {
	for i, o := range opts {
		if o.Kind == k {
			return i
		}
	}
	return -1
}

// AnswerPermission is the reply to a request: the chosen option, or
// Cancelled, which ACP uses when the person gave no answer.
type AnswerPermission struct {
	ID        uint64
	OptionID  string
	Cancelled bool
}

// Events about permission requests.
type (
	// PermissionAsked is a session/request_permission from the agent.
	PermissionAsked struct {
		At time.Time
		Permission
	}
	// LabelsSeen is the session's labels as of an update. It can only add
	// to what the core already knows.
	LabelsSeen struct {
		At     time.Time
		Labels Labels
	}
	// PermissionMove moves the highlight over the options of the request
	// on offer.
	PermissionMove struct {
		At    time.Time
		Delta int
	}
	// PermissionConfirm answers with the highlighted option.
	PermissionConfirm struct{ At time.Time }
	// PermissionQuick answers with the first option of a kind (y and n).
	PermissionQuick struct {
		At   time.Time
		Kind OptionKind
	}
	// PermissionCancel answers cancelled (Esc).
	PermissionCancel struct{ At time.Time }
	// PermissionShowAll puts the request's whole input into the transcript.
	PermissionShowAll struct{ At time.Time }
)

func (e PermissionAsked) at() time.Time   { return e.At }
func (e LabelsSeen) at() time.Time        { return e.At }
func (e PermissionMove) at() time.Time    { return e.At }
func (e PermissionConfirm) at() time.Time { return e.At }
func (e PermissionQuick) at() time.Time   { return e.At }
func (e PermissionCancel) at() time.Time  { return e.At }
func (e PermissionShowAll) at() time.Time { return e.At }

func (AnswerPermission) effect() {}

// armed reports whether keys other than cancel may answer the request on
// offer at time at.
func (s *State) armed(at time.Time) bool {
	return len(s.Asks) > 0 && at.Sub(s.Asks[0].Since) >= ArmDelay
}

func (r *reducer) permission(e Event) {
	s := &r.s
	switch e := e.(type) {
	case LabelsSeen:
		s.Labels = s.Labels.Union(e.Labels)
	case PermissionAsked:
		if s.Phase == Exited || (s.Phase == Busy && s.Turn.Cancelling) {
			r.emit(AnswerPermission{ID: e.ID, Cancelled: true})
			return
		}
		if s.Phase == Busy {
			s.Turn.LastEvent = e.At
			s.Turn.Retry = nil
		}
		p := e.Permission
		p.Options = onceOptions(p.Options)
		if len(s.Asks) == 0 {
			p.Since = e.At
			s.Pick = DefaultPick(p.Options)
		}
		s.Asks = append(s.Asks, p)
	case PermissionMove:
		if n := len(s.Asks); n > 0 && len(s.Asks[0].Options) > 0 {
			m := len(s.Asks[0].Options)
			s.Pick = ((s.Pick+e.Delta)%m + m) % m
		}
	case PermissionConfirm:
		if s.armed(e.At) && s.Pick >= 0 && s.Pick < len(s.Asks[0].Options) {
			r.answer(e.At, s.Pick)
		}
	case PermissionQuick:
		if s.armed(e.At) {
			if i := PickKind(s.Asks[0].Options, e.Kind); i >= 0 {
				r.answer(e.At, i)
			}
		}
	case PermissionCancel:
		if len(s.Asks) > 0 {
			r.answer(e.At, -1)
		}
	case PermissionShowAll:
		if len(s.Asks) > 0 {
			p := s.Asks[0]
			r.add(Entry{Kind: Request, Text: p.Label(), Detail: p.Input, Final: true})
		}
	}
}

// answer resolves the request on offer with option i, or cancelled when i
// is -1, notes the outcome in the transcript, and offers the next one.
func (r *reducer) answer(at time.Time, i int) {
	s := &r.s
	p := s.Asks[0]
	s.Asks = s.Asks[1:]
	verb := "cancelled"
	if i >= 0 {
		o := p.Options[i]
		r.emit(AnswerPermission{ID: p.ID, OptionID: o.ID})
		switch {
		case o.Kind.Allows():
			verb = "allowed"
		case o.Kind.Rejects():
			verb = "rejected"
		default:
			verb = "chose " + EscapeLabel(o.Name) + " for"
		}
	} else {
		r.emit(AnswerPermission{ID: p.ID, Cancelled: true})
	}
	r.add(Entry{Kind: Note, Text: verb + ": " + EscapeLabel(p.Label()), Final: true})
	r.next(at)
}

// next starts the clock for the request now on offer, if any.
func (r *reducer) next(at time.Time) {
	if len(r.s.Asks) > 0 {
		r.s.Asks[0].Since = at
		r.s.Pick = DefaultPick(r.s.Asks[0].Options)
	}
}

// cancelAsks answers every pending request cancelled, as ACP requires
// when the turn is cancelled, and when the turn or the agent is gone.
func (r *reducer) cancelAsks() {
	for _, p := range r.s.Asks {
		r.emit(AnswerPermission{ID: p.ID, Cancelled: true})
	}
	r.s.Asks = nil
	r.s.Pick = 0
}
