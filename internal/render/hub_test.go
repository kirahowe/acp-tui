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
	f := ansi.Strip(HubView(h, "", "", t0, 100, 14).Content)
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
