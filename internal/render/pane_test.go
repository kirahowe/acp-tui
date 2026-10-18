package render

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"pgregory.net/rapid"

	"github.com/kirahowe/acp-tui/internal/core"
)

var (
	untrusted = core.Item{
		Kind: core.ShownItem, Ref: "shown:2026-10-18/t1", Path: "shown/2026-10-18/t1.md", CallID: "t1",
		Title: "Concert venues near the hall",
		Text:  "Three venues are open late.\nDetails: https://example.org/venues\n\nA second paragraph with more words so that the text wraps in a narrow pane.",
		Sources: []core.Source{{Title: "Venue list", Ref: "web:https://example.org/venues"},
			{Title: "Hall site", Ref: "web:https://hall.example.com/about"}},
		Labels:  core.Labels{Private: true, Contaminated: true},
		Version: "412", SHA256: "0123456789abcdef0123456789abcdef", CanTrust: true,
		TrustCommand: "relay trust doc shown/2026-10-18/t1.md",
	}
	trusted = core.Item{
		Kind: core.ShownItem, Ref: "shown:2026-10-18/t0", Title: "Hall address", Text: "1 Main Street", Labels: core.Labels{Private: true},
	}
	proposal = core.Item{
		Kind: core.ProposalItem, CallID: "t2", Tool: "send_campaign", Input: `{"group":"Kira only"}`,
		Preview: "Sends **Venue list** to Kira only.\nSee https://send.example.net/campaign",
		Labels:  core.Labels{Private: true, Contaminated: true}, Status: core.StatusWaitingProposal,
	}
)

func paneState(focus bool, items ...core.Item) core.State {
	s := core.State{Phase: core.Idle, Ready: true}
	s.Pane = core.Pane{Relay: true, Server: items, Focus: focus}
	s.Labels = core.Labels{Private: true, Contaminated: true}
	return s
}

func goldenLines(t *testing.T, lines []string) {
	t.Helper()
	requireGolden(t, strings.Join(lines, "\n"))
}

