package core

import (
	"testing"
	"time"

	"pgregory.net/rapid"
)

// laterThan returns a time at or after every event in the session.
func laterThan(t *rapid.T, steps []step) time.Time {
	at := t0
	for _, st := range steps {
		if e := st.event.at(); e.After(at) {
			at = e
		}
	}
	return at.Add(time.Duration(rapid.IntRange(0, 60000).Draw(t, "afterMs")) * time.Millisecond)
}

// P8: Status is defined for every reachable state and agrees with it.
func TestStatusTotal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genSession(t)
		now := laterThan(t, steps)
		for i, st := range steps {
			s := st.after
			l := Status(s, now)
			if (l.Kind == StatusIdle) != (s.Phase == Idle) {
				t.Fatalf("step %d: kind %v in phase %v", i, l.Kind, s.Phase)
			}
			retrying := s.Phase == Busy && !s.Turn.Cancelling && s.Turn.Retry != nil && s.Turn.Retry.At.After(now)
			if (l.Kind == StatusRetrying) != retrying {
				t.Fatalf("step %d: kind %v, retry %+v, now %v", i, l.Kind, s.Turn.Retry, now)
			}
			if l.Kind == StatusRetrying && l.RetryIn <= 0 {
				t.Fatalf("step %d: retrying in %v", i, l.RetryIn)
			}
			if l.Queued != len(s.Queue) || l.Elapsed < 0 || (l.Quiet != 0 && l.Quiet < QuietAfter) {
				t.Fatalf("step %d: %+v", i, l)
			}
			if l.Kind == StatusRunning && l.Tools == 0 || l.Tools != 1 && l.Tool != "" {
				t.Fatalf("step %d: %+v", i, l)
			}
		}
	})
}

// P9: for a fixed state, Elapsed and Quiet never decrease as time passes.
func TestElapsedMonotonic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		steps := genSession(t)
		s := final(steps)
		now := laterThan(t, steps)
		prev := Status(s, now)
		for range 20 {
			now = now.Add(time.Duration(rapid.IntRange(0, 20000).Draw(t, "stepMs")) * time.Millisecond)
			l := Status(s, now)
			if l.Elapsed < prev.Elapsed || l.Quiet < prev.Quiet {
				t.Fatalf("at %v: %+v after %+v", now, l, prev)
			}
			prev = l
		}
	})
}

func TestStatusPrecedence(t *testing.T) {
	retryAt := t0.Add(10 * time.Second)
	running := Running
	s := busy()
	s, _ = Reduce(s, ThoughtChunk{At: t0, Text: "hm"})
	if k := Status(s, t0).Kind; k != StatusThinking {
		t.Fatalf("kind %v, want thinking", k)
	}
	s, _ = Reduce(s, ToolStarted{At: t0, ID: "c1", Title: "echo"})
	if k := Status(s, t0).Kind; k != StatusWaiting {
		t.Fatalf("kind %v, want waiting for a pending tool", k)
	}
	s, _ = Reduce(s, ToolUpdated{At: t0, ID: "c1", Status: &running})
	if l := Status(s, t0); l.Kind != StatusRunning || l.Tool != "echo" {
		t.Fatalf("%+v, want running echo", l)
	}
	s, _ = Reduce(s, Notice{At: t0, Severity: "warning", Title: "rate limited", RetryAt: &retryAt})
	if l := Status(s, t0.Add(time.Second)); l.Kind != StatusRetrying || l.RetryIn != 9*time.Second {
		t.Fatalf("%+v, want retrying in 9s", l)
	}
	if k := Status(s, retryAt).Kind; k != StatusRunning {
		t.Fatalf("kind %v once the retry time has passed", k)
	}
	s, _ = Reduce(s, Interrupt{At: t0})
	if k := Status(s, t0).Kind; k != StatusCancelling {
		t.Fatalf("kind %v, want cancelling", k)
	}
	if l := Status(s, t0.Add(QuietAfter)); l.Quiet != QuietAfter {
		t.Fatalf("quiet %v", l.Quiet)
	}
}
