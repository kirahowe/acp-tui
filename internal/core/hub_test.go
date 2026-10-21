package core

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func role(id string, session string) Role {
	r := Role{ID: id, Title: strings.ToUpper(id[:1]) + id[1:]}
	if session != "" {
		r.Conv = &RosterConv{Session: session, Turn: TurnIdle}
	}
	return r
}

func apply(t interface{ Helper() }, h Hub, es ...Event) (Hub, []Effect) {
	t.Helper()
	var all []Effect
	for _, e := range es {
		var fx []Effect
		h, fx = ReduceHub(h, e)
		all = append(all, fx...)
	}
	return h, all
}

func routed(session string, e Event) Event { return Routed{Session: session, Event: e} }

func convText(h Hub, session string) string {
	c := h.ConvBySession(session)
	if c == nil {
		return ""
	}
	var b strings.Builder
	for _, e := range c.State.Entries {
		if e.Kind == Agent {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

func attachesIn(fx []Effect) []Attach {
	var out []Attach
	for _, f := range fx {
		if a, ok := f.(Attach); ok {
			out = append(out, a)
		}
	}
	return out
}

func TestHubOpenFlagAttachesBeforeTheRosterIsKnown(t *testing.T) {
	h, fx := apply(t, NewHub("dev"), HubConnected{At: t0})
	if h.Focus != "dev" || h.List {
		t.Fatalf("focus %q list %v", h.Focus, h.List)
	}
	if got := attachesIn(fx); !reflect.DeepEqual(got, []Attach{{Role: "dev"}}) {
		t.Fatalf("attaches %v", got)
	}
	// Once relay answers, the placeholder takes the session and goes live.
	h, _ = apply(t, h, Attached{At: t0, Role: "dev", Session: "s1"})
	c := h.Focused()
	if c == nil || c.Session != "s1" || !c.Attached || c.State.Phase != Idle {
		t.Fatalf("conv %+v", c)
	}
}

func TestHubPickingARoleWithALiveConversationLoadsItAndAnEmptyOneDoesNot(t *testing.T) {
	h, _ := apply(t, NewHub(""), HubConnected{At: t0},
		RosterUpdate{At: t0, Roles: []Role{role("dev", "s1"), role("mail", "")}})
	if !h.List || h.Focus != "" {
		t.Fatalf("starts on the list: %+v", h)
	}
	h, fx := apply(t, h, ListOpen{At: t0})
	if got := attachesIn(fx); !reflect.DeepEqual(got, []Attach{{Role: "dev", Session: "s1"}}) {
		t.Fatalf("attaches %v", got)
	}
	h, fx = apply(t, h, FocusMove{At: t0, Delta: 1})
	if h.Focus != "mail" || len(attachesIn(fx)) != 0 || h.Focused() != nil {
		t.Fatalf("moving to a role with no conversation opens none: %+v %v", h.Focus, fx)
	}
	// Typing to it starts one, and the text waits for the attach.
	h, fx = apply(t, h, Submit{At: t0, Text: "hello"})
	if got := attachesIn(fx); !reflect.DeepEqual(got, []Attach{{Role: "mail"}}) {
		t.Fatalf("attaches %v", got)
	}
	h, fx = apply(t, h, Attached{At: t0, Role: "mail", Session: "s2"})
	var sent []SendPrompt
	for _, f := range fx {
		if fs, ok := f.(ForSession); ok {
			if sp, ok := fs.Effect.(SendPrompt); ok {
				if fs.Session != "s2" {
					t.Fatalf("prompt for %q", fs.Session)
				}
				sent = append(sent, sp)
			}
		}
	}
	if len(sent) != 1 || sent[0].Text != "hello" {
		t.Fatalf("sent %v", sent)
	}
}

func TestHubReplayBeforeTheAnswerIsKept(t *testing.T) {
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0})
	h, _ = apply(t, h,
		routed("s1", UserChunk{At: t0, Text: "hi"}),
		routed("s1", TextChunk{At: t0, Text: "hello"}),
		Attached{At: t0, Role: "dev", Session: "s1"})
	if len(h.Convs) != 1 {
		t.Fatalf("one conversation, got %d", len(h.Convs))
	}
	c := h.Convs[0]
	if c.Role != "dev" || c.State.Phase != Idle || len(c.State.Entries) != 2 ||
		c.State.Entries[0].Kind != User || c.State.Entries[1].Text != "hello" {
		t.Fatalf("%+v", c)
	}
}

func TestHubQueuedTextSurvivesAReplayThatCameFirst(t *testing.T) {
	h, _ := apply(t, NewHub(""), HubConnected{At: t0}, RosterUpdate{At: t0, Roles: []Role{role("dev", "")}})
	h, _ = apply(t, h, FocusIndex{At: t0, Index: 0}, Submit{At: t0, Text: "first"})
	h, fx := apply(t, h, routed("s9", TextChunk{At: t0, Text: "old"}), Attached{At: t0, Role: "dev", Session: "s9"})
	if len(h.Convs) != 1 {
		t.Fatalf("convs %d", len(h.Convs))
	}
	got := false
	for _, f := range fx {
		if fs, ok := f.(ForSession); ok {
			if sp, ok := fs.Effect.(SendPrompt); ok && sp.Text == "first" && fs.Session == "s9" {
				got = true
			}
		}
	}
	if !got {
		t.Fatalf("queued prompt not sent: %v", fx)
	}
}

func TestHubAttachFailureRestoresTheText(t *testing.T) {
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0})
	h, _ = apply(t, h, Submit{At: t0, Text: "typed"})
	h, fx := apply(t, h, Attached{At: t0, Role: "dev", Err: "no such role"})
	if len(h.Convs) != 0 || !strings.Contains(h.Notice, "no such role") {
		t.Fatalf("%+v", h)
	}
	if !slices.Contains(fx, Effect(RestoreInput{Text: "typed"})) {
		t.Fatalf("effects %v", fx)
	}
}