func TestPaneGolden(t *testing.T) {
	list := paneState(true, proposal, untrusted, trusted)
	list.Pane.Sel, list.Pane.SelKey = 1, untrusted.Key()
	list.Entries = []core.Entry{{Kind: core.Agent, ID: "m", Final: true, Text: "The hall seats 900 [[fact:12]], see [[doc:gone]], [[web:https://x.test/a]] and [[note:later]]."}}
	list.Pane.Cites = []core.Citation{
		{Ref: "[[fact:12]]", Kind: "fact", Status: core.CiteOK, Title: "Fact 12 [venues]"},
		{Ref: "[[doc:gone]]", Kind: "doc", Status: core.CiteMissing},
		{Ref: "[[web:https://x.test/a]]", Kind: "web", Status: core.CiteExternal, Target: "https://x.test/a"},
		{Ref: "[[note:later]]"},
	}
	list.Pane.Domains = []string{"example.org"}

	t.Run("side", func(t *testing.T) {
		list.Asks = []core.Permission{{ID: 1, CallID: "t2", Name: "send_campaign", Since: t0}}
		for i := range list.Pane.Server {
			if list.Pane.Server[i].Kind == core.ProposalItem {
				list.Pane.Server[i].AskID = 1
			}
		}
		list.Pane.Focus = false
		f := View(list, "› ", "ctrl+j for a new line", t0, 130, 24)
		goldenLines(t, strings.Split(f.Content, "\n"))
	})
	list.Asks = nil
	list.Pane.Focus = true
	t.Run("side_focused", func(t *testing.T) {
		f := View(list, "› ", "ctrl+j for a new line", t0, 130, 24)
		goldenLines(t, strings.Split(f.Content, "\n"))
	})
	t.Run("overlay", func(t *testing.T) {
		f := View(list, "› ", "ctrl+j for a new line", t0, 60, 24)
		goldenLines(t, strings.Split(f.Content, "\n"))
	})
	t.Run("overlay_closed_hint", func(t *testing.T) {
		closed := list
		closed.Pane.Focus = false
		f := View(closed, "› ", "ctrl+j for a new line", t0, 60, 10)
		goldenLines(t, strings.Split(f.Content, "\n"))
	})
	t.Run("detail_untrusted", func(t *testing.T) {
		d := list
		d.Pane.Detail = true
		goldenLines(t, PaneLines(d, t0, 44, 30))
	})
	t.Run("detail_untrusted_scrolled", func(t *testing.T) {
		d := list
		d.Pane.Detail, d.Pane.Scroll = true, 5
		goldenLines(t, PaneLines(d, t0, 44, 12))
	})
	t.Run("detail_proposal", func(t *testing.T) {
		d := list
		d.Pane.Detail, d.Pane.Sel, d.Pane.SelKey = true, 0, proposal.Key()
		goldenLines(t, PaneLines(d, t0, 44, 24))
	})
	t.Run("trust_confirmation", func(t *testing.T) {
		d := list
		d.Pane.Trust = &core.TrustPrompt{Item: untrusted, Since: t0}
		goldenLines(t, PaneLines(d, t0.Add(core.ArmDelay), 44, 24))
	})
	t.Run("trust_confirmation_not_armed", func(t *testing.T) {
		d := list
		d.Pane.Trust = &core.TrustPrompt{Item: untrusted, Since: t0}
		goldenLines(t, PaneLines(d, t0, 44, 24))
	})
	t.Run("trust_command", func(t *testing.T) {
		d := list
		d.Pane.Message, d.Pane.Command = "Trust needs a terminal. Run this there:", untrusted.TrustCommand
		goldenLines(t, PaneLines(d, t0, 44, 24))
	})
	t.Run("trust_failed", func(t *testing.T) {
		d := list
		d.Pane.Message, d.Pane.Failure, d.Pane.Command = "Trust failed.", "the doc changed since it was listed", untrusted.TrustCommand
		goldenLines(t, PaneLines(d, t0, 44, 24))
	})
}

func TestPermissionBoxShowsThePreview(t *testing.T) {
	s := core.State{Phase: core.Busy, Ready: true, Labels: core.Labels{Contaminated: true}}
	s.Asks = []core.Permission{{ID: 1, Name: "send_campaign", Input: `{"group":"Kira only"}`,
		Preview: "Sends **Venue list** to Kira only.\nSee https://send.example.net/campaign", Since: t0,
		Options: []core.PermOption{{ID: "a", Name: "Allow", Kind: core.AllowOnce}, {ID: "r", Name: "Reject", Kind: core.RejectOnce}}}}
	s.Pick = 1
	f := View(s, "", "", t0.Add(time.Second), 70, 20)
	goldenLines(t, strings.Split(f.Content, "\n"))
	plain := ansi.Strip(f.Content)
	if strings.Contains(plain, "https://send") {
		t.Error("a contaminated session's preview shows a live link")
	}
	if strings.Index(plain, "Sends") > strings.Index(plain, "Kira only\"") {
		t.Error("the preview does not come above the input")
	}
}

// The side pane leaves the transcript alone: what goes to scrollback is
// the same lines the frame would print without it, at the reduced width.
func TestSidePaneLeavesPrintAlone(t *testing.T) {
	s := paneState(false, untrusted)
	for i := range 30 {
		s.Entries = append(s.Entries, core.Entry{Kind: core.Agent, ID: "m", Final: true, Text: strings.Repeat("word ", 5+i%7)})
	}
	with := View(s, "› ", "", t0, 140, 20)
	s.Pane = core.Pane{}
	without := View(s, "› ", "", t0, MainWidth(140), 20)
	if strings.Join(with.Print, "\n") != strings.Join(without.Print, "\n") || with.PrintTo != without.PrintTo {
		t.Errorf("print differs:\n%q\n%q", with.Print, without.Print)
	}
	for _, l := range with.Print {
		if strings.Contains(l, "│") || ansi.StringWidth(l) > MainWidth(140) {
			t.Errorf("print line %q holds the pane or is too wide", l)
		}
	}
}

