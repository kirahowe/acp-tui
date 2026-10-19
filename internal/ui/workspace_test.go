package ui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kirahowe/acp-tui/internal/core"
)

const ctrlX = "\x18"

func (h *harness) extensionCalls(method string) []string {
	var out []string
	for _, l := range strings.Split(h.agentLog(), "\n") {
		if rest, ok := strings.CutPrefix(l, "extension "+method+" "); ok {
			out = append(out, rest)
		}
	}
	return out
}

// wideWorkspace plays the workspace script on a wide screen up to the
// request, which shows relay's preview, with the pane beside it.
func wideWorkspace(t *testing.T) *harness {
	h := start(t, "workspace.jsonl", 140, 34)
	h.keys("go\r")
	h.waitScreen("the request", func(s string) bool { return strings.Contains(s, "permission needed") })
	return h
}

func TestWorkspaceSidePaneAndApproval(t *testing.T) {
	h := wideWorkspace(t)
	scr := h.waitScreen("the pane", func(s string) bool {
		return strings.Contains(s, "Concert venues near the hall") && strings.Contains(s, "waiting for you")
	})
	for _, want := range []string{"workspace (2)", "untrusted", "hxxps://example[.]org/venues", "[new domain]", "send_campaign",
		"Sends **Venue list** to Kira only.", "hxxps://send[.]example[.]net/campaign"} {
		if !strings.Contains(scr, want) {
			t.Errorf("%q not on screen:\n%s", want, scr)
		}
	}
	if strings.Contains(scr, "https://example.org") || strings.Contains(scr, "https://send.example.net") {
		t.Errorf("a live link on screen:\n%s", scr)
	}
	// The preview sits in the permission box too, above the input.
	box := scr[strings.Index(scr, "permission needed"):]
	if !strings.Contains(box, "Sends") || strings.Index(box, "Sends") > strings.Index(box, "Kira only\"") {
		t.Errorf("no preview above the input in the box:\n%s", scr)
	}
	// Keys belong to the request: ctrl+x does not take the pane.
	h.keys(ctrlX)
	time.Sleep(100 * time.Millisecond)
	time.Sleep(core.ArmDelay)
	h.keys("y")
	h.waitOutcome(`{"optionId":"allow","outcome":"selected"}`)
	h.waitIdle()
	// The citations of the latest reply, as relay answered them.
	scr = h.waitScreen("the citations", func(s string) bool { return strings.Contains(s, "Fact 12 [venues]") && strings.Contains(s, "missing") })
	if !strings.Contains(scr, "[[doc:gone]]") {
		t.Errorf("the missing reference is not named:\n%s", scr)
	}
	// The approved call's host is no longer new, the unapproved one is.
	if got := len(h.extensionCalls("_relay/resolve")); got != 2 {
		t.Errorf("%d resolve calls, want one per distinct reference", got)
	}
	if len(h.extensionCalls("_relay/items")) < 3 {
		t.Errorf("items asked for %d times; want on connect, after the display and around the request:\n%s",
			len(h.extensionCalls("_relay/items")), h.agentLog())
	}
}

func TestWorkspaceTrust(t *testing.T) {
	h := wideWorkspace(t)
	h.waitScreen("the pane", func(s string) bool { return strings.Contains(s, "waiting for you") })
	time.Sleep(core.ArmDelay + 100*time.Millisecond)
	h.keys("y")
	h.waitOutcome(`{"optionId":"allow","outcome":"selected"}`)
	h.waitIdle()
	h.keys(ctrlX)
	h.waitScreen("focus", func(s string) bool { return strings.Contains(s, "↑↓ select") })
	// Keys go to the pane, not the input box.
	h.keys("hello")
	h.keys("j") // to the shown item
	h.keys("t")
	scr := h.waitScreen("the confirmation", func(s string) bool { return strings.Contains(s, "Trust this?") })
	for _, want := range []string{"Trust this?", "contaminated from what", "Three venues are open late.", "shown/2026-10-18/t1.md", "v412"} {
		if !strings.Contains(scr, want) {
			t.Errorf("%q not in the confirmation:\n%s", want, scr)
		}
	}
	if len(h.extensionCalls("_relay/trust")) != 0 {
		t.Fatal("trust sent before the confirmation")
	}
	// An early y does nothing.
	h.keys("y")
	time.Sleep(100 * time.Millisecond)
	if len(h.extensionCalls("_relay/trust")) != 0 {
		t.Fatal("trust sent before the arm delay")
	}
	time.Sleep(core.ArmDelay)
	h.keys("y")
	scr = h.waitScreen("the outcome", func(s string) bool { return strings.Contains(s, "Trusted.") })
	calls := h.extensionCalls("_relay/trust")
	if len(calls) != 1 || !strings.Contains(calls[0], `"path":"shown/2026-10-18/t1.md"`) || !strings.Contains(calls[0], `"version":412`) ||
		!strings.Contains(calls[0], `"sha256":"0123456789abcdef`) || !strings.Contains(calls[0], `"sessionId":"fake-session"`) {
		t.Fatalf("trust calls %q", calls)
	}
	// Relay's new list has the item without the contaminated label.
	scr = h.waitScreen("the refreshed list", func(s string) bool { return strings.Contains(s, "shown · private · trusted") })
	if strings.Contains(scr, "untrusted") {
		t.Errorf("still marked untrusted after the trust:\n%s", scr)
	}
	h.keys("\x1b") // leave the pane
	h.waitScreen("unfocused", func(s string) bool { return strings.Contains(s, ctrlXHint) })
	if strings.Contains(h.screen(), "hello") {
		t.Errorf("keys typed in the pane reached the input:\n%s", h.screen())
	}
	if p := h.prompts(); len(p) != 1 {
		t.Errorf("prompts %q", p)
	}
}