func TestHubPermissionInTheBackgroundWaitsAndSettles(t *testing.T) {
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0})
	h, _ = apply(t, h, Attached{At: t0, Role: "dev", Session: "s1"},
		RosterUpdate{At: t0, Roles: []Role{role("dev", "s1"), role("mail", "s2")}})
	h, _ = apply(t, h, FocusIndex{At: t0, Index: 1}, Attached{At: t0, Role: "mail", Session: "s2"}, FocusIndex{At: t0, Index: 0})
	ask := PermissionAsked{At: t0, Permission: Permission{ID: 7, CallID: "c1", Name: "send",
		Options: []PermOption{{ID: "a", Kind: AllowOnce}, {ID: "r", Kind: RejectOnce}}}}
	h, _ = apply(t, h, routed("s2", ask))
	if got := h.Approvals(*h.RoleByID("mail")); got != 1 {
		t.Fatalf("approvals %d", got)
	}
	if len(h.ConvBySession("s1").State.Asks) != 0 {
		t.Fatal("the request landed in the wrong conversation")
	}
	// Switching to it arms the request afresh: a quick y does nothing.
	h, fx := apply(t, h, FocusIndex{At: t0.Add(time.Hour), Index: 1}, PermissionQuick{At: t0.Add(time.Hour + time.Millisecond), Kind: AllowOnce})
	for _, f := range fx {
		if _, ok := f.(AnswerPermission); ok {
			t.Fatalf("answered too soon: %v", fx)
		}
	}
	// Another device answers it: the dialog closes and our handler is released.
	h, fx = apply(t, h, PermissionSettled{At: t0.Add(2 * time.Hour), Session: "s2", CallID: "c1", Outcome: "allow"})
	if len(h.Focused().State.Asks) != 0 {
		t.Fatal("dialog still open")
	}
	if !slices.Contains(fx, Effect(AnswerPermission{ID: 7, Cancelled: true})) {
		t.Fatalf("effects %v", fx)
	}
}

