package render

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/kirahowe/acp-tui/internal/core"
)

func rosterFixture() []core.Role {
	return []core.Role{
		{ID: "dev", Title: "Dev", Conv: &core.RosterConv{Session: "s1", Turn: core.TurnIdle, Turns: 3, Unread: true,
			LastActivity: t0.Add(-5 * time.Minute)}},
		{ID: "mail", Title: "Mail", Labels: core.Labels{Private: true}, Holds: 2, Conv: &core.RosterConv{Session: "s2", Turn: core.TurnQueued, Turns: 1,
			Approvals: 1, LastActivity: t0.Add(-time.Hour)}},
		{ID: "garden", Title: "Garden planner", Working: true},
		{ID: "news", Title: "News", Conv: &core.RosterConv{Session: "s3", Turn: core.TurnRunning, Turns: 7,
			LastActivity: t0.Add(-10 * time.Second)}},
	}
}

func hubStep(h core.Hub, es ...core.Event) core.Hub {
	for _, e := range es {
		h, _ = core.ReduceHub(h, e)
	}
	return h
}

// hubFixture is a hub with a conversation in focus.
func hubFixture() core.Hub {
	ask := core.PermissionAsked{At: t0, Permission: core.Permission{ID: 1, CallID: "c", Name: "send_mail", Input: `{"to":"a@example.com"}`,
		Options: []core.PermOption{{ID: "a", Name: "Allow", Kind: core.AllowOnce}, {ID: "r", Name: "Reject", Kind: core.RejectOnce}}}}
	h := hubStep(core.NewHub("dev"),
		core.HubConnected{At: t0, Agent: "relay"},
		core.RosterUpdate{At: t0, Roles: rosterFixture()},
		core.Attached{At: t0, Role: "dev", Session: "s1"},
		core.Routed{Session: "s1", Event: core.UserChunk{At: t0, Text: "What is on today?"}},
		core.Routed{Session: "s1", Event: core.TextChunk{At: t0, Text: "Two **meetings** and a dentist visit."}},
		core.FocusIndex{At: t0, Index: 1},
		core.Attached{At: t0, Role: "mail", Session: "s2"},
		core.Routed{Session: "s2", Event: ask},
		core.FocusIndex{At: t0, Index: 0})
	return h
}

func TestHubGolden(t *testing.T) {
	now := t0.Add(time.Minute)
	h := hubFixture()
	list := hubStep(h, core.ToggleList{At: t0})
	asking := hubStep(h, core.FocusIndex{At: t0, Index: 1})
	ending := hubStep(h, core.AskEnd{At: t0})
	helped := hubStep(h, core.ToggleHelp{At: t0})
	empty := hubStep(h, core.FocusIndex{At: t0, Index: 2})
	for _, c := range []struct {
		name string
		h    core.Hub
	}{{"conversation", h}, {"list", list}, {"request", asking}, {"ending", ending}, {"help", helped}, {"no_conversation", empty}} {
		for _, w := range []int{39, 99} {
			t.Run(fmt.Sprintf("%s_%d", c.name, w), func(t *testing.T) {
				f := HubView(c.h, strings.Repeat(" ", InputWidth(HubMainWidth(w))), "ctrl+j for a new line", now, w, 16)
				requireGolden(t, f.Content)
			})
		}
	}
}

// The frame always has exactly the rows asked for, none wider than the
// width, at any size and in any view.
func TestHubFrameFits(t *testing.T) {
	h := hubFixture()
	views := map[string]core.Hub{
		"conversation": h,
		"list":         hubStep(h, core.ToggleList{At: t0}),
		"request":      hubStep(h, core.FocusIndex{At: t0, Index: 1}),
		"ending":       hubStep(h, core.AskEnd{At: t0}),
		"help":         hubStep(h, core.ToggleHelp{At: t0}),
		"none":         hubStep(h, core.FocusIndex{At: t0, Index: 2}),
		"fresh":        core.NewHub(""),
	}
	for name, v := range views {
		for _, w := range []int{1, 8, 20, 40, 71, 72, 99, 160} {
			for _, rows := range []int{2, 5, 10, 24} {
				f := HubView(v, "", "hint", t0, w, rows)
				lines := strings.Split(f.Content, "\n")
				if len(lines) != rows {
					t.Errorf("%s %dx%d: %d rows", name, w, rows, len(lines))
				}
				for i, l := range lines {
					if got := ansi.StringWidth(l); got > w {
						t.Errorf("%s %dx%d: row %d is %d wide: %q", name, w, rows, i, got, l)
					}
				}
			}
		}
	}
}

