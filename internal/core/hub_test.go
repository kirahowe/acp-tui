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
	if len(fx) != 0 {
		t.Fatalf("seen before the replay is over: %v", fx)
	}
	h, fx = apply(t, h, Replayed{At: t0, Session: "s1"})
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

// A load that fails is not tried again on every roster that still lists
// the session.
func TestHubFailedLoadIsNotRetried(t *testing.T) {
	h, fx := apply(t, NewHub(""), HubConnected{At: t0}, RosterUpdate{At: t0, Roles: []Role{role("dev", "s1")}}, ListOpen{At: t0})
	if len(attachesIn(fx)) != 1 {
		t.Fatalf("%v", fx)
	}
	h, _ = apply(t, h, Attached{At: t0, Role: "dev", Session: "s1", Err: "not live"})
	_, fx = apply(t, h, RosterUpdate{At: t0, Roles: []Role{role("dev", "s1")}})
	if len(attachesIn(fx)) != 0 {
		t.Fatalf("tried again: %v", fx)
	}
}

func TestHubReplayedClosesWhatTheReplayLeftOpen(t *testing.T) {
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, RosterUpdate{At: t0, Roles: []Role{role("dev", "s1")}},
		routed("s1", ToolStarted{At: t0, ID: "t", Name: "fetch", Status: Running}),
		Attached{At: t0, Role: "dev", Session: "s1"})
	h, _ = apply(t, h, Replayed{At: t0, Session: "s1"})
	c := h.Convs[0]
	if !c.Loaded {
		t.Fatal("not marked loaded")
	}
	for _, e := range c.State.Entries {
		for _, call := range e.Calls {
			if call.Status != Stopped {
				t.Fatalf("call %+v", call)
			}
		}
	}
}

// A replay cut anywhere (at a point where no call is open), then the
// marker, then live updates, is the whole transcript.
func TestHubReplayMarkerThenLiveEqualsTheWholeTranscript(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var events []Event
		safe := []int{0} // cut points with no call open
		n := rapid.IntRange(1, 20).Draw(t, "n")
		for i := range n {
			switch rapid.IntRange(0, 3).Draw(t, "kind") {
			case 0:
				events = append(events, routed("s1", UserChunk{At: t0, MessageID: fmt.Sprintf("u%d", i), Text: "q"}))
			case 1:
				id := fmt.Sprintf("c%d", i)
				events = append(events, routed("s1", ToolStarted{At: t0, ID: id, Name: "x", Status: Running}),
					routed("s1", ToolUpdated{At: t0, ID: id, Status: statusp(OK)}))
			case 2:
				events = append(events, routed("s1", ThoughtChunk{At: t0, MessageID: "t", Text: "hm"}))
			default:
				events = append(events, routed("s1", TextChunk{At: t0, MessageID: "a", Text: rapid.SampledFrom([]string{"a ", "b\n\n", "c"}).Draw(t, "text")}))
			}
			safe = append(safe, len(events))
		}
		cut := safe[rapid.IntRange(0, len(safe)-1).Draw(t, "cut")]
		attach := Attached{At: t0, Role: "dev", Session: "s1"}
		whole, _ := apply(t, NewHub("dev"), append([]Event{HubConnected{At: t0}, attach}, events...)...)
		split := append([]Event{HubConnected{At: t0}, attach}, events[:cut]...)
		split = append(split, Replayed{At: t0, Session: "s1"})
		split = append(split, events[cut:]...)
		part, _ := apply(t, NewHub("dev"), split...)
		w, p := whole.Convs[0].State.Entries, part.Convs[0].State.Entries
		// Finality is not compared: an entry the marker leaves open is
		// open in both.
		if len(w) != len(p) {
			t.Fatalf("cut %d: %d entries vs %d", cut, len(w), len(p))
		}
		for i := range w {
			w[i].Final, p[i].Final = false, false
			if !reflect.DeepEqual(w[i], p[i]) {
				t.Fatalf("cut %d entry %d:\n%+v\n%+v", cut, i, w[i], p[i])
			}
		}
	})
}