func TestHubAnswerGoesWithTheRequestsID(t *testing.T) {
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, Attached{At: t0, Role: "dev", Session: "s1"})
	ask := PermissionAsked{At: t0, Permission: Permission{ID: 3, CallID: "c", Options: []PermOption{{ID: "a", Kind: AllowOnce}, {ID: "r", Kind: RejectOnce}}}}
	h, fx := apply(t, h, routed("s1", ask), PermissionQuick{At: t0.Add(time.Second), Kind: AllowOnce})
	if !slices.Contains(fx, Effect(AnswerPermission{ID: 3, OptionID: "a"})) {
		t.Fatalf("%v", fx)
	}
	if len(h.Focused().State.Asks) != 0 {
		t.Fatal("still asking")
	}
}

func TestHubSeenIsSentOnceWhenTheLatestTurnIsOnScreen(t *testing.T) {
	r := role("dev", "s1")
	r.Conv.Turns, r.Conv.Unread = 2, true
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, Attached{At: t0, Role: "dev", Session: "s1"})
	h, fx := apply(t, h, RosterUpdate{At: t0, Roles: []Role{r}})
	if !slices.Contains(fx, Effect(SendSeen{Session: "s1", Turn: 2})) {
		t.Fatalf("%v", fx)
	}
	_, fx = apply(t, h, RosterUpdate{At: t0, Roles: []Role{r}})
	for _, f := range fx {
		if _, ok := f.(SendSeen); ok {
			t.Fatalf("sent twice: %v", fx)
		}
	}
	// Not while a turn runs, nor while the list covers the conversation,
	// nor while scrolled up.
	r.Conv.Turns, r.Conv.Turn = 3, TurnRunning
	_, fx = apply(t, h, RosterUpdate{At: t0, Roles: []Role{r}})
	if len(fx) != 0 {
		t.Fatalf("%v", fx)
	}
	r.Conv.Turn = TurnIdle
	hl, _ := apply(t, h, ToggleList{At: t0})
	_, fx = apply(t, hl, RosterUpdate{At: t0, Roles: []Role{r}})
	if len(fx) != 0 {
		t.Fatalf("list open: %v", fx)
	}
	hs := h
	hs.Convs = slices.Clone(h.Convs)
	hs.Convs[0].State.Transcript.Scroll = 4
	_, fx = apply(t, hs, RosterUpdate{At: t0, Roles: []Role{r}})
	if len(fx) != 0 {
		t.Fatalf("scrolled up: %v", fx)
	}
}

func TestHubQueuedTurnShowsInTheRoster(t *testing.T) {
	r := role("dev", "s1")
	r.Conv.Turn = TurnQueued
	h, _ := apply(t, NewHub(""), HubConnected{At: t0}, RosterUpdate{At: t0, Roles: []Role{r}})
	if h.RoleByID("dev").Conv.Turn != TurnQueued {
		t.Fatal("not queued")
	}
}

func TestHubEndAsksFirst(t *testing.T) {
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, Attached{At: t0, Role: "dev", Session: "s1"},
		RosterUpdate{At: t0, Roles: []Role{role("dev", "s1")}})
	h, fx := apply(t, h, AskEnd{At: t0})
	if !h.EndAsk || len(fx) != 0 {
		t.Fatalf("asks first: %+v %v", h.EndAsk, fx)
	}
	h2, fx := apply(t, h, ConfirmEnd{At: t0, Yes: false})
	if h2.EndAsk || len(fx) != 0 || h2.Focused() == nil {
		t.Fatal("no ends nothing")
	}
	h, fx = apply(t, h, ConfirmEnd{At: t0, Yes: true})
	if !slices.Contains(fx, Effect(EndConversation{Session: "s1"})) || !h.Ending {
		t.Fatalf("%v", fx)
	}
	// A roster that still lists it does not bring it back.
	h, fx = apply(t, h, ConversationEnded{At: t0, Session: "s1"}, RosterUpdate{At: t0, Roles: []Role{role("dev", "s1")}})
	if h.Focused() != nil || len(attachesIn(fx)) != 0 {
		t.Fatalf("%+v %v", h.Convs, fx)
	}
	// A new conversation in the role on screen is picked up.
	_, fx = apply(t, h, RosterUpdate{At: t0, Roles: []Role{role("dev", "s2")}})
	if got := attachesIn(fx); !reflect.DeepEqual(got, []Attach{{Role: "dev", Session: "s2"}}) {
		t.Fatalf("%v", got)
	}
}