func TestNarrowOverlayPrintsNothing(t *testing.T) {
	s := paneState(true, untrusted)
	for i := range 30 {
		s.Entries = append(s.Entries, core.Entry{Kind: core.Agent, ID: "m", Final: true, Text: strings.Repeat("word ", 5+i%7)})
	}
	if f := View(s, "› ", "", t0, 60, 20); len(f.Print) != 0 {
		t.Errorf("printed %q under the overlay", f.Print)
	}
}

func TestSideThreshold(t *testing.T) {
	if Side(SideMinWidth-1) || !Side(SideMinWidth) {
		t.Error("threshold")
	}
	for w := SideMinWidth; w < 400; w++ {
		if MainWidth(w)+gap+PaneWidth(w) != w || MainWidth(w) < 40 {
			t.Fatalf("width %d: main %d pane %d", w, MainWidth(w), PaneWidth(w))
		}
	}
}

// Characters that attack a terminal or a reader.
var nasty = []string{
	"plain", "", "\x1b[31mred\x1b[0m", "\x1b]8;;https://evil.test\x07click\x1b]8;;\x07", "bell\x07", "a\u202eb", "zero\u200bwidth",
	"\u2028line", "tab\there", "cr\rhere", "c1\u009b31m", "https://evil.test/x?d=1", "www.evil.test", "line\nbreak", "\u0000nul",
	"wide \u4e16\u754c \U0001f389", "[[fact:1]]", "ftp://h.test", "\ufeffbom", "\u00e9", "\x7f",
}

func paneText0(t *rapid.T, label string) string {
	return rapid.OneOf(rapid.SampledFrom(nasty), rapid.String()).Draw(t, label)
}

func genItem(t *rapid.T) core.Item {
	it := core.Item{
		Kind: core.ItemKind(rapid.IntRange(0, 1).Draw(t, "kind")), Ref: paneText0(t, "ref"), Path: paneText0(t, "path"), CallID: paneText0(t, "call"),
		Title: paneText0(t, "title"), Text: paneText0(t, "text"), Tool: paneText0(t, "tool"), Input: rapid.SampledFrom([]string{"", `{"a":"` + paneText0(t, "arg") + `"}`, paneText0(t, "rawinput")}).Draw(t, "input"),
		Preview: paneText0(t, "preview"), Status: rapid.SampledFrom([]string{"waiting", "allowed", "rejected", "cancelled", paneText0(t, "status")}).Draw(t, "status"),
		Version: paneText0(t, "version"), SHA256: paneText0(t, "sha"), CanTrust: rapid.Bool().Draw(t, "can"), TrustCommand: paneText0(t, "cmd"),
		Labels: core.Labels{Private: rapid.Bool().Draw(t, "p"), Contaminated: rapid.Bool().Draw(t, "c")},
	}
	for range rapid.IntRange(0, 3).Draw(t, "sources") {
		it.Sources = append(it.Sources, core.Source{Title: paneText0(t, "stitle"), Ref: paneText0(t, "sref")})
	}
	return it
}