func TestHubNarrowShowsOneOrTheOther(t *testing.T) {
	h := hubFixture()
	conv := HubView(h, "", "", t0, 50, 14).Content
	if strings.Contains(conv, "Garden planner") || !strings.Contains(conv, "ctrl+g roles") || !strings.Contains(conv, "dentist") {
		t.Errorf("conversation screen:\n%s", conv)
	}
	list := HubView(hubStep(h, core.ToggleList{At: t0}), "", "", t0, 50, 14).Content
	if strings.Contains(list, "dentist") || !strings.Contains(list, "Garden planner") {
		t.Errorf("list screen:\n%s", list)
	}
	// Wide shows both.
	wide := HubView(h, "", "", t0, 100, 14).Content
	if !strings.Contains(wide, "Garden planner") || !strings.Contains(wide, "dentist") {
		t.Errorf("wide screen:\n%s", wide)
	}
}

func TestHubMarksStandOut(t *testing.T) {
	h := hubFixture()
	f := HubView(h, "", "", t0, 100, 14).Content
	plain := ansi.Strip(f)
	for _, want := range []string{"?1", "!2", "●", "▶", "…", "~"} {
		if !strings.Contains(plain, want) {
			t.Errorf("sidebar lacks %q:\n%s", want, plain)
		}
	}
	// The request waiting in Mail is in reverse video.
	if !strings.Contains(f, "\x1b[0;1;7m?1") {
		t.Errorf("approvals not in reverse video:\n%q", f)
	}
}

func TestHubQueuedTurnIsSaid(t *testing.T) {
	h := hubStep(hubFixture(), core.FocusIndex{At: t0, Index: 1})
	f := ansi.Strip(HubView(h, "", "", t0, 100, 24).Content)
	if !strings.Contains(f, "waiting for the role's scheduled work") {
		t.Errorf("the queued turn is not said:\n%s", f)
	}
	list := ansi.Strip(HubView(hubStep(h, core.ToggleList{At: t0}), "", "", t0, 50, 14).Content)
	if !strings.Contains(list, "waiting for scheduled work") {
		t.Errorf("the list does not say it:\n%s", list)
	}
}

// Hostile role names are shown with their controls made harmless.
func TestHubRoleTitlesAreSanitized(t *testing.T) {
	h := hubStep(core.NewHub(""), core.HubConnected{At: t0},
		core.RosterUpdate{At: t0, Roles: []core.Role{{ID: "x", Title: "bad\x1b[31m\x07\nname"}}})
	f := HubView(h, "", "", t0, 100, 8).Content
	if strings.Contains(f, "\x1b[31m") || strings.Contains(f, "\x07") {
		t.Errorf("escape got through: %q", f)
	}
}

// A conversation can be on screen before the roster has come: --open
// attaches at once.
func TestHubDrawsAConversationBeforeTheRoster(t *testing.T) {
	h := hubStep(core.NewHub("dev"), core.HubConnected{At: t0}, core.Attached{At: t0, Role: "dev", Session: "s1"},
		core.Routed{Session: "s1", Event: core.TextChunk{At: t0, Text: "early"}})
	for _, w := range []int{30, 100} {
		if f := HubView(h, "", "", t0, w, 10).Content; !strings.Contains(ansi.Strip(f), "early") {
			t.Errorf("%d wide:\n%s", w, f)
		}
	}
}

// parityState holds one of every kind of entry, a contaminated session
// and a display in the workspace pane.
func parityState() core.State {
	return core.State{
		Phase: core.Idle, Shared: true, Transcript: core.Transcript{Open: true},
		Labels: core.Labels{Private: true, Contaminated: true},
		Entries: []core.Entry{
			{Kind: core.Banner, Title: "Clerk", Text: "relay 1 · model", Final: true},
			{Kind: core.User, Text: "Find venues near https://example.com/x", Final: true},
			{Kind: core.Thought, Text: "thinking about halls", Final: true},
			{Kind: core.Tool, Calls: []core.Call{{ID: "1", Name: "search", Status: core.OK}, {ID: "2", Name: "fetch", Status: core.Failed, Output: "timed out"}}, Final: true},
			{Kind: core.Agent, Text: "Found **three** at https://example.com/hall", Final: true, Defang: true},
			{Kind: core.Note, Text: "! rate limited", Detail: "retrying", Final: true},
			{Kind: core.Request, Text: "send_mail", Detail: `{"to":"a"}`, Preview: "Will mail a@example.com", PreviewDefang: true, Final: true},
			{Kind: core.Note, Text: "[max tokens]", Final: true},
		},
		Pane: core.Pane{Local: []core.Item{{Kind: core.ShownItem, CallID: "1", Title: "Venues near the hall", Text: "body",
			Labels: core.Labels{Contaminated: true}}}},
	}
}