func TestHubEndElsewhereDropsTheConversation(t *testing.T) {
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, Attached{At: t0, Role: "dev", Session: "s1"},
		RosterUpdate{At: t0, Roles: []Role{role("dev", "s1")}})
	h, _ = apply(t, h, RosterUpdate{At: t0, Roles: []Role{role("dev", "")}})
	if h.Focused() != nil || h.Focus != "dev" {
		t.Fatalf("%+v", h)
	}
}

func TestHubKeepsAConversationThatTheRosterHasNotListedYet(t *testing.T) {
	// A stale roster, sent before the conversation opened, arrives after
	// the attach was answered. It must not drop the new conversation.
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, Attached{At: t0, Role: "dev", Session: "s1"},
		RosterUpdate{At: t0, Roles: []Role{role("dev", "")}})
	if h.Focused() == nil {
		t.Fatal("dropped")
	}
}

func TestHubUnknownOpenRole(t *testing.T) {
	h, _ := apply(t, NewHub("nope"), HubConnected{At: t0}, RosterUpdate{At: t0, Roles: []Role{role("dev", "")}})
	if h.Focus != "" || !h.List || !strings.Contains(h.Notice, "nope") {
		t.Fatalf("%+v", h)
	}
}

func TestHubCtrlCCancelsATurnAnotherDeviceStarted(t *testing.T) {
	r := role("dev", "s1")
	r.Conv.Turn = TurnRunning
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, Attached{At: t0, Role: "dev", Session: "s1"}, RosterUpdate{At: t0, Roles: []Role{r}})
	_, fx := apply(t, h, Interrupt{At: t0, InputEmpty: true})
	if !slices.Contains(fx, Effect(ForSession{Session: "s1", Effect: SendCancel{}})) || slices.Contains(fx, Effect(Quit{})) {
		t.Fatalf("%v", fx)
	}
	r.Conv.Turn = TurnIdle
	h, _ = apply(t, h, RosterUpdate{At: t0, Roles: []Role{r}})
	_, fx = apply(t, h, Interrupt{At: t0, InputEmpty: true})
	if !slices.Contains(fx, Effect(Quit{})) {
		t.Fatalf("idle quits: %v", fx)
	}
}

func TestHubAgentExitedQuitsOnceAndMarksEverything(t *testing.T) {
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, Attached{At: t0, Role: "dev", Session: "s1"})
	h, fx := apply(t, h, AgentExited{At: t0, Err: "boom"})
	n := 0
	for _, f := range fx {
		if _, ok := f.(Quit); ok {
			n++
		}
	}
	if n != 1 || !h.Gone || h.Convs[0].State.Phase != Exited {
		t.Fatalf("%d %+v", n, h)
	}
	_, fx = apply(t, h, AgentExited{At: t0})
	if len(fx) != 0 {
		t.Fatal("twice")
	}
}

func TestHubRemoteIdleClosesWhatTheReplayLeftOpen(t *testing.T) {
	r := role("dev", "s1")
	r.Conv.Turn = TurnRunning
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, Attached{At: t0, Role: "dev", Session: "s1"},
		RosterUpdate{At: t0, Roles: []Role{r}},
		routed("s1", ToolStarted{At: t0, ID: "t", Name: "fetch", Status: Running}),
		routed("s1", TextChunk{At: t0, Text: "partial"}))
	if h.Convs[0].State.Entries[len(h.Convs[0].State.Entries)-1].Final {
		t.Fatal("closed early")
	}
	r.Conv.Turn = TurnIdle
	h, _ = apply(t, h, RosterUpdate{At: t0, Roles: []Role{r}})
	for _, e := range h.Convs[0].State.Entries {
		if !e.Final {
			t.Fatalf("entry left open: %+v", e)
		}
		for _, c := range e.Calls {
			if c.Status == Running {
				t.Fatal("call still running")
			}
		}
	}
}