func toolCalls(h Hub) []Call {
	var out []Call
	for _, e := range h.Convs[0].State.Entries {
		out = append(out, e.Calls...)
	}
	return out
}

// Attaching in the middle of a turn started elsewhere: the call that is
// open at the marker is running, and its update arrives live.
func TestHubReplayedMidTurnLeavesRunningCallsOpen(t *testing.T) {
	r := role("dev", "s1")
	r.Conv.Turn = TurnRunning
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, RosterUpdate{At: t0, Roles: []Role{r}},
		Attached{At: t0, Role: "dev", Session: "s1"},
		routed("s1", ToolStarted{At: t0, ID: "t", Name: "fetch", Status: Running}),
		Replayed{At: t0, Session: "s1"})
	if c := toolCalls(h); len(c) != 1 || c[0].Status != Running {
		t.Fatalf("calls after the marker: %+v", c)
	}
	h, _ = apply(t, h, routed("s1", ToolUpdated{At: t0, ID: "t", Status: statusp(OK)}))
	if c := toolCalls(h); len(c) != 1 || c[0].Status != OK {
		t.Fatalf("calls after the live update: %+v", c)
	}
	// And a queued turn counts as running; an idle roster later closes leftovers.
	r.Conv.Turn = TurnIdle
	h, _ = apply(t, h, routed("s1", ToolStarted{At: t0, ID: "u", Name: "x", Status: Running}),
		RosterUpdate{At: t0, Roles: []Role{r}})
	if c := toolCalls(h); len(c) != 2 || c[1].Status != Stopped {
		t.Fatalf("fallback: %+v", c)
	}
}

// A live update for a call the marker already stopped changes it or is
// ignored, but never adds a second entry.
func TestHubLateUpdateForAStoppedCallAddsNothing(t *testing.T) {
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, RosterUpdate{At: t0, Roles: []Role{role("dev", "s1")}},
		Attached{At: t0, Role: "dev", Session: "s1"},
		routed("s1", ToolStarted{At: t0, ID: "t", Name: "fetch", Status: Running}),
		Replayed{At: t0, Session: "s1"})
	if c := toolCalls(h); len(c) != 1 || c[0].Status != Stopped {
		t.Fatalf("not stopped: %+v", c)
	}
	n := len(h.Convs[0].State.Entries)
	h, _ = apply(t, h, routed("s1", ToolUpdated{At: t0, ID: "t", Status: statusp(OK)}))
	if len(h.Convs[0].State.Entries) != n || len(toolCalls(h)) != 1 {
		t.Fatalf("a second entry: %+v", h.Convs[0].State.Entries)
	}
	// Even once the run is final.
	h, _ = apply(t, h, routed("s1", TextChunk{At: t0, Text: "x"}), routed("s1", TextChunk{At: t0, Text: "\n\n"}),
		routed("s1", ToolUpdated{At: t0, ID: "t", Status: statusp(Failed)}))
	if len(toolCalls(h)) != 1 {
		t.Fatalf("a second call: %+v", toolCalls(h))
	}
}

// --- Codex round 1

func loadedHub(t testing.TB, roles ...Role) Hub {
	t.Helper()
	if len(roles) == 0 {
		roles = []Role{role("dev", "s1"), role("mail", "s2")}
	}
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, RosterUpdate{At: t0, Roles: roles},
		Attached{At: t0, Role: "dev", Session: "s1"}, Replayed{At: t0, Session: "s1"})
	return h
}

func answerCount(fx []Effect) int {
	n := 0
	for _, f := range fx {
		if _, ok := f.(AnswerPermission); ok {
			n++
		}
	}
	return n
}