func hubOf(st core.State) core.Hub {
	return core.Hub{Connected: true, Focus: "clerk", RosterLoaded: true,
		Roles: []core.Role{{ID: "clerk", Title: "Clerk"}},
		Convs: []core.Conv{{Role: "clerk", Session: "s", Attached: true, State: st}}}
}

// The conversation in hub mode shows every line of text the inline view
// prints for the same entries, at the same width.
func TestHubConversationShowsWhatTheInlineViewShows(t *testing.T) {
	st := parityState()
	for _, w := range []int{40, 60} {
		inline := Entries(nil, st.Entries, w, t0)
		got := ansi.Strip(HubView(hubOf(st), "", "", t0, w, 80).Content)
		for _, l := range inline {
			l = strings.TrimSpace(ansi.Strip(l))
			if l == "" || strings.Trim(l, "╭╮╰╯─│ ") == "" { // blank rows and box edges are chrome
				continue
			}
			if !strings.Contains(got, l) {
				t.Errorf("%d wide: the inline view prints %q, the hub view does not:\n%s", w, l, got)
			}
		}
	}
}

// A display is in front of the operator on a wide screen without a key,
// as in the single-session view, with its untrusted mark.
func TestHubShowsTheDisplayBesideTheConversation(t *testing.T) {
	st := parityState()
	st.Entries = st.Entries[:5]
	inline := ansi.Strip(View(st, "", "", t0, 159, 50).Content)
	hub := ansi.Strip(HubView(hubOf(st), "", "", t0, 159, 50).Content)
	for _, want := range []string{"Venues near the hall", "untrusted"} {
		if !strings.Contains(inline, want) {
			t.Fatalf("the inline view lacks %q:\n%s", want, inline)
		}
		if !strings.Contains(hub, want) {
			t.Errorf("the hub view lacks %q:\n%s", want, hub)
		}
	}
	// Narrower than that the pane waits for ctrl+x, as inline.
	if narrow := ansi.Strip(HubView(hubOf(st), "", "", t0, 99, 50).Content); strings.Contains(narrow, "Venues near the hall") {
		t.Errorf("the pane stood beside a conversation too narrow for it:\n%s", narrow)
	}
}