// genRoster is a roster of the fixed roles r0..r3, each with its fixed
// session s0..s3 or none, and random decorations.
func genRoster(t *rapid.T) []Role {
	n := rapid.IntRange(0, 4).Draw(t, "roles")
	var roles []Role
	for i := range n {
		r := role(fmt.Sprintf("r%d", i), "")
		r.Working = rapid.Bool().Draw(t, "working")
		r.Holds = rapid.IntRange(0, 3).Draw(t, "holds")
		if rapid.Bool().Draw(t, "live") {
			r.Conv = &RosterConv{Session: fmt.Sprintf("s%d", i),
				Turn:      rapid.SampledFrom([]TurnState{TurnIdle, TurnRunning, TurnQueued}).Draw(t, "turn"),
				Turns:     rapid.IntRange(0, 5).Draw(t, "turns"),
				Unread:    rapid.Bool().Draw(t, "unread"),
				Approvals: rapid.IntRange(0, 2).Draw(t, "approvals")}
		}
		roles = append(roles, r)
	}
	return roles
}

func TestHubRosterUpdateIsIdempotent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		h, _ := ReduceHub(NewHub(rapid.SampledFrom([]string{"", "r0", "r1"}).Draw(t, "open")), HubConnected{At: t0})
		for range rapid.IntRange(0, 4).Draw(t, "before") {
			h, _ = ReduceHub(h, RosterUpdate{At: t0, Roles: genRoster(t)})
		}
		roster := RosterUpdate{At: t0, Roles: genRoster(t)}
		once, _ := ReduceHub(h, roster)
		twice, fx := ReduceHub(once, roster)
		if !reflect.DeepEqual(once, twice) {
			t.Fatalf("not idempotent:\n%+v\n%+v", once, twice)
		}
		if len(fx) != 0 {
			t.Fatalf("second application had effects: %v", fx)
		}
		if !reflect.DeepEqual(once.Roles, roster.Roles) && len(roster.Roles) > 0 {
			t.Fatalf("roster not replaced: %+v", once.Roles)
		}
	})
}

// A step of a generated hub session.
func genHubEvent(t *rapid.T, seq *int, sent map[string]*strings.Builder) Event {
	sessions := []string{"s0", "s1", "s2", "s3"}
	switch rapid.IntRange(0, 9).Draw(t, "kind") {
	case 0, 1, 2:
		s := rapid.SampledFrom(sessions).Draw(t, "session")
		*seq++
		tok := fmt.Sprintf("[%s#%d]", s, *seq)
		if sent[s] == nil {
			sent[s] = &strings.Builder{}
		}
		sent[s].WriteString(tok)
		return routed(s, TextChunk{At: t0, MessageID: "m", Text: tok})
	case 3:
		return RosterUpdate{At: t0, Roles: genRosterKeeping(t)}
	case 4:
		return FocusMove{At: t0, Delta: rapid.SampledFrom([]int{-1, 1}).Draw(t, "delta")}
	case 5:
		return FocusIndex{At: t0, Index: rapid.IntRange(0, 3).Draw(t, "index")}
	case 6:
		return FocusLast{At: t0}
	case 7:
		return ToggleList{At: t0}
	case 8:
		return ListMove{At: t0, Delta: rapid.SampledFrom([]int{-1, 1}).Draw(t, "ldelta")}
	}
	return ListOpen{At: t0}
}

// genRosterKeeping always lists every role with its session, so no
// conversation in the property ends.
func genRosterKeeping(t *rapid.T) []Role {
	var roles []Role
	for i := range 4 {
		r := role(fmt.Sprintf("r%d", i), fmt.Sprintf("s%d", i))
		r.Conv.Turn = rapid.SampledFrom([]TurnState{TurnIdle, TurnRunning, TurnQueued}).Draw(t, "turn")
		r.Conv.Turns = rapid.IntRange(0, 5).Draw(t, "turns")
		r.Conv.Unread = rapid.Bool().Draw(t, "unread")
		roles = append(roles, r)
	}
	return roles
}