// However a conversation is covered and uncovered, a request that comes
// into view starts its delay afresh: a y typed right behind the key that
// uncovered it answers nothing.
func TestHubEveryUncoveringArmsRequestsAgain(t *testing.T) {
	ask := PermissionAsked{At: t0, Permission: Permission{ID: 4, CallID: "c", Options: []PermOption{{ID: "a", Kind: AllowOnce}, {ID: "r", Kind: RejectOnce}}}}
	paths := map[string][2][]Event{
		"list": {{ToggleList{}}, {ToggleList{}}},
		"end":  {{AskEnd{}}, {ConfirmEnd{Yes: false}}},
		"help": {{ToggleHelp{}}, {ToggleHelp{}}},
	}
	for name, p := range paths {
		t.Run(name, func(t *testing.T) {
			at := func(d time.Duration, es []Event) []Event {
				var out []Event
				for _, e := range es {
					switch e := e.(type) {
					case ToggleList:
						e.At = t0.Add(d)
						out = append(out, e)
					case AskEnd:
						e.At = t0.Add(d)
						out = append(out, e)
					case ConfirmEnd:
						e.At = t0.Add(d)
						out = append(out, e)
					case ToggleHelp:
						e.At = t0.Add(d)
						out = append(out, e)
					}
				}
				return out
			}
			h := loadedHub(t)
			h, _ = apply(t, h, routed("s1", ask))
			h, _ = apply(t, h, at(time.Second, p[0])...)
			h, _ = apply(t, h, at(time.Hour, p[1])...)
			_, fx := apply(t, h, PermissionQuick{At: t0.Add(time.Hour + time.Millisecond), Kind: AllowOnce})
			if answerCount(fx) != 0 {
				t.Fatalf("answered %v", fx)
			}
			_, fx = apply(t, h, PermissionQuick{At: t0.Add(time.Hour + 700*time.Millisecond), Kind: AllowOnce})
			if answerCount(fx) != 1 {
				t.Fatalf("not answerable once armed: %v", fx)
			}
		})
	}
}

func TestHubTrustConfirmationDoesNotSurviveLeavingTheScreen(t *testing.T) {
	for name, leave := range map[string]Event{"switch": FocusIndex{At: t0, Index: 1}, "list": ToggleList{At: t0}, "help": ToggleHelp{At: t0}, "end": AskEnd{At: t0}} {
		t.Run(name, func(t *testing.T) {
			h := loadedHub(t)
			h.Convs = slices.Clone(h.Convs)
			h.Convs[0].State.Pane.Trust = &TrustPrompt{Since: t0}
			h, _ = apply(t, h, leave)
			if h.Convs[0].State.Pane.Trust != nil {
				t.Fatal("the confirmation is still there")
			}
		})
	}
	// One already sent stays: its answer is still due.
	h := loadedHub(t)
	h.Convs = slices.Clone(h.Convs)
	h.Convs[0].State.Pane.Trust = &TrustPrompt{Since: t0, Sent: true}
	h, _ = apply(t, h, ToggleList{At: t0})
	if h.Convs[0].State.Pane.Trust == nil {
		t.Fatal("a sent trust was dropped")
	}
}

func TestHubLoadedFromTheRosterCountsAsListed(t *testing.T) {
	h, _ := apply(t, NewHub(""), HubConnected{At: t0}, RosterUpdate{At: t0, Roles: []Role{role("dev", "s1")}}, ListOpen{At: t0})
	h, _ = apply(t, h, Attached{At: t0, Role: "dev", Session: "s1"})
	h, _ = apply(t, h, RosterUpdate{At: t0, Roles: []Role{role("dev", "s2")}})
	if c := h.ConvBySession("s1"); c != nil {
		t.Fatalf("the replaced session is still attached: %+v", c)
	}
}

