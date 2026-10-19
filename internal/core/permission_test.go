package core

import (
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func genLabels(t *rapid.T) Labels {
	return Labels{Private: rapid.Bool().Draw(t, "private"), Contaminated: rapid.Bool().Draw(t, "contaminated")}
}

func genOptions(t *rapid.T) []PermOption {
	kinds := []OptionKind{AllowOnce, AllowAlways, RejectOnce, RejectAlways, "future_kind", ""}
	n := rapid.IntRange(0, 5).Draw(t, "nOptions")
	var opts []PermOption
	for i := range n {
		opts = append(opts, PermOption{ID: fmt.Sprintf("opt%d", i), Name: fmt.Sprintf("Option %d", i),
			Kind: rapid.SampledFrom(kinds).Draw(t, "kind")})
	}
	return opts
}

func busyState() State {
	s, _ := Reduce(State{}, Connected{At: t0})
	s, _ = Reduce(s, Submit{At: t0, Text: "go"})
	return s
}

func answers(fx []Effect) []AnswerPermission {
	var out []AnswerPermission
	for _, f := range fx {
		if a, ok := f.(AnswerPermission); ok {
			out = append(out, a)
		}
	}
	return out
}

// Labels only grow, whatever order and whatever phase the updates come in.
func TestLabelsMonotone(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var s State
		seen := Labels{}
		now := t0
		for range rapid.IntRange(1, 40).Draw(t, "steps") {
			before := s.Labels
			var e Event
			switch rapid.IntRange(0, 4).Draw(t, "event") {
			case 0:
				l := genLabels(t)
				seen = seen.Union(l)
				e = LabelsSeen{At: now, Labels: l}
			case 1:
				e = Connected{At: now}
			case 2:
				e = Submit{At: now, Text: "x"}
			case 3:
				e = PromptDone{At: now, Stop: EndTurn}
			case 4:
				e = AgentExited{At: now}
			}
			s, _ = Reduce(s, e)
			if (before.Private && !s.Labels.Private) || (before.Contaminated && !s.Labels.Contaminated) {
				t.Fatalf("labels shrank from %+v to %+v on %T", before, s.Labels, e)
			}
		}
		if s.Labels != seen {
			t.Fatalf("labels %+v, want the union %+v", s.Labels, seen)
		}
		// An empty set seen later changes nothing.
		again, _ := Reduce(s, LabelsSeen{At: now})
		if again.Labels != s.Labels {
			t.Fatalf("empty LabelsSeen changed %+v to %+v", s.Labels, again.Labels)
		}
	})
}

func TestLabelsUnion(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a, b := genLabels(t), genLabels(t)
		u := a.Union(b)
		if u != b.Union(a) || u.Union(a) != u || (a.Union(Labels{}) != a) {
			t.Fatalf("union laws: %+v %+v %+v", a, b, u)
		}
		if u.Empty() != (a.Empty() && b.Empty()) {
			t.Fatalf("Empty(%+v) with %+v and %+v", u, a, b)
		}
	})
}