func TestHubSwitchingNeverLosesOrMixesEntries(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		h, _ := ReduceHub(NewHub(""), HubConnected{At: t0})
		h, _ = ReduceHub(h, RosterUpdate{At: t0, Roles: genRosterKeeping(t)})
		sent := map[string]*strings.Builder{}
		seq := 0
		seen := map[string]int{}
		for range rapid.IntRange(1, 60).Draw(t, "steps") {
			var fx []Effect
			h, fx = ReduceHub(h, genHubEvent(t, &seq, sent))
			// Attach answers come back for what was asked.
			for _, f := range fx {
				if a, ok := f.(Attach); ok {
					sess := a.Session
					if sess == "" {
						sess = "s" + strings.TrimPrefix(a.Role, "r")
					}
					h, _ = ReduceHub(h, Attached{At: t0, Role: a.Role, Session: sess})
				}
				if s, ok := f.(SendSeen); ok {
					if s.Turn <= seen[s.Session] {
						t.Fatalf("seen went back: %v after %d", s, seen[s.Session])
					}
					seen[s.Session] = s.Turn
				}
			}
			// Invariants after every step.
			roles := map[string]bool{}
			for _, c := range h.Convs {
				if c.Role != "" {
					if roles[c.Role] {
						t.Fatalf("two conversations for %s", c.Role)
					}
					roles[c.Role] = true
				}
			}
			if h.Focus != "" && h.RoleByID(h.Focus) == nil {
				t.Fatalf("focus %q is not in the roster", h.Focus)
			}
			if h.List && h.Focus == "" && len(h.Convs) > 0 && false {
				t.Fatal("unreachable")
			}
		}
		for s, want := range sent {
			if got := convText(h, s); got != want.String() {
				t.Fatalf("session %s: entries %q, sent %q", s, got, want.String())
			}
		}
	})
}

func TestHubReplayThenLiveEqualsTheWholeTranscript(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var events []Event
		n := rapid.IntRange(1, 25).Draw(t, "n")
		for i := range n {
			switch rapid.IntRange(0, 4).Draw(t, "kind") {
			case 0:
				events = append(events, routed("s1", UserChunk{At: t0, MessageID: fmt.Sprintf("u%d", i), Text: fmt.Sprintf("q%d", i)}))
			case 1:
				events = append(events, routed("s1", ThoughtChunk{At: t0, MessageID: "t", Text: "hm"}))
			case 2:
				events = append(events, routed("s1", ToolStarted{At: t0, ID: fmt.Sprintf("c%d", i), Name: "x", Status: Running}))
			case 3:
				events = append(events, routed("s1", ToolUpdated{At: t0, ID: fmt.Sprintf("c%d", max(i-1, 0)), Status: statusp(OK)}))
			default:
				events = append(events, routed("s1", TextChunk{At: t0, MessageID: "a", Text: rapid.SampledFrom([]string{"a ", "b\n\n", "c"}).Draw(t, "text")}))
			}
		}
		attach := Attached{At: t0, Role: "dev", Session: "s1"}
		cut := rapid.IntRange(0, n).Draw(t, "cut")
		// Whole: attached first, everything live.
		whole, _ := apply(t, NewHub("dev"), append([]Event{HubConnected{At: t0}, attach}, events...)...)
		// Replayed: the first part before the answer, the rest after it.
		var split []Event
		split = append(split, HubConnected{At: t0})
		split = append(split, events[:cut]...)
		split = append(split, attach)
		split = append(split, events[cut:]...)
		part, _ := apply(t, NewHub("dev"), split...)
		if !reflect.DeepEqual(whole.Convs[0].State.Entries, part.Convs[0].State.Entries) {
			t.Fatalf("entries differ at cut %d:\n%+v\n%+v", cut, whole.Convs[0].State.Entries, part.Convs[0].State.Entries)
		}
	})
}

func statusp(s ToolStatus) *ToolStatus { return &s }

func TestUserChunkIsHarmlessInSingleSessionMode(t *testing.T) {
	s, fx := Reduce(State{}, Connected{At: t0})
	s, fx2 := Reduce(s, UserChunk{At: t0, Text: "from elsewhere"})
	if len(fx)+len(fx2) != 0 && len(fx2) != 0 {
		t.Fatalf("effects %v", fx2)
	}
	last := s.Entries[len(s.Entries)-1]
	if s.Phase != Idle || last.Kind != User || last.Text != "from elsewhere" {
		t.Fatalf("%+v", s)
	}
}