func genPaneState(t *rapid.T) core.State {
	var items []core.Item
	for range rapid.IntRange(0, 4).Draw(t, "items") {
		items = append(items, genItem(t))
	}
	s := paneState(rapid.Bool().Draw(t, "focus"), items...)
	s.Labels = core.Labels{Contaminated: rapid.Bool().Draw(t, "session")}
	s.Pane.Relay = rapid.Bool().Draw(t, "relay")
	s.Pane.Local = items
	if len(items) > 0 {
		s.Pane.Sel = rapid.IntRange(0, len(items)).Draw(t, "sel")
	}
	s.Pane.Detail = rapid.Bool().Draw(t, "detail")
	s.Pane.Scroll = rapid.IntRange(0, 30).Draw(t, "scroll")
	s.Pane.Message, s.Pane.Failure, s.Pane.Command = paneText0(t, "msg"), paneText0(t, "fail"), paneText0(t, "command")
	if len(items) > 0 && rapid.Bool().Draw(t, "trust") {
		s.Pane.Trust = &core.TrustPrompt{Item: items[0], Since: t0}
	}
	refs := ""
	for range rapid.IntRange(0, 3).Draw(t, "refs") {
		ref := "[[" + rapid.SampledFrom([]string{"fact", "doc", "web"}).Draw(t, "kind") + ":" + rapid.StringMatching(`[a-z0-9:/.%\x00-\x1f]{1,12}`).Draw(t, "id") + "]]"
		refs += ref + " "
		if rapid.Bool().Draw(t, "resolved") {
			s.Pane.Cites = append(s.Pane.Cites, core.Citation{Ref: ref, Status: rapid.SampledFrom([]string{"", "ok", "missing", "external"}).Draw(t, "status"),
				Title: paneText0(t, "ctitle"), Target: paneText0(t, "target"), Labels: core.Labels{Contaminated: rapid.Bool().Draw(t, "cc")}})
		}
	}
	s.Entries = []core.Entry{{Kind: core.Agent, ID: "m", Final: true, Text: refs}}
	return s
}

// unprintable says whether r must never reach the terminal as itself.
func unprintable(r rune) bool {
	return r == 0x1b || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) ||
		unicode.Is(unicode.Co, r) || r == 0xFFFD
}

// Whatever relay (or a model) puts in an item, the pane draws exactly the
// room it was given, no wider than the width, with no character that could
// move the cursor, start a link or reorder text.
func TestPaneNeverEmitsRawControls(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genPaneState(t)
		w := rapid.IntRange(1, 70).Draw(t, "width")
		h := rapid.IntRange(0, 40).Draw(t, "height")
		now := t0.Add(time.Duration(rapid.IntRange(0, 2000).Draw(t, "ms")) * time.Millisecond)
		lines := PaneLines(s, now, w, h)
		if len(lines) != h {
			t.Fatalf("%d lines, want %d", len(lines), h)
		}
		for _, l := range lines {
			if strings.Contains(l, "\x1b]") {
				t.Fatalf("operating system command in %q", l)
			}
			plain := ansi.Strip(l)
			if ansi.StringWidth(l) > w {
				t.Fatalf("line %q is %d wide, limit %d", l, ansi.StringWidth(l), w)
			}
			for _, r := range plain {
				if unprintable(r) && r != 0xFFFD {
					t.Fatalf("raw %U in %q (plain %q)", r, l, plain)
				}
			}
			if strings.ContainsAny(plain, "\r\n") {
				t.Fatalf("line break inside a line: %q", plain)
			}
		}
		// And in a whole frame, beside the transcript or over it.
		fw := rapid.SampledFrom([]int{50, 80, 120, 160}).Draw(t, "frameWidth")
		f := View(s, "› ", "hint", now, fw, 24)
		for _, l := range strings.Split(f.Content, "\n") {
			if ansi.StringWidth(l) > fw {
				t.Fatalf("frame row %q is %d wide, limit %d", l, ansi.StringWidth(l), fw)
			}
			for _, r := range ansi.Strip(l) {
				if r == 0x1b || (unprintable(r) && r != 0xFFFD) {
					t.Fatalf("raw %U in frame row %q", r, l)
				}
			}
		}
	})
}