// P-perm1: a request is answered with an option it offered, exactly once,
// and the answer is the option the key stands for. Keys before the
// request is armed answer nothing.
func TestPermissionAnswersAnOfferedOption(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := busyState()
		opts := genOptions(t)
		s, _ = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: 1, Name: "web_fetch", Options: opts}})
		// Only once-options are offered, in the order sent.
		var once []PermOption
		for _, o := range opts {
			if o.Kind == AllowOnce || o.Kind == RejectOnce {
				once = append(once, o)
			}
		}
		if len(once) != len(s.Asks[0].Options) {
			t.Fatalf("offered %+v of %+v", s.Asks[0].Options, opts)
		}
		for i, o := range s.Asks[0].Options {
			if o != once[i] {
				t.Fatalf("offered %+v of %+v", s.Asks[0].Options, opts)
			}
		}
		opts = once
		if len(s.Asks) != 1 || s.Pick < 0 || (len(opts) > 0 && s.Pick >= len(opts)) {
			t.Fatalf("asks %d pick %d of %d", len(s.Asks), s.Pick, len(opts))
		}
		// Too early: nothing is answered.
		early := t0.Add(ArmDelay - time.Millisecond)
		for _, e := range []Event{PermissionConfirm{At: early}, PermissionQuick{At: early, Kind: AllowOnce}, PermissionQuick{At: early, Kind: RejectOnce}} {
			next, fx := Reduce(s, e)
			if len(answers(fx)) != 0 || len(next.Asks) != 1 {
				t.Fatalf("%T before the delay answered: %+v", e, fx)
			}
		}
		for range rapid.IntRange(0, 6).Draw(t, "moves") {
			s, _ = Reduce(s, PermissionMove{At: t0, Delta: rapid.IntRange(-3, 3).Draw(t, "delta")})
			if len(opts) > 0 && (s.Pick < 0 || s.Pick >= len(opts)) {
				t.Fatalf("pick %d out of %d options", s.Pick, len(opts))
			}
		}
		late := t0.Add(ArmDelay)
		var e Event
		var want *PermOption
		switch rapid.IntRange(0, 4).Draw(t, "key") {
		case 0:
			e = PermissionConfirm{At: late}
			if len(opts) > 0 {
				want = &opts[s.Pick]
			}
		case 1:
			e = PermissionQuick{At: late, Kind: AllowOnce}
			if i := PickKind(opts, AllowOnce); i >= 0 {
				want = &opts[i]
			}
		case 2:
			e = PermissionQuick{At: late, Kind: RejectOnce}
			if i := PickKind(opts, RejectOnce); i >= 0 {
				want = &opts[i]
			}
		case 3:
			e = PermissionCancel{At: t0} // never gated
			next, fx := Reduce(s, e)
			a := answers(fx)
			if len(a) != 1 || !a[0].Cancelled || a[0].ID != 1 || len(next.Asks) != 0 {
				t.Fatalf("cancel: %+v asks %d", a, len(next.Asks))
			}
			return
		case 4:
			e = PermissionShowAll{At: late}
			next, fx := Reduce(s, e)
			if len(answers(fx)) != 0 || len(next.Asks) != 1 || next.Entries[len(next.Entries)-1].Kind != Request {
				t.Fatalf("show all answered or left no entry")
			}
			return
		}
		next, fx := Reduce(s, e)
		a := answers(fx)
		if want == nil {
			if len(a) != 0 || len(next.Asks) != 1 {
				t.Fatalf("%T with no such option answered %+v", e, a)
			}
			return
		}
		if len(a) != 1 || a[0].Cancelled || a[0].OptionID != want.ID || a[0].ID != 1 || len(next.Asks) != 0 {
			t.Fatalf("%T gave %+v, want option %q", e, a, want.ID)
		}
	})
}

// P-perm2: with several requests queued, each is answered once, in order,
// and each gets its own arming delay from the moment it comes up.
func TestPermissionQueue(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := busyState()
		n := rapid.IntRange(2, 5).Draw(t, "n")
		for i := 1; i <= n; i++ {
			s, _ = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: uint64(i), Options: []PermOption{
				{ID: "a", Kind: AllowOnce}, {ID: "r", Kind: RejectOnce}}}})
		}
		if len(s.Asks) != n {
			t.Fatalf("%d queued of %d", len(s.Asks), n)
		}
		now := t0.Add(ArmDelay)
		for i := 1; i <= n; i++ {
			var fx []Effect
			s, fx = Reduce(s, PermissionQuick{At: now, Kind: AllowOnce})
			a := answers(fx)
			if len(a) != 1 || a[0].ID != uint64(i) || a[0].OptionID != "a" {
				t.Fatalf("answer %d: %+v", i, a)
			}
			if i < n {
				// The next one is not armed at the instant it comes up.
				s2, fx2 := Reduce(s, PermissionQuick{At: now, Kind: AllowOnce})
				if len(answers(fx2)) != 0 || len(s2.Asks) != n-i {
					t.Fatalf("request %d answered at once by a stray key", i+1)
				}
				now = now.Add(ArmDelay)
			}
		}
		if len(s.Asks) != 0 {
			t.Fatalf("%d left", len(s.Asks))
		}
	})
}

// P-perm3: every request is answered exactly once, however the turn ends.
func TestPermissionsAlwaysAnswered(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := busyState()
		n := rapid.IntRange(1, 4).Draw(t, "n")
		for i := 1; i <= n; i++ {
			s, _ = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: uint64(i), Options: genOptions(t)}})
		}
		end := rapid.SampledFrom([]Event{
			PromptDone{At: t0, Stop: EndTurn}, PromptDone{At: t0, Stop: Cancelled}, PromptFailed{At: t0, Message: "x"},
			AgentExited{At: t0}, Interrupt{At: t0, InputEmpty: true}, EOF{At: t0}, PermissionCancel{At: t0},
		}).Draw(t, "end")
		_, fx := Reduce(s, end)
		got := map[uint64]int{}
		for _, a := range answers(fx) {
			if !a.Cancelled {
				t.Fatalf("%T answered %+v, not cancelled", end, a)
			}
			got[a.ID]++
		}
		want := n
		if _, one := end.(PermissionCancel); one {
			want = 1
		}
		if len(got) != want {
			t.Fatalf("%T answered %v of %d", end, got, n)
		}
		for id, c := range got {
			if c != 1 {
				t.Fatalf("request %d answered %d times", id, c)
			}
		}
	})
}

