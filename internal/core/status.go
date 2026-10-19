package core

import (
	"math/bits"
	"time"
)

type StatusKind int

const (
	StatusStarting StatusKind = iota
	StatusIdle
	StatusWaiting
	StatusThinking
	StatusWriting
	StatusRunning
	StatusRetrying
	StatusCancelling
	StatusAsking // a permission request waits for the person
	StatusExited
)

// QuietAfter is how long a busy agent can be silent before the status line
// says so.
const QuietAfter = 10 * time.Second

// StatusLine is what the status line shows, before formatting.
type StatusLine struct {
	Kind    StatusKind
	Tool    string        // the title, when one tool is running
	Tools   int           // how many tools are running
	RetryIn time.Duration // when Retrying
	Reason  string        // why it is retrying, when Retrying
	Elapsed time.Duration // since the turn started
	Quiet   time.Duration // since the last agent event, once >= QuietAfter
	Queued  int
	// Context is the share of the context window in use, in percent, when
	// HasContext: the agent has reported a window size.
	Context    int
	HasContext bool
	Spend      *Cost  // what the session has cost, once the agent says
	Labels     Labels // what the session has seen
	TrustNext  bool   // the next paste goes in as trusted text
	Asks       int    // permission requests waiting, including the one on offer
	// PaneItems is how many items the workspace pane lists. PaneHint says
	// the pane is a toggled overlay, so the status line names its key.
	PaneItems int
	PaneHint  bool
}

// Status describes s at time now.
func Status(s State, now time.Time) StatusLine {
	l := StatusLine{Queued: len(s.Queue), Spend: s.Spend, Labels: s.Labels, Asks: len(s.Asks), TrustNext: s.TrustNext, PaneItems: len(PaneItems(s))}
	if u := s.Usage; u != nil && u.Size > 0 {
		l.HasContext = true
		// used*100 can overflow 64 bits, so multiply into 128.
		hi, lo := bits.Mul64(min(u.Used, u.Size), 100)
		pct, _ := bits.Div64(hi, lo, u.Size) // hi < Size, as Used <= Size
		l.Context = int(pct)
	}
	switch s.Phase {
	case Starting:
		l.Kind = StatusStarting
		return l
	case Idle:
		l.Kind = StatusIdle
		return l
	case Exited:
		l.Kind = StatusExited
		return l
	}
	t := s.Turn
	l.Elapsed = max(now.Sub(t.Started), 0)
	if q := now.Sub(t.LastEvent); q >= QuietAfter {
		l.Quiet = q
	}
	for _, e := range s.Entries[s.Printed.Entry:] { // entries before it are final
		if e.Kind != Tool || e.Final {
			continue
		}
		for _, c := range e.Calls {
			if c.Status == Running {
				l.Tools++
				l.Tool = c.Label()
			}
		}
	}
	if l.Tools != 1 {
		l.Tool = ""
	}
	switch {
	case t.Cancelling:
		l.Kind = StatusCancelling
	case len(s.Asks) > 0:
		l.Kind = StatusAsking
		l.Quiet = 0 // the silence is the person's, not the agent's
	case t.Retry != nil && t.Retry.At.After(now):
		l.Kind = StatusRetrying
		l.RetryIn = t.Retry.At.Sub(now)
		l.Reason = t.Retry.Reason
	case l.Tools > 0:
		l.Kind = StatusRunning
	case t.Activity == Thinking:
		l.Kind = StatusThinking
	case t.Activity == Writing:
		l.Kind = StatusWriting
	default:
		l.Kind = StatusWaiting
	}
	return l
}