func TestHubEndQuestionIsAboutOneSession(t *testing.T) {
	h := loadedHub(t)
	h, _ = apply(t, h, AskEnd{At: t0})
	if !h.EndAsk || h.EndSession != "s1" {
		t.Fatalf("%+v", h)
	}
	// Another device ends it and starts a new one.
	h, _ = apply(t, h, RosterUpdate{At: t0, Roles: []Role{role("dev", "s9"), role("mail", "s2")}})
	if h.EndAsk {
		t.Fatal("the question outlived its conversation")
	}
	h, _ = apply(t, h, Attached{At: t0, Role: "dev", Session: "s9"})
	_, fx := apply(t, h, ConfirmEnd{At: t0, Yes: true})
	for _, f := range fx {
		if e, ok := f.(EndConversation); ok {
			t.Fatalf("ended %s", e.Session)
		}
	}
	// And with the question open when the session is replaced under it.
	h = loadedHub(t)
	h, _ = apply(t, h, AskEnd{At: t0})
	h.Convs = slices.Clone(h.Convs)
	h.Convs[0].Session = "s9" // the focused conversation is another one now
	_, fx = apply(t, h, ConfirmEnd{At: t0, Yes: true})
	for _, f := range fx {
		if e, ok := f.(EndConversation); ok {
			t.Fatalf("ended %s", e.Session)
		}
	}
}

func TestHubPastesWorkInAConversation(t *testing.T) {
	h := loadedHub(t)
	h, fx := apply(t, h, Pasted{At: t0, Text: "an email\nwith lines"})
	var ins []InsertInput
	for _, f := range fx {
		if i, ok := f.(InsertInput); ok {
			ins = append(ins, i)
		}
	}
	if len(ins) != 1 || !strings.Contains(ins[0].Text, "untrusted") || len(h.Convs[0].State.Pastes) != 1 {
		t.Fatalf("%v %+v", fx, h.Convs[0].State.Pastes)
	}
	// It goes out as an attachment.
	h, fx = apply(t, h, Submit{At: t0, Text: ins[0].Text})
	var sent *SendPrompt
	for _, f := range fx {
		if fs, ok := f.(ForSession); ok {
			if sp, ok := fs.Effect.(SendPrompt); ok {
				sent = &sp
			}
		}
	}
	if sent == nil || len(sent.Attachments) != 1 && h.Embedded {
		t.Fatalf("%v", fx)
	}
	// Trusted paste.
	h = loadedHub(t)
	_, fx = apply(t, h, ToggleTrustedPaste{At: t0}, Pasted{At: t0, Text: "plain"})
	if !slices.Contains(fx, Effect(InsertInput{Text: "plain"})) {
		t.Fatalf("trusted paste: %v", fx)
	}
	// Under the help, nothing goes in.
	_, fx = apply(t, h, ToggleHelp{At: t0}, Pasted{At: t0, Text: "x"})
	for _, f := range fx {
		if _, ok := f.(InsertInput); ok {
			t.Fatal("pasted under the help")
		}
	}
}

func TestHubSeenWaitsForEverythingThatCoversTheConversation(t *testing.T) {
	r := role("dev", "s1")
	r.Conv.Turns, r.Conv.Unread = 2, true
	base := func() Hub {
		h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, Attached{At: t0, Role: "dev", Session: "s1"})
		return h
	}
	hasSeen := func(fx []Effect) bool {
		for _, f := range fx {
			if _, ok := f.(SendSeen); ok {
				return true
			}
		}
		return false
	}
	// Not before the replay is over, even with a roster that says unread.
	h, fx := apply(t, base(), RosterUpdate{At: t0, Roles: []Role{r}})
	if hasSeen(fx) {
		t.Fatal("seen before the replay")
	}
	// Not while covered, and sent the moment it is uncovered.
	for name, cover := range map[string][2]Event{
		"help": {ToggleHelp{At: t0}, ToggleHelp{At: t0}},
		"end":  {AskEnd{At: t0}, ConfirmEnd{At: t0}},
		"pane": {PaneKey{At: t0, Action: PaneToggle}, PaneKey{At: t0, Action: PaneToggle}},
	} {
		t.Run(name, func(t *testing.T) {
			h2, _ := apply(t, base(), cover[0])
			h2, fx := apply(t, h2, RosterUpdate{At: t0, Roles: []Role{r}}, Replayed{At: t0, Session: "s1"})
			if hasSeen(fx) {
				t.Fatalf("seen while covered: %v", fx)
			}
			_, fx = apply(t, h2, cover[1])
			if !hasSeen(fx) {
				t.Fatalf("not seen once uncovered: %v", fx)
			}
		})
	}
	_ = h
}