func TestHubShowsTheSessionsLabels(t *testing.T) {
	got := ansi.Strip(HubView(hubOf(parityState()), "", "", t0, 60, 40).Content)
	for _, want := range []string{"[private]", "contaminated", "hxxps://"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}

// Role titles are text from outside the session's trust: they are shown
// as session text is, by the role's labels.
func TestHubRoleTitlesFollowTheirLabels(t *testing.T) {
	title := "Evil https://e.test/?k=secret ‮​\x1b[31m end"
	roles := []core.Role{{ID: "evil", Title: title, Labels: core.Labels{Private: true, Contaminated: true}}}
	h := hubStep(core.NewHub(""), core.HubConnected{At: t0}, core.RosterUpdate{At: t0, Roles: roles})
	check := func(name, got string) {
		t.Helper()
		if strings.Contains(got, "https://e.test") {
			t.Errorf("%s: a live link:\n%s", name, got)
		}
		if strings.Contains(got, "‮") || strings.Contains(got, "​") || strings.Contains(got, "\x1b[31m") {
			t.Errorf("%s: an invisible or control character got through: %q", name, got)
		}
		if !strings.Contains(ansi.Strip(got), "hxxps://e[.]test") {
			t.Errorf("%s: the title is not shown defanged:\n%s", name, ansi.Strip(got))
		}
	}
	check("sidebar", HubView(hubStep(h, core.ListOpen{At: t0}), "", "", t0, 140, 10).Content)
	// The list screen (narrow) shows it and its labels.
	list := HubView(h, "", "", t0, 120+1, 10).Content
	check("list", list)
	narrowList := HubView(h, "", "", t0, 60, 10).Content
	check("narrow list", narrowList)
	if p := ansi.Strip(narrowList); !strings.Contains(p, "contaminated") || !strings.Contains(p, "private") {
		t.Errorf("the list does not say what the labels are:\n%s", p)
	}
	conv := hubStep(h, core.FocusIndex{At: t0, Index: 0})
	f := HubView(conv, "", "", t0, 140, 10).Content
	check("header", f)
	if !strings.Contains(f, "contaminated") || !strings.Contains(f, "[private]") {
		t.Errorf("the header lacks the role's labels:\n%s", ansi.Strip(f))
	}
	// A role the roster does not know is the command line's word.
	h2 := hubStep(core.NewHub("https://x.test/‮"), core.HubConnected{At: t0})
	if g := HubView(h2, "", "", t0, 100, 8).Content; strings.Contains(g, "https://x.test") || strings.Contains(g, "‮") {
		t.Errorf("an unlisted name: %q", g)
	}
}

// The text area is the inside of the box that is drawn, in every layout.
func TestHubInputWidthIsTheBoxDrawn(t *testing.T) {
	h := hubFixture()
	for _, w := range []int{30, 60, 71, 72, 99, 150, 158, 159, 200} {
		iw := HubInputWidth(h, w)
		line := strings.Repeat("x", iw)
		got := ansi.Strip(HubView(h, line, "", t0, w, 20).Content)
		if !strings.Contains(got, line) {
			t.Errorf("%d wide: a %d wide input line is cut:\n%s", w, iw, got)
		}
		// And no wider than the box: one more would be cut.
		if strings.Contains(got, line+"x") {
			t.Errorf("%d wide: the box is wider than the text area (%d)", w, iw)
		}
	}
}

// --- Codex round 2

func longRequest(h core.Hub, title string) core.Hub {
	return hubStep(h, core.Routed{Session: "s1", Event: core.PermissionAsked{At: t0, Permission: core.Permission{
		ID: 1, CallID: "c", Name: "send_mail", Title: title, Input: `{"to":"a@example.com","body":"` + strings.Repeat("word ", 40) + `"}`,
		Options: []core.PermOption{{ID: "a", Name: "Allow", Kind: core.AllowOnce}, {ID: "r", Name: "Reject", Kind: core.RejectOnce}}}}})
}

// A request with a 500 character title still shows whose it is, the
// options and the keys, or, in a window too small for that, says so.
func TestHubLongRequestTitleKeepsTheRoleAndTheOptionsOnScreen(t *testing.T) {
	title := strings.Repeat("send everything ", 32)[:500]
	h := longRequest(hubFixture(), title)
	for _, size := range [][2]int{{40, 26}, {40, 30}, {60, 24}, {100, 30}} {
		w, rows := size[0], size[1]
		f := HubView(h, "", "", t0.Add(time.Second), w, rows)
		got := ansi.Strip(f.Content)
		if got = strings.TrimSpace(got); len(strings.Split(f.Content, "\n")) != rows {
			t.Errorf("%dx%d: %d rows", w, rows, len(strings.Split(f.Content, "\n")))
		}
		if f.TooSmall {
			t.Errorf("%dx%d: too small, but a request fits there:\n%s", w, rows, got)
			continue
		}
		for _, want := range []string{"Dev", "Allow (y)", "Reject (n)", "esc cancel"} {
			if !strings.Contains(got, want) {
				t.Errorf("%dx%d: %q is not on screen:\n%s", w, rows, want, got)
			}
		}
	}
	// Where it does not fit, the frame says so and names the role.
	for _, size := range [][2]int{{40, 8}, {40, 5}, {20, 6}, {40, 2}, {34, 12}, {40, 16}} {
		f := HubView(h, "", "", t0.Add(time.Second), size[0], size[1])
		if !f.TooSmall {
			t.Errorf("%v: a request cannot be shown whole here:\n%s", size, ansi.Strip(f.Content))
		}
		if n := len(strings.Split(f.Content, "\n")); n != size[1] {
			t.Errorf("%v: %d rows", size, n)
		}
		if size[1] >= 4 && !strings.Contains(ansi.Strip(f.Content), "too small") {
			t.Errorf("%v: it does not say so:\n%s", size, ansi.Strip(f.Content))
		}
	}
}

// The single-session view has the same bound.
func TestInlineLongRequestTitleIsBounded(t *testing.T) {
	title := strings.Repeat("send everything ", 32)[:500]
	s, _ := core.Reduce(core.State{}, core.Connected{At: t0})
	s, _ = core.Reduce(s, core.Submit{At: t0, Text: "go"})
	s, _ = core.Reduce(s, core.PermissionAsked{At: t0, Permission: core.Permission{ID: 1, Name: "send_mail", Title: title, Input: `{"a":1}`,
		Options: []core.PermOption{{ID: "a", Name: "Allow", Kind: core.AllowOnce}, {ID: "r", Name: "Reject", Kind: core.RejectOnce}}}})
	f := View(s, "› ", "", t0.Add(time.Second), 39, 16)
	got := ansi.Strip(f.Content)
	if f.TooSmall || !strings.Contains(got, "Allow (y)") || !strings.Contains(got, "Reject (n)") || !strings.Contains(got, "permission needed") {
		t.Errorf("a 500 character title pushed the request off the screen:\n%s", got)
	}
	if f := View(s, "› ", "", t0.Add(time.Second), 39, 5); !f.TooSmall || !strings.Contains(ansi.Strip(f.Content), "too small") {
		t.Errorf("no word that the window is too small:\n%s", ansi.Strip(f.Content))
	}
	// v puts the whole title in the transcript.
	s, _ = core.Reduce(s, core.PermissionShowAll{At: t0})
	all := ansi.Strip(strings.Join(Entries(nil, s.Entries, 80, t0), " "))
	if !strings.Contains(strings.Join(strings.Fields(all), ""), strings.Join(strings.Fields(title), "")) {
		t.Errorf("the full title is not reachable:\n%s", all)
	}
}

// Whenever the keys that approve are live, the whole request can be read:
// after v, scrolling brings every line of it on screen. Anything else is
// "too small". One rule decides (core.RequestFits), whatever the size.
func TestHubApprovalIsLiveOnlyWhereTheWholeRequestCanBeRead(t *testing.T) {
	title := strings.Repeat("send everything ", 32)[:500]
	base := longRequest(hubFixture(), title)
	live := 0
	for w := 12; w < 72; w += 7 {
		for rows := 2; rows <= 46; rows += 2 {
			f := HubView(base, "", "", t0.Add(time.Second), w, rows)
			if f.TooSmall {
				continue
			}
			live++
			h := hubStep(base, core.PermissionShowAll{At: t0})
			c := h.Focused().State
			want := Entries(nil, c.Entries[len(c.Entries)-1:], w, t0)
			// Every line of the request, wherever the view scrolls.
			seen := map[string]bool{}
			for scroll := 0; scroll <= HubView(h, "", "", t0.Add(time.Second), w, rows).Max; scroll++ {
				hs := h
				hs.Convs = append([]core.Conv(nil), h.Convs...)
				for i := range hs.Convs {
					if hs.Convs[i].Role == hs.Focus {
						hs.Convs[i].State.Transcript.Scroll = scroll
					}
				}
				for _, l := range strings.Split(ansi.Strip(HubView(hs, "", "", t0.Add(time.Second), w, rows).Content), "\n") {
					seen[strings.TrimSpace(l)] = true
				}
			}
			for _, l := range want {
				if l = strings.TrimSpace(ansi.Strip(l)); l != "" && !seen[l] {
					t.Fatalf("%dx%d: approval is live but %q cannot be read", w, rows, l)
				}
			}
		}
	}
	if live == 0 {
		t.Fatal("no size was live: the sweep checks nothing")
	}
}

// The list scrolls to keep the selected role in sight, on the narrow list
// screen and in the sidebar.
func TestHubListScrollsToTheSelectedRole(t *testing.T) {
	h := hubStep(core.NewHub(""), core.HubConnected{At: t0}, core.RosterUpdate{At: t0, Roles: rosterFixture()})
	for i := 0; i < 3; i++ {
		h = hubStep(h, core.ListMove{At: t0, Delta: 1})
	}
	narrow := ansi.Strip(HubView(h, "", "", t0, 39, 7).Content)
	if !strings.Contains(narrow, "News") || strings.Contains(narrow, "Dev") {
		t.Errorf("narrow list at 39x7 with the cursor on News:\n%s", narrow)
	}
	if n := len(strings.Split(narrow, "\n")); n != 7 {
		t.Errorf("%d rows", n)
	}
	// Back at the first, the first shows.
	h = hubStep(h, core.ListMove{At: t0, Delta: 1}) // wraps to Dev
	if top := ansi.Strip(HubView(h, "", "", t0, 39, 7).Content); !strings.Contains(top, "Dev") {
		t.Errorf("wrapped to Dev, but:\n%s", top)
	}
	// The sidebar, with the conversation in focus on the last role.
	f := hubStep(core.NewHub("news"), core.HubConnected{At: t0}, core.RosterUpdate{At: t0, Roles: rosterFixture()})
	side := ansi.Strip(HubView(f, "", "", t0, 100, 5).Content)
	if !strings.Contains(side, "› News") && !strings.Contains(side, "News") {
		t.Errorf("the sidebar at 100x5 lost the role in focus:\n%s", side)
	}
	if strings.Contains(strings.Split(side, "\n")[1], "Dev") {
		t.Errorf("the sidebar did not scroll:\n%s", side)
	}
}