// A request that comes in while the turn is being cancelled, or after
// the agent is gone, is answered cancelled at once and never shown.
func TestPermissionAfterCancel(t *testing.T) {
	s := busyState()
	s, _ = Reduce(s, Interrupt{At: t0})
	s, fx := Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: 9}})
	if a := answers(fx); len(a) != 1 || !a[0].Cancelled || a[0].ID != 9 || len(s.Asks) != 0 {
		t.Fatalf("while cancelling: %+v, %d asks", a, len(s.Asks))
	}
	s, _ = Reduce(s, AgentExited{At: t0})
	_, fx = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: 10}})
	if a := answers(fx); len(a) != 1 || !a[0].Cancelled {
		t.Fatalf("after exit: %+v", a)
	}
}

// The highlight starts on the safest option, so a bare enter refuses.
func TestDefaultPick(t *testing.T) {
	opts := []PermOption{{ID: "a", Kind: AllowOnce}, {ID: "ra", Kind: RejectAlways}, {ID: "r", Kind: RejectOnce}}
	if i := DefaultPick(opts); opts[i].ID != "r" {
		t.Errorf("picked %v", opts[i])
	}
	if i := DefaultPick(opts[:2]); opts[i].ID != "ra" {
		t.Errorf("picked %v", opts[i])
	}
	if i := DefaultPick(opts[:1]); i != 0 {
		t.Errorf("picked %d", i)
	}
	if i := DefaultPick(nil); i != 0 {
		t.Errorf("picked %d", i)
	}
}

// The status line says it is waiting for the person, and does not call
// that silence the agent's.
func TestStatusAsking(t *testing.T) {
	s := busyState()
	s, _ = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: 1}})
	s, _ = Reduce(s, LabelsSeen{At: t0, Labels: Labels{Private: true, Contaminated: true}})
	l := Status(s, t0.Add(time.Minute))
	if l.Kind != StatusAsking || l.Quiet != 0 || l.Asks != 1 || l.Labels != (Labels{Private: true, Contaminated: true}) {
		t.Fatalf("%+v", l)
	}
}

// P-perm6: no standing permission is ever offered or granted, whatever
// the agent sends and whichever key is pressed.
func TestNoAlwaysOptions(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := busyState()
		s, _ = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: 1, Options: genOptions(t)}})
		for _, o := range s.Asks[0].Options {
			if o.Kind != AllowOnce && o.Kind != RejectOnce {
				t.Fatalf("offered %+v", o)
			}
		}
		late := t0.Add(ArmDelay)
		for _, e := range []Event{PermissionConfirm{At: late}, PermissionQuick{At: late, Kind: AllowAlways}, PermissionQuick{At: late, Kind: RejectAlways}} {
			_, fx := Reduce(s, e)
			for _, a := range answers(fx) {
				if !a.Cancelled && (a.OptionID == "" || a.OptionID[:3] != "opt") {
					t.Fatalf("answer %+v", a)
				}
			}
		}
		// Quick keys for the always kinds find nothing.
		for _, k := range []OptionKind{AllowAlways, RejectAlways} {
			if _, fx := Reduce(s, PermissionQuick{At: late, Kind: k}); len(answers(fx)) != 0 {
				t.Fatalf("%s answered", k)
			}
		}
	})
}

func TestOnlyAlwaysOptionsLeavesCancel(t *testing.T) {
	s := busyState()
	s, _ = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: 1, Options: []PermOption{
		{ID: "a", Kind: AllowAlways}, {ID: "r", Kind: RejectAlways}}}})
	late := t0.Add(ArmDelay)
	if _, fx := Reduce(s, PermissionConfirm{At: late}); len(answers(fx)) != 0 {
		t.Fatalf("enter answered with nothing offered: %+v", fx)
	}
	if _, fx := Reduce(s, PermissionCancel{At: t0}); len(answers(fx)) != 1 {
		t.Fatalf("cancel: %+v", fx)
	}
}

// A request's proposal is labelled by its preview's labels, and the
// session is not: the session saw nothing of the preview.
func TestPreviewLabelsStayOffTheSession(t *testing.T) {
	s := State{Phase: Busy, Ready: true, Labels: Labels{Private: true}}
	s, _ = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: 1, Name: "send", CallID: "c1", Preview: "p",
		Labels: Labels{Private: true}, PreviewLabels: Labels{Contaminated: true}, FixedArgs: []string{"group"},
		Options: []PermOption{{ID: "a", Kind: AllowOnce}}}})
	if s.Labels.Contaminated {
		t.Error("the session took the preview's labels")
	}
	var found bool
	for _, it := range s.Pane.Local {
		if it.Kind == ProposalItem && it.CallID == "c1" {
			found = true
			if !it.Labels.Contaminated || !it.Labels.Private || len(it.FixedArgs) != 1 {
				t.Errorf("proposal %#v", it)
			}
		}
	}
	if !found {
		t.Error("no proposal")
	}
}