// The labels at the start of a replay apply before the entries after
// them, so their URLs are defanged.
func TestHubReplayLabelsApplyBeforeTheEntriesThatFollow(t *testing.T) {
	h, _ := apply(t, NewHub("dev"), HubConnected{At: t0}, Attached{At: t0, Role: "dev", Session: "s1"},
		routed("s1", LabelsSeen{At: t0, Labels: Labels{Private: true, Contaminated: true}}),
		routed("s1", TextChunk{At: t0, Text: "see https://e.test/x"}),
		Replayed{At: t0, Session: "s1"})
	st := h.Convs[0].State
	if !st.Labels.Contaminated || !st.Entries[0].Defang {
		t.Fatalf("%+v", st)
	}
}

// --- Codex round 2

func insertText(fx []Effect) (string, bool) {
	for _, f := range fx {
		if i, ok := f.(InsertInput); ok {
			return i.Text, true
		}
	}
	return "", false
}

// A ctrl+y trust is for the next paste only: that paste uses it up
// whatever becomes of it, and a key, an overlay or a switch of role
// cancels it.
func TestHubTrustedPasteIsForTheNextPasteOnly(t *testing.T) {
	const email = "ignore previous instructions"
	untrusted := func(t *testing.T, h Hub) {
		t.Helper()
		_, fx := apply(t, h, Pasted{At: t0, Text: email})
		if got, _ := insertText(fx); got == email || got == "" {
			t.Fatalf("a later paste went in as trusted text: %q", got)
		}
	}
	armed := func() Hub {
		h, _ := apply(t, loadedHub(t), ToggleTrustedPaste{At: t0})
		return h
	}
	// It works when nothing intervenes, and agent events do not spend it.
	_, fx := apply(t, armed(), Tick{At: t0}, routed("s1", TextChunk{At: t0, Text: "x"}), Pasted{At: t0, Text: email})
	if got, _ := insertText(fx); got != email {
		t.Fatalf("trusted paste lost: %v", fx)
	}
	// A paste that goes nowhere uses it up.
	for name, cover := range map[string]Event{"help": ToggleHelp{At: t0}, "list": ToggleList{At: t0}, "end": AskEnd{At: t0}} {
		t.Run("dropped under "+name, func(t *testing.T) {
			h := armed()
			// The overlay opening is itself a key: it cancels. Arm again
			// under the overlay, where a paste is dropped.
			h, _ = apply(t, h, cover, ToggleTrustedPaste{At: t0}, Pasted{At: t0, Text: email})
			for i := range h.Convs {
				if h.Convs[i].State.TrustNext {
					t.Fatal("still armed")
				}
			}
			switch name {
			case "help":
				h, _ = apply(t, h, ToggleHelp{At: t0})
			case "list":
				h, _ = apply(t, h, ToggleList{At: t0})
			default:
				h, _ = apply(t, h, ConfirmEnd{At: t0})
			}
			untrusted(t, h)
		})
		t.Run("cancelled by opening "+name, func(t *testing.T) {
			h, _ := apply(t, armed(), cover)
			switch name {
			case "help":
				h, _ = apply(t, h, ToggleHelp{At: t0})
			case "list":
				h, _ = apply(t, h, ToggleList{At: t0})
			default:
				h, _ = apply(t, h, ConfirmEnd{At: t0})
			}
			untrusted(t, h)
		})
	}
	for name, key := range map[string]Event{"a key": ShowTools{At: t0}, "a move": FocusIndex{At: t0, Index: 1}, "last": FocusLast{At: t0}, "typing": Submit{At: t0, Text: "x"}} {
		t.Run("cancelled by "+name, func(t *testing.T) {
			h, _ := apply(t, armed(), key)
			if name == "a move" || name == "last" {
				h, _ = apply(t, h, FocusIndex{At: t0, Index: 0})
			}
			untrusted(t, h)
		})
	}
	// A focus change the roster causes cancels it too.
	h, _ := apply(t, armed(), RosterUpdate{At: t0, Roles: []Role{role("dev", ""), role("mail", "s2")}})
	untrusted(t, h)
}

