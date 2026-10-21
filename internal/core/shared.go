package core

import "time"

// A State is Shared when it is one conversation among several on one
// connection (hub mode). Another device may be driving it, and an
// attach is followed by a replay of what already happened, so its
// updates are taken in any phase, not only inside a prompt of our own.
// Its transcript is always the full-screen view, which the hub draws,
// so nothing goes to scrollback and the view never closes.

// Viewport says whether the window is too small to show a permission
// request whole. While it is, the keys that approve do nothing (cancelling
// still works): a request that cannot be read cannot be allowed.
type Viewport struct {
	At       time.Time
	TooSmall bool
}

func (e Viewport) at() time.Time { return e.At }

// Events about a conversation that the hub tells it.
type (
	// UserChunk is text the agent says the user wrote: relay replays
	// each prompt that way, and sends the prompts of other devices as
	// they happen. A plain agent has no reason to send one, but one that
	// does is shown the same.
	UserChunk struct {
		At              time.Time
		MessageID, Text string
	}
	// ConvAttached means the attach to the conversation has been
	// answered, so it can take prompts. Embedded is the agent's
	// promptCapabilities.embeddedContext.
	ConvAttached struct {
		At       time.Time
		Embedded bool
	}
	// Settled says another device answered the request for CallID, so
	// the dialog closes (_relay/permission/settled).
	Settled struct {
		At      time.Time
		CallID  string
		Outcome string // what the other device chose, for the note
	}
	// RemoteIdle says relay reports no turn running in the conversation,
	// so whatever is still open in it will not change.
	RemoteIdle struct{ At time.Time }
	// ReplayEnd says relay has sent all of the replay (_relay/replayed).
	// Calls the replay left open will not finish, so they stop.
	ReplayEnd struct{ At time.Time }
	// Blurred says the conversation has left the screen, or been covered.
	// A trust confirmation waiting in its pane is dropped: the operator
	// starts it again on purpose.
	Blurred struct{ At time.Time }
	// Focused says the conversation has just come on screen. A request
	// on offer is armed afresh, as if it had just arrived.
	Focused struct{ At time.Time }
)

func (e UserChunk) at() time.Time    { return e.At }
func (e ConvAttached) at() time.Time { return e.At }
func (e Settled) at() time.Time      { return e.At }
func (e RemoteIdle) at() time.Time   { return e.At }
func (e ReplayEnd) at() time.Time    { return e.At }
func (e Blurred) at() time.Time      { return e.At }
func (e Focused) at() time.Time      { return e.At }

func (r *reducer) shared(e Event) {
	s := &r.s
	switch e := e.(type) {
	case UserChunk:
		if s.Phase == Exited {
			return
		}
		if n := len(s.Entries); n > 0 && e.MessageID != "" && s.Entries[n-1].Kind == User && s.Entries[n-1].ID == e.MessageID {
			s.Entries[n-1].Text += e.Text
			return
		}
		// The text came down the agent's channel, so its URLs show
		// defanged once the session is contaminated, as the agent's own
		// words do.
		r.add(Entry{Kind: User, ID: e.MessageID, Text: e.Text, Final: true})
		s.Entries[len(s.Entries)-1].Defang = s.Labels.Contaminated
	case ConvAttached:
		if s.Phase == Starting {
			s.Phase = Idle
			s.Embedded = e.Embedded
			r.refresh()
			r.askCards()
			r.sendQueue(e.At)
		}
	case Settled:
		for i, p := range s.Asks {
			if p.CallID != e.CallID {
				continue
			}
			s.Asks = append(s.Asks[:i:i], s.Asks[i+1:]...)
			// Our own answer to relay is dropped, but the handler
			// waiting on it has to return.
			r.emit(AnswerPermission{ID: p.ID, Cancelled: true})
			r.proposalAnswered(p.ID, StatusCancelledAsk)
			r.refresh()
			r.add(Entry{Kind: Note, Text: "answered elsewhere: " + EscapeLabel(p.Label()), Final: true})
			if i == 0 {
				r.next(e.At)
			}
			return
		}
	case RemoteIdle:
		if s.Phase != Busy && s.Phase != Exited {
			r.closeAll()
		}
	case ReplayEnd:
		// An open reply or thought stays open: the turn may still be
		// running and its next chunk joins it. The roster going idle
		// closes those.
		if s.Phase != Exited {
			for i := range s.Entries {
				en := &s.Entries[i]
				if en.Kind != Tool || en.Final {
					continue
				}
				// Finality is left to the next entry: a run with every call
				// done still takes the next call.
				for j := range en.Calls {
					if !done(en.Calls[j].Status) {
						en.Calls[j].Status = Stopped
					}
				}
			}
		}
	case Blurred:
		if t := s.Pane.Trust; t != nil && !t.Sent {
			s.Pane.Trust = nil
		}
	case Focused:
		r.next(e.At)
	}
}