const ctrlXHint = "ctrl+x to focus"

func TestWorkspaceTrustCancelledByAnotherKey(t *testing.T) {
	h := wideWorkspace(t)
	h.waitScreen("the pane", func(s string) bool { return strings.Contains(s, "waiting for you") })
	time.Sleep(core.ArmDelay + 100*time.Millisecond)
	h.keys("y")
	h.waitOutcome(`{"optionId":"allow","outcome":"selected"}`)
	h.waitIdle()
	h.keys(ctrlX)
	h.keys("j")
	h.keys("t")
	h.waitScreen("the confirmation", func(s string) bool { return strings.Contains(s, "Trust this?") })
	time.Sleep(core.ArmDelay + 100*time.Millisecond)
	h.keys("n")
	h.waitScreen("cancel", func(s string) bool { return strings.Contains(s, "Trust cancelled.") })
	time.Sleep(100 * time.Millisecond)
	if n := len(h.extensionCalls("_relay/trust")); n != 0 {
		t.Fatalf("%d trust calls after a cancel", n)
	}
}

func TestWorkspaceOverlayOnANarrowScreen(t *testing.T) {
	h := start(t, "workspace.jsonl", 80, 28)
	h.keys("go\r")
	h.waitScreen("the request", func(s string) bool { return strings.Contains(s, "permission needed") })
	time.Sleep(core.ArmDelay + 100*time.Millisecond)
	h.keys("y")
	h.waitOutcome(`{"optionId":"allow","outcome":"selected"}`)
	h.waitIdle()
	h.waitScreen("the hint", func(s string) bool { return strings.Contains(s, "ctrl+x workspace (2)") })
	h.keys(ctrlX)
	scr := h.waitScreen("the overlay", func(s string) bool {
		return strings.Contains(s, "workspace (2)") && strings.Contains(s, "↑↓ select")
	})
	for _, want := range []string{"untrusted", "Concert venues near the hall", "hxxps://example[.]org/venues"} {
		if !strings.Contains(scr, want) {
			t.Errorf("%q not in the overlay:\n%s", want, scr)
		}
	}
	// Enter opens the item; it scrolls; esc goes back, esc again closes.
	h.keys("\r")
	h.waitScreen("the detail", func(s string) bool { return strings.Contains(s, "↑↓ scroll") })
	h.keys("\x1b")
	h.waitScreen("the list", func(s string) bool { return strings.Contains(s, "↑↓ select") })
	h.keys("\x1b")
	h.waitScreen("closed", func(s string) bool {
		return !strings.Contains(s, "↑↓ select") && strings.Contains(s, "ctrl+x workspace (2)")
	})
	if len(h.extensionCalls("_relay/trust")) != 0 {
		t.Error("a trust call with nobody asking")
	}
}

// A display is for the operator: it is not in the tool output that
// ctrl+t shows, and not in the transcript.
func TestDisplayStaysOutOfToolOutput(t *testing.T) {
	h := start(t, "workspace.jsonl", 80, 40)
	h.keys("go\r")
	h.waitScreen("the request", func(s string) bool { return strings.Contains(s, "permission needed") })
	time.Sleep(core.ArmDelay + 100*time.Millisecond)
	h.keys("y")
	h.waitOutcome(`{"optionId":"allow","outcome":"selected"}`)
	h.waitIdle()
	h.keys("\x14") // ctrl+t
	all := h.waitScreen("the output in full", func(s string) bool { return strings.Contains(s, "search result text") })
	if strings.Contains(all, "Three venues are open late") {
		t.Errorf("the display's text is in the transcript:\n%s", all)
	}
}

// Against an agent without the extension methods the pane still works
// from what the client saw, and offers no trust.
func TestWorkspaceWithoutRelay(t *testing.T) {
	h := start(t, "workspace_plain.jsonl", 140, 30)
	h.keys("go\r")
	h.waitScreen("the request", func(s string) bool { return strings.Contains(s, "permission needed") })
	scr := h.waitScreen("the display", func(s string) bool { return strings.Contains(s, "Concert venues near the hall") })
	if !strings.Contains(scr, "untrusted") {
		t.Errorf("no untrusted mark:\n%s", scr)
	}
	time.Sleep(core.ArmDelay + 100*time.Millisecond)
	h.keys("n")
	h.waitOutcome(`{"optionId":"reject","outcome":"selected"}`)
	h.waitIdle()
	h.keys(ctrlX)
	h.waitScreen("focus", func(s string) bool { return strings.Contains(s, "↑↓ select") })
	// A notification and a request come in on different goroutines, so
	// which item is newer varies; select the shown one either way.
	scr = h.screen()
	if sel := lineWith(scr, "▸"); !strings.Contains(sel, "Concert") {
		if strings.Index(scr, "Concert venues") < strings.Index(scr, "send_campaign") {
			h.keys("k")
		} else {
			h.keys("j")
		}
	}
	h.keys("t")
	h.waitScreen("no trust", func(s string) bool { return strings.Contains(s, "Trust is not available here") })
	if n := len(h.extensionCalls("_relay/trust")); n != 0 {
		t.Errorf("%d trust calls without relay", n)
	}
	// The first calls (connect, the display, the request) can be out before
	// the first answer; the answer to the request and the end of the turn
	// would make a fourth and fifth.
	if got := len(h.extensionCalls("_relay/items")); got < 1 || got > 3 {
		t.Errorf("%d items calls; after method-not-found it should stop asking:\n%s", got, h.agentLog())
	}
	if strings.Contains(h.agentLog(), "_relay/resolve") {
		t.Errorf("resolve asked after method-not-found:\n%s", h.agentLog())
	}
}

func lineWith(screen, sub string) string {
	for _, l := range strings.Split(screen, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}
