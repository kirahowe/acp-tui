package core

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// feed applies events in turn and returns the state and all the effects.
func feed(s State, events ...Event) (State, []Effect) {
	var all []Effect
	for _, e := range events {
		var fx []Effect
		s, fx = Reduce(s, e)
		all = append(all, fx...)
	}
	return s, all
}

func effectsOf[T Effect](fx []Effect) []T {
	var out []T
	for _, f := range fx {
		if v, ok := f.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func key(at time.Time, a PaneAction) PaneKey { return PaneKey{At: at, Action: a} }

var trustable = Item{
	Kind: ShownItem, Ref: "shown:2026-10-18/x", Path: "shown/2026-10-18/x.md", CallID: "x", Title: "Venues",
	Text: "see https://example.org/v", Sources: []Source{{Title: "List", Ref: "web:https://example.org/list"}},
	Labels: Labels{Private: true, Contaminated: true}, Version: "412", SHA256: "abc123def456abc123",
	CanTrust: true, TrustCommand: "relay trust doc shown/2026-10-18/x.md",
}

// focused is a connected session with relay's list loaded and the pane
// focused on the first item.
func focused(items ...Item) State {
	s, _ := feed(State{}, Connected{At: t0}, ItemsLoaded{At: t0, Items: items}, key(t0, PaneToggle))
	return s
}

func TestParseRefs(t *testing.T) {
	got := ParseRefs("see [[fact:12]] and [[doc:a b/c.md]], again [[fact:12]]; not [[nope:1]] or [[fact:]] or [[fact:a\nb]] or [fact:1]")
	want := []string{"[[fact:12]]", "[[doc:a b/c.md]]"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Any id, once its brackets, newline and percent are escaped, is found
// whole in the middle of prose.
func TestParseRefsFindsEscapedIDs(t *testing.T) {
	kinds := []string{"fact", "doc", "note", "memo", "journal", "rem", "file", "web", "shown", "report"}
	rapid.Check(t, func(t *rapid.T) {
		kind := rapid.SampledFrom(kinds).Draw(t, "kind")
		raw := rapid.StringN(1, 20, -1).Draw(t, "id")
		id := strings.NewReplacer("%", "%25", "[", "%5B", "]", "%5D", "\n", "%0A").Replace(raw)
		ref := "[[" + kind + ":" + id + "]]"
		got := ParseRefs("before " + ref + " after")
		if !slices.Equal(got, []string{ref}) {
			t.Fatalf("ParseRefs found %q in %q", got, ref)
		}
	})
}

func TestHosts(t *testing.T) {
	got := Hosts("a https://User@Example.ORG:8080/x?y=1, www.other.net. and hxxps://nope/z")
	want := []string{"example.org", "www.other.net", "nope"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func displayEvent(id, title string, labels Labels) ToolUpdated {
	return ToolUpdated{At: t0, ID: id, Displays: []Display{{Title: title, Body: "body " + title, Labels: labels, Ref: "shown:" + id}}}
}

func TestDisplaysFillThePaneNewestFirst(t *testing.T) {
	s := busyState()
	var fx []Effect
	s, fx = feed(s, displayEvent("a", "first", Labels{}), displayEvent("b", "second", Labels{Contaminated: true}))
	items := PaneItems(s)
	if len(items) != 2 || items[0].Title != "second" || items[1].Title != "first" {
		t.Fatalf("items %#v", items)
	}
	if n := len(effectsOf[RequestItems](fx)); n != 2 {
		t.Errorf("%d item requests, want one per display", n)
	}
	// The same display again replaces itself.
	s, _ = feed(s, displayEvent("a", "first", Labels{}))
	if len(PaneItems(s)) != 2 {
		t.Errorf("a repeated display added an item: %#v", PaneItems(s))
	}
}

func TestRelayListIsAuthoritative(t *testing.T) {
	s, fx := feed(State{}, Connected{At: t0})
	if len(effectsOf[RequestItems](fx)) != 1 {
		t.Fatalf("no item request on connect: %#v", fx)
	}
	s, _ = feed(s, Submit{At: t0, Text: "go"}, displayEvent("a", "local", Labels{}))
	s, _ = feed(s, ItemsLoaded{At: t0, Items: []Item{{Kind: ShownItem, Ref: "shown:r", Title: "from relay"}}})
	if items := PaneItems(s); len(items) != 1 || items[0].Title != "from relay" {
		t.Fatalf("items %#v", items)
	}
	// A failed call leaves the list alone.
	s, _ = feed(s, ItemsLoaded{At: t0, Failed: true})
	if len(PaneItems(s)) != 1 {
		t.Fatalf("a failure changed the list: %#v", PaneItems(s))
	}
}

func TestPlainAgentFallsBackAndStopsAsking(t *testing.T) {
	s, _ := feed(busyStateRaw(), ItemsLoaded{At: t0, Unavailable: true})
	s, fx := feed(s, displayEvent("a", "local", Labels{}), TextChunk{At: t0, Text: "see [[fact:1]]\n\n"},
		PermissionAsked{At: t0, Permission: Permission{ID: 1, CallID: "c", Name: "t"}})
	if n := len(effectsOf[RequestItems](fx)) + len(effectsOf[RequestResolve](fx)); n != 0 {
		t.Errorf("%d relay requests after method-not-found", n)
	}
	items := PaneItems(s)
	if len(items) != 2 || items[0].Kind != ProposalItem || items[0].Status != StatusWaitingProposal || items[1].Title != "local" {
		t.Fatalf("items %#v", items)
	}
	s, _ = feed(s, key(t0, PaneToggle)) // refused: a request waits
	if s.Pane.Focus {
		t.Error("the pane took focus while a request waited")
	}
}

// busyStateRaw is a busy session whose connect request has not been
// answered yet.
func busyStateRaw() State { return busyState() }

func TestCitationsAreResolvedOnce(t *testing.T) {
	s := busyState()
	var all []Effect
	var fx []Effect
	for _, chunk := range []string{"The hall [[fa", "ct:12]] seats 900, ", "[[fact:12]] and [[web:https://x.test/a]].\n\n", "More [[fact:12]]."} {
		s, fx = feed(s, TextChunk{At: t0, MessageID: "m", Text: chunk})
		all = append(all, fx...)
	}
	var refs []string
	for _, r := range effectsOf[RequestResolve](all) {
		refs = append(refs, r.Ref)
	}
	if want := []string{"[[fact:12]]", "[[web:https://x.test/a]]"}; !slices.Equal(refs, want) {
		t.Fatalf("resolve requests %q, want %q", refs, want)
	}
	if c := s.Pane.Cite("[[fact:12]]"); c.Status != "" {
		t.Errorf("a pending citation reads %+v", c)
	}
	s, _ = feed(s, CitationResolved{At: t0, Cite: Citation{Ref: "[[fact:12]]", Kind: "fact", Status: CiteOK, Title: "Fact 12"}})
	if c := s.Pane.Cite("[[fact:12]]"); c.Title != "Fact 12" || c.Status != CiteOK {
		t.Errorf("resolved citation %+v", c)
	}
	// Only the latest message's references are the current ones.
	s, _ = feed(s, TextChunk{At: t0, MessageID: "m2", Text: "Now [[doc:b]]."})
	if got := LatestRefs(s); !slices.Equal(got, []string{"[[doc:b]]"}) {
		t.Errorf("latest refs %q", got)
	}
}

func TestPermissionFlowStillGuardsApproval(t *testing.T) {
	s := focused(trustable)
	s, _ = feed(s, PermissionAsked{At: t0, Permission: Permission{ID: 1, CallID: "c", Name: "send", Preview: "does X at https://send.example.net/a",
		Options: []PermOption{{ID: "a", Kind: AllowOnce}, {ID: "r", Kind: RejectOnce}}}})
	if s.Pane.Focus {
		t.Error("the pane kept focus under a request")
	}
	// Keys for the pane do nothing while the request waits.
	s2, fx := feed(s, key(t0.Add(time.Hour), PaneYes), key(t0.Add(time.Hour), PaneEnter), key(t0.Add(time.Hour), PaneTrust))
	if len(fx) != 0 || len(s2.Asks) != 1 {
		t.Fatalf("pane keys changed a waiting request: %#v", fx)
	}
	// The existing flow answers, after the arm delay, and the domains of
	// what was approved become known.
	if s.Pane.NewHosts("https://send.example.net/a")[0] != "send.example.net" {
		t.Fatal("the host is known before approval")
	}
	s3, fx := feed(s, PermissionQuick{At: t0.Add(ArmDelay / 2), Kind: AllowOnce})
	if len(answers(fx)) != 0 {
		t.Fatal("answered before the arm delay")
	}
	s3, fx = feed(s3, PermissionQuick{At: t0.Add(ArmDelay), Kind: AllowOnce})
	if len(answers(fx)) != 1 || len(effectsOf[RequestItems](fx)) != 1 {
		t.Fatalf("effects %#v", fx)
	}
	if got := s3.Pane.NewHosts("https://send.example.net/a"); len(got) != 0 {
		t.Errorf("the approved host is still new: %q", got)
	}
	if st := s3.Pane.Local[len(s3.Pane.Local)-1].Status; st != StatusAllowed {
		t.Errorf("status %q", st)
	}
	// A rejection vouches for nothing.
	s4, _ := feed(s, PermissionQuick{At: t0.Add(ArmDelay), Kind: RejectOnce})
	if len(s4.Pane.NewHosts("https://send.example.net/a")) != 1 {
		t.Error("a rejected call's host became known")
	}
}

func TestTrustFlow(t *testing.T) {
	s := focused(trustable)
	s, _ = feed(s, key(t0, PaneTrust))
	if s.Pane.Trust == nil || s.Pane.Trust.Item.Path != trustable.Path {
		t.Fatalf("no confirmation: %+v", s.Pane)
	}
	// y before the delay does nothing, and keeps the confirmation.
	early, fx := feed(s, key(t0.Add(ArmDelay-time.Millisecond), PaneYes))
	if len(fx) != 0 || early.Pane.Trust == nil {
		t.Fatalf("armed early: %#v", fx)
	}
	// Armed, y sends what was listed.
	s, fx = feed(s, key(t0.Add(ArmDelay), PaneYes))
	send := effectsOf[SendTrust](fx)
	if len(send) != 1 || send[0] != (SendTrust{Path: trustable.Path, Version: "412", SHA256: trustable.SHA256}) {
		t.Fatalf("effects %#v", fx)
	}
	// While it is out, keys do nothing.
	if s2, fx := feed(s, key(t0.Add(time.Hour), PaneYes), key(t0.Add(time.Hour), PaneEsc)); len(fx) != 0 || s2.Pane.Trust == nil {
		t.Fatal("a key acted while the trust was out")
	}
	s, fx = feed(s, TrustDone{At: t0, Path: trustable.Path})
	if s.Pane.Trust != nil || s.Pane.Message != "Trusted." || len(effectsOf[RequestItems](fx)) != 1 {
		t.Fatalf("after trust: %+v %#v", s.Pane, fx)
	}
	if got := s.Pane.NewHosts("https://example.org/v https://example.org/list"); len(got) != 0 {
		t.Errorf("trusted hosts are still new: %q", got)
	}
}

func TestTrustCancelsOnAnyOtherKey(t *testing.T) {
	for _, a := range []PaneAction{PaneOther, PaneUp, PaneDown, PaneEsc, PaneEnter, PaneTrust, PaneToggle} {
		s := focused(trustable)
		s, _ = feed(s, key(t0, PaneTrust))
		s, fx := feed(s, key(t0.Add(time.Hour), a))
		if s.Pane.Trust != nil || len(effectsOf[SendTrust](fx)) != 0 {
			t.Errorf("action %d left the confirmation or sent a trust", a)
		}
		// And a late y afterwards sends nothing.
		_, fx = feed(s, key(t0.Add(2*time.Hour), PaneYes))
		if len(effectsOf[SendTrust](fx)) != 0 {
			t.Errorf("action %d: a y after the cancel sent a trust", a)
		}
	}
}

func TestTrustFailureShowsTheCommand(t *testing.T) {
	s := focused(trustable)
	s, _ = feed(s, key(t0, PaneTrust), key(t0.Add(ArmDelay), PaneYes))
	s, _ = feed(s, TrustDone{At: t0, Path: trustable.Path, Err: "the doc changed"})
	if s.Pane.Failure != "the doc changed" || s.Pane.Command != trustable.TrustCommand || s.Pane.Trust != nil {
		t.Fatalf("pane %+v", s.Pane)
	}
}

func TestTrustWithoutCanTrustShowsTheCommandOnly(t *testing.T) {
	it := trustable
	it.CanTrust = false
	s := focused(it)
	s, fx := feed(s, key(t0, PaneTrust))
	if s.Pane.Trust != nil || s.Pane.Command != it.TrustCommand || len(fx) != 0 {
		t.Fatalf("pane %+v, effects %#v", s.Pane, fx)
	}
	if _, fx = feed(s, key(t0.Add(time.Hour), PaneYes)); len(fx) != 0 {
		t.Fatalf("a y sent %#v", fx)
	}
}

func TestTrustUnavailableWithoutRelay(t *testing.T) {
	s := busyState()
	s, _ = feed(s, ItemsLoaded{At: t0, Unavailable: true}, displayEvent("a", "x", Labels{Contaminated: true}), key(t0, PaneToggle), key(t0, PaneTrust))
	if s.Pane.Trust != nil || s.Pane.Command != "" || !strings.Contains(s.Pane.Message, "relay chat") {
		t.Fatalf("pane %+v", s.Pane)
	}
}

func TestTrustOnlyContaminatedShownItems(t *testing.T) {
	clean := trustable
	clean.Labels = Labels{Private: true}
	for name, it := range map[string]Item{"clean": clean, "proposal": {Kind: ProposalItem, CallID: "c", Labels: Labels{Contaminated: true}, CanTrust: true}} {
		s := focused(it)
		s, fx := feed(s, key(t0, PaneTrust))
		if s.Pane.Trust != nil || s.Pane.Message == "" || len(fx) != 0 {
			t.Errorf("%s: pane %+v", name, s.Pane)
		}
	}
}

func TestPaneNavigation(t *testing.T) {
	a, b := trustable, trustable
	b.Ref, b.Path, b.Title = "shown:b", "shown/b.md", "Second"
	s := focused(a, b)
	if SelectedIndex(s) != 0 {
		t.Fatal("not on the first item")
	}
	s, _ = feed(s, key(t0, PaneDown))
	if SelectedIndex(s) != 1 {
		t.Fatalf("down: %d", SelectedIndex(s))
	}
	s, _ = feed(s, key(t0, PaneDown)) // clamped
	if SelectedIndex(s) != 1 {
		t.Fatalf("down past the end: %d", SelectedIndex(s))
	}
	// The selection follows its item when a new one arrives on top.
	s, _ = feed(s, ItemsLoaded{At: t0, Items: []Item{{Kind: ShownItem, Ref: "shown:new"}, a, b}})
	if i := SelectedIndex(s); i != 2 {
		t.Fatalf("selection moved to %d", i)
	}
	// Enter opens, scroll is clamped to what the renderer says, esc goes back.
	s, _ = feed(s, key(t0, PaneEnter))
	if !s.Pane.Detail {
		t.Fatal("enter did not open")
	}
	s, _ = feed(s, PaneKey{At: t0, Action: PaneDown, Max: 1}, PaneKey{At: t0, Action: PaneDown, Max: 1}, PaneKey{At: t0, Action: PageNext, Max: 1})
	if s.Pane.Scroll != 1 {
		t.Fatalf("scroll %d", s.Pane.Scroll)
	}
	s, _ = feed(s, PaneKey{At: t0, Action: PagePrev, Max: 1})
	if s.Pane.Scroll != 0 {
		t.Fatalf("scroll %d", s.Pane.Scroll)
	}
	s, _ = feed(s, key(t0, PaneEsc))
	if s.Pane.Detail || !s.Pane.Focus {
		t.Fatalf("esc from detail: %+v", s.Pane)
	}
	s, _ = feed(s, key(t0, PaneEsc))
	if s.Pane.Focus {
		t.Fatal("esc did not return focus")
	}
	// Keys do nothing while the pane is not focused.
	s2, fx := feed(s, key(t0, PaneEnter), key(t0, PaneTrust), key(t0, PaneYes))
	if s2.Pane.Detail || s2.Pane.Trust != nil || len(fx) != 0 {
		t.Fatal("an unfocused pane acted on keys")
	}
}

// Whatever keys reach the pane, it never answers a request, never sends a
// prompt, and sends a trust only for an item relay listed as trustable,
// with that item's own version and hash, after the arm delay.
func TestPaneKeysCannotApproveOrSend(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := focused(trustable, Item{Kind: ProposalItem, CallID: "c", Status: StatusWaitingProposal})
		now := t0
		for range rapid.IntRange(1, 40).Draw(t, "keys") {
			now = now.Add(time.Duration(rapid.IntRange(0, 900).Draw(t, "ms")) * time.Millisecond)
			var e Event
			switch rapid.IntRange(0, 3).Draw(t, "kind") {
			case 0:
				e = PaneKey{At: now, Action: PaneAction(rapid.IntRange(0, int(PaneYes)).Draw(t, "action")), Max: 5}
			case 1:
				e = Tick{At: now}
			case 2:
				e = ItemsLoaded{At: now, Items: []Item{trustable}}
			case 3:
				e = Submit{At: now, Text: ""}
			}
			prev := s
			var fx []Effect
			s, fx = Reduce(s, e)
			if len(answers(fx)) != 0 || len(effectsOf[SendPrompt](fx)) != 0 {
				t.Fatalf("%T gave %#v", e, fx)
			}
			for _, st := range effectsOf[SendTrust](fx) {
				pk, _ := e.(PaneKey)
				if pk.Action != PaneYes || prev.Pane.Trust == nil || pk.At.Sub(prev.Pane.Trust.Since) < ArmDelay ||
					st != (SendTrust{Path: trustable.Path, Version: trustable.Version, SHA256: trustable.SHA256}) {
					t.Fatalf("trust sent by %+v with prompt %+v", e, prev.Pane.Trust)
				}
			}
		}
	})
}

// The set of approved domains only grows, whatever happens.
func TestDomainsOnlyGrow(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := focused(trustable)
		now := t0
		id := uint64(0)
		for range rapid.IntRange(1, 40).Draw(t, "steps") {
			now = now.Add(time.Second)
			var e Event
			switch rapid.IntRange(0, 5).Draw(t, "kind") {
			case 0:
				id++
				e = PermissionAsked{At: now, Permission: Permission{ID: id, CallID: fmt.Sprint(id), Input: fmt.Sprintf(`{"u":"https://h%d.test/x"}`, id%4),
					Options: []PermOption{{ID: "a", Kind: AllowOnce}, {ID: "r", Kind: RejectOnce}}}}
			case 1:
				e = PermissionQuick{At: now, Kind: AllowOnce}
			case 2:
				e = PermissionQuick{At: now, Kind: RejectOnce}
			case 3:
				e = PermissionCancel{At: now}
			case 4:
				e = PaneKey{At: now, Action: PaneAction(rapid.IntRange(0, int(PaneYes)).Draw(t, "action"))}
			case 5:
				e = TrustDone{At: now, Path: trustable.Path}
			}
			before := s.Pane.Domains
			s, _ = Reduce(s, e)
			for _, d := range before {
				if !slices.Contains(s.Pane.Domains, d) {
					t.Fatalf("%q left the set after %T", d, e)
				}
			}
		}
	})
}

func TestShowAllCarriesThePreview(t *testing.T) {
	s := busyState()
	s, _ = feed(s, LabelsSeen{At: t0, Labels: Labels{Contaminated: true}},
		PermissionAsked{At: t0, Permission: Permission{ID: 1, Name: "send", Input: `{"a":1}`, Preview: "will send x"}},
		PermissionShowAll{At: t0})
	e := s.Entries[len(s.Entries)-1]
	if e.Kind != Request || e.Preview != "will send x" || !e.PreviewDefang || e.Detail != `{"a":1}` {
		t.Fatalf("entry %+v", e)
	}
}

func TestStatusCountsPaneItems(t *testing.T) {
	s := focused(trustable, trustable)
	if got := Status(s, t0).PaneItems; got != 2 {
		t.Errorf("PaneItems %d", got)
	}
}

// Without relay's list the pane shows what it saw, newest first, however
// many displays arrive.
func TestLocalItemsNewestFirst(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := busyState()
		n := rapid.IntRange(0, 12).Draw(t, "n")
		for i := range n {
			s, _ = Reduce(s, displayEvent(fmt.Sprintf("c%d", i), fmt.Sprintf("title %d", i), Labels{}))
		}
		items := PaneItems(s)
		if len(items) != n {
			t.Fatalf("%d items, want %d", len(items), n)
		}
		for i, it := range items {
			if want := fmt.Sprintf("title %d", n-1-i); it.Title != want {
				t.Fatalf("item %d is %q, want %q", i, it.Title, want)
			}
		}
	})
}
