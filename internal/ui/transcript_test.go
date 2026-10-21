package ui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kirahowe/acp-tui/internal/core"
)

// Ctrl-O opens the transcript with the thinking in full while the agent
// works. The permission request closes it, so the request is never
// hidden behind the view. Afterwards scrollback has the reply and the
// one-line marker, never the thinking itself.
func TestTranscriptView(t *testing.T) { bothSizes(t, testTranscriptView) }

func testTranscriptView(t *testing.T, cols, rows int) {
	h := start(t, "thinking.jsonl", cols, rows)
	h.waitIdle()
	h.keys("go\r")
	h.waitScreen("the marker", func(s string) bool { return strings.Contains(s, "∴ thinking") })
	h.keys("\x0f") // ctrl+o
	h.waitScreen("the thinking", func(s string) bool { return strings.Contains(s, "The page has") })
	h.waitScreen("the request", func(s string) bool { return strings.Contains(s, "permission needed") })
	time.Sleep(core.ArmDelay + 100*time.Millisecond)
	h.keys("y")
	h.waitOutcome(`{"optionId":"allow","outcome":"selected"}`)
	h.waitIdle()
	all := h.waitAll("Fetched.")
	if strings.Contains(all, "The page has") || !strings.Contains(all, "∴ thought") {
		t.Fatalf("the thinking reached the inline view:\n%s", all)
	}

	// Inside the view, each key flips its own kind. On the short screen
	// the thinking is above what shows.
	h.keys("\x14") // ctrl+t: open, tools in full; thinking stays as it was
	h.waitScreen("both in full", func(s string) bool {
		return strings.Contains(s, "url: https") && (rows < 24 || strings.Contains(s, "The page has"))
	})
	h.keys("o")
	h.waitScreen("thinking hidden", func(s string) bool {
		return !strings.Contains(s, "The page has") && strings.Contains(s, "url: https")
	})
	scroll := strings.Join(h.emu.scrollback(), "\n")
	h.keys("q")
	h.waitIdle()
	if again := strings.Join(h.emu.scrollback(), "\n"); again != scroll {
		t.Fatalf("scrollback changed:\n%s\n---\n%s", scroll, again)
	}

	// The next turn still reaches scrollback, in order, once.
	// Lines on their way to scrollback are in neither it nor the frame
	// for a moment, so wait for them to land.
	h.keys("again\r")
	h.wait("the next turn after the first", h.all, func(all string) bool {
		f, a, s := strings.Index(all, "Fetched."), strings.Index(all, "› again"), strings.Index(all, "Second.")
		return count(all, "Fetched.") == 1 && f >= 0 && f < a && a < s
	})
}

// ? on an empty input shows the keys; any key closes the list, and a key
// that means something still does it.
func TestHelpOverlay(t *testing.T) {
	h := start(t, "simple.jsonl", 80, 24)
	h.waitIdle()
	h.keys("?")
	h.waitScreen("the keys", func(s string) bool { return strings.Contains(s, "any key closes this") })
	h.keys("h")
	h.waitScreen("closed, with the key typed", func(s string) bool {
		return !strings.Contains(s, "any key closes this") && strings.Contains(s, "› h")
	})
	h.keys("?")
	h.waitScreen("a ? typed after text", func(s string) bool {
		return strings.Contains(s, "› h?") && !strings.Contains(s, "any key closes this")
	})
}

// Bubble Tea would write a print into the alternate screen, where it is
// lost, so nothing prints while the view is open. What the agent wrote
// meanwhile reaches scrollback once the view closes, once and in order.
func TestNothingLostBehindTranscript(t *testing.T) {
	h := start(t, "tool_burst.jsonl", 40, 10)
	h.waitIdle()
	h.keys("go\r\x14")
	h.waitScreen("the end, in the view", func(s string) bool {
		return strings.Contains(s, "All thirty read.") && strings.Contains(s, "transcript")
	})
	h.keys("\x1b")
	h.wait("everything once, in order", h.all, func(all string) bool {
		c, r, a := strings.Index(all, "Checking everything."), strings.Index(all, "✓ read file1.go"), strings.Index(all, "All thirty read.")
		return count(all, "Checking everything.") == 1 && count(all, "All thirty read.") == 1 && c < r && r < a && c >= 0
	})
}

// Ctrl-Y before a paste does nothing to it: a paste is untrusted when it
// arrives, wherever it came from.
func TestPasteAfterCtrlYIsUntrusted(t *testing.T) {
	h := startWith(t, "simple.jsonl", 80, 24, "", "-embedded")
	h.waitIdle()
	h.keys("\x19\x14") // ctrl+y, ctrl+t
	h.waitScreen("the view", func(s string) bool { return strings.Contains(s, "transcript") })
	h.keys(bracketed("dropped"))
	h.keys("q")
	h.waitIdle()
	h.keys(bracketed("untrusted web page"))
	h.waitScreen("the placeholder", func(s string) bool { return strings.Contains(s, "untrusted #1]") })
	h.keys("\r")
	h.waitAll("Hello there.")
	if r := h.resources(); len(r) != 1 || !strings.Contains(r[0], "untrusted web page") {
		t.Fatalf("resources %q", r)
	}
}

// Ctrl-D in the view still leaves everything in scrollback.
func TestQuitFromTranscriptKeepsScrollback(t *testing.T) {
	h := start(t, "tool_burst.jsonl", 40, 10)
	h.waitIdle()
	h.keys("go\r\x14")
	h.waitScreen("the end, in the view", func(s string) bool {
		return strings.Contains(s, "All thirty read.") && strings.Contains(s, "transcript")
	})
	h.keys("\x04")
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("did not quit")
	}
	all := h.all()
	if count(all, "Checking everything.") != 1 || count(all, "All thirty read.") != 1 || !strings.Contains(all, "› go") {
		t.Fatalf("after quitting:\n%s", all)
	}
}

// The list of keys over the view does not hide a request, and keys typed
// for the view before the request is armed do not answer it.
func TestHelpOverTranscriptYieldsToRequest(t *testing.T) {
	h := start(t, "thinking.jsonl", 80, 24)
	h.waitIdle()
	h.keys("go\r\x0f?")
	h.waitScreen("the request", func(s string) bool { return strings.Contains(s, "permission needed") })
	if s := h.screen(); strings.Contains(s, "any key closes this") || strings.Contains(s, "esc back") {
		t.Fatalf("the request shares the screen:\n%s", s)
	}
	h.keys("y")
	time.Sleep(core.ArmDelay + 100*time.Millisecond)
	if o := h.permissionOutcome(); o != "" {
		t.Fatalf("answered before it was armed: %s", o)
	}
	h.keys("n")
	h.waitOutcome(`{"optionId":"reject","outcome":"selected"}`)
}