// Text from a contaminated item shows under the untrusted mark, with no
// live link, wherever it is drawn.
func TestContaminatedItemsAreMarkedAndDefanged(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		it := genItem(t)
		it.Labels.Contaminated = true
		it.Kind = core.ShownItem
		it.Title = rapid.SampledFrom([]string{"Title", "see https://evil.test/a?d=1", "www.evil.test x"}).Draw(t, "title")
		it.Text = "go to " + rapid.SampledFrom([]string{"https://evil.test/x", "ftp://evil.test", "http://a.b.c/d", "www.e.test",
			"https://a.test/?next=www.evil.test/?d=SECRET", "https://a.test/r?u=https://evil.test/x"}).Draw(t, "url") + " now"
		it.Sources = []core.Source{{Title: "s", Ref: "web:https://evil.test/src"}}
		s := paneState(true, it)
		s.Pane.Relay = true
		s.Entries = nil
		mode := rapid.IntRange(0, 2).Draw(t, "mode")
		switch mode {
		case 1:
			s.Pane.Detail = true
		case 2:
			s.Pane.Trust = &core.TrustPrompt{Item: it, Since: t0}
		}
		out := strings.Join(PaneLines(s, t0, 60, 60), "\n")
		plain := ansi.Strip(out)
		if !strings.Contains(plain, "untrusted") {
			t.Fatalf("no untrusted mark (mode %d):\n%s", mode, plain)
		}
		if !strings.Contains(out, sgr(bold|reverse)+" untrusted ") {
			t.Fatalf("the mark is not in reverse video:\n%q", out)
		}
		for _, l := range strings.Split(plain, "\n") {
			if live(l) {
				t.Fatalf("live link in %q", l)
			}
		}
	})
}

func TestPaneMarksNewDomains(t *testing.T) {
	s := paneState(true, untrusted)
	s.Pane.Domains = []string{"example.org"}
	plain := ansi.Strip(strings.Join(PaneLines(s, t0, 60, 40), "\n"))
	if strings.Count(plain, "[new domain]") != 1 || !strings.Contains(plain, "hall[.]example[.]com") {
		t.Errorf("only the hall site is new:\n%s", plain)
	}
	s.Pane.Domains = []string{"example.org", "hall.example.com"}
	if plain := ansi.Strip(strings.Join(PaneLines(s, t0, 60, 40), "\n")); strings.Contains(plain, "new domain") {
		t.Errorf("known domains are marked:\n%s", plain)
	}
}

func TestPaneShowsMissingInReverse(t *testing.T) {
	s := paneState(false)
	s.Entries = []core.Entry{{Kind: core.Agent, ID: "m", Final: true, Text: "[[doc:gone]] [[fact:1]]"}}
	s.Pane.Cites = []core.Citation{{Ref: "[[doc:gone]]", Status: core.CiteMissing}}
	out := strings.Join(PaneLines(s, t0, 50, 12), "\n")
	if !strings.Contains(out, sgr(reverse)+" missing ") {
		t.Errorf("missing is not in reverse video:\n%q", out)
	}
	if !strings.Contains(ansi.Strip(out), "unchecked [[fact:1]]") {
		t.Errorf("an unresolved reference is not marked unchecked:\n%s", ansi.Strip(out))
	}
}

func TestDetailMaxMatchesWhatIsDrawn(t *testing.T) {
	s := paneState(true, untrusted)
	s.Pane.Detail = true
	_, mx := paneRender(s, t0, 30, 10)
	if mx <= 0 {
		t.Fatal("no scroll for a long text in a short pane")
	}
	s.Pane.Scroll = mx
	last := ansi.Strip(strings.Join(paneBodyTail(s), "\n"))
	if !strings.Contains(last, "relay trust doc") && !strings.Contains(last, "hall") {
		t.Errorf("the last scroll position does not reach the end:\n%s", last)
	}
	s.Pane.Scroll = mx + 50
	a, _ := paneRender(s, t0, 30, 10)
	s.Pane.Scroll = mx
	b, _ := paneRender(s, t0, 30, 10)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Error("scrolling past the end changes the picture")
	}
}

// paneBodyTail is the pane at its furthest scroll, 30 wide and 10 tall.
func paneBodyTail(s core.State) []string {
	l, _ := paneRender(s, t0, 30, 10)
	return l
}