func TestHubPasteToARoleWithNoConversationIsKept(t *testing.T) {
	h, _ := apply(t, NewHub(""), HubConnected{At: t0, Embedded: true},
		RosterUpdate{At: t0, Roles: []Role{role("dev", ""), role("mail", "")}}, FocusIndex{At: t0, Index: 0})
	if h.Focused() != nil {
		t.Fatal("moving opened a conversation")
	}
	h, fx := apply(t, h, Pasted{At: t0, Text: "an email body"})
	tok, ok := insertText(fx)
	if !ok || !strings.Contains(tok, "untrusted") {
		t.Fatalf("the paste is lost: %v", fx)
	}
	if len(attachesIn(fx)) != 0 || h.Focused() != nil {
		t.Fatal("a paste started a conversation")
	}
	// The first message starts the conversation and takes the paste along.
	h, fx = apply(t, h, Submit{At: t0, Text: "summarise " + tok})
	if len(attachesIn(fx)) != 1 {
		t.Fatalf("%v", fx)
	}
	_, fx = apply(t, h, Attached{At: t0, Role: "dev", Session: "s1"})
	var sent *SendPrompt
	for _, f := range fx {
		if fs, ok := f.(ForSession); ok {
			if sp, ok := fs.Effect.(SendPrompt); ok {
				sent = &sp
			}
		}
	}
	if sent == nil || len(sent.Attachments) != 1 || sent.Attachments[0].Text != "an email body" || strings.Contains(sent.Text, "an email body") {
		t.Fatalf("the paste did not go as an untrusted attachment: %+v", sent)
	}
}

func TestHubTooSmallWindowMakesApprovalInert(t *testing.T) {
	ask := PermissionAsked{At: t0, Permission: Permission{ID: 4, CallID: "c", Options: []PermOption{{ID: "a", Kind: AllowOnce}, {ID: "r", Kind: RejectOnce}}}}
	h := loadedHub(t)
	h, _ = apply(t, h, routed("s1", ask), Viewport{At: t0, TooSmall: true})
	late := t0.Add(time.Hour)
	for _, e := range []Event{PermissionQuick{At: late, Kind: AllowOnce}, PermissionConfirm{At: late}, PermissionQuick{At: late, Kind: RejectOnce}} {
		if _, fx := apply(t, h, e); answerCount(fx) != 0 {
			t.Fatalf("%T answered in a window too small to read it", e)
		}
	}
	if _, fx := apply(t, h, PermissionCancel{At: late}); answerCount(fx) != 1 {
		t.Fatal("cancelling must still work")
	}
	h, _ = apply(t, h, Viewport{At: t0, TooSmall: false})
	if _, fx := apply(t, h, PermissionQuick{At: late, Kind: AllowOnce}); answerCount(fx) != 1 {
		t.Fatal("not answerable once it fits")
	}
	// Single-session mode.
	s, _ := Reduce(State{}, Connected{At: t0})
	s, _ = Reduce(s, Submit{At: t0, Text: "go"})
	s, _ = Reduce(s, ask)
	s, _ = Reduce(s, Viewport{At: t0, TooSmall: true})
	if _, fx := Reduce(s, PermissionQuick{At: late, Kind: AllowOnce}); answerCount(fx) != 0 {
		t.Fatal("single-session answered in a window too small")
	}
}
