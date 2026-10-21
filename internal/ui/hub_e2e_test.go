package ui_test

// End to end for hub mode, in process: the real ui.Model and
// internal/agent against the fake hub (cmd/fakehub), which speaks the
// protocol of relay's hub. A second device is played through the fake
// hub's control file.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kirahowe/acp-tui/internal/agent"
	"github.com/kirahowe/acp-tui/internal/core"
	"github.com/kirahowe/acp-tui/internal/fakehub"
	"github.com/kirahowe/acp-tui/internal/ui"
)

type hubHarness struct {
	*harness
	control string
}

// threeRoles is a hub where Dev already has a conversation Kira has not
// read, and Mail and News have none.
func threeRoles() fakehub.Config {
	return fakehub.Config{Roles: []fakehub.RoleConfig{
		{ID: "dev", Title: "Dev", Live: &fakehub.Live{Unread: true, History: []fakehub.Said{{User: "hi"}, {Agent: "hello from dev"}}}},
		{ID: "mail", Title: "Mail", Labels: []string{"private"}},
		{ID: "news", Title: "News"},
	}}
}

func startHub(t *testing.T, cfg fakehub.Config, w, h int, open string) *hubHarness {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "hub.json")
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	hh := &hubHarness{
		harness: &harness{t: t, emu: newTerminal(w, h), log: filepath.Join(dir, "hub.log"), done: make(chan struct{})},
		control: filepath.Join(dir, "control"),
	}
	if err := os.WriteFile(hh.control, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	conn, err := agent.Start([]string{fakeHub, "-log", hh.log, "-control", hh.control, cfgPath}, dir, os.Stderr,
		func(e core.Event) {
			<-started
			hh.p.Send(ui.EventMsg{Event: e})
		})
	if err != nil {
		t.Fatal(err)
	}
	hh.conn = conn
	hh.p = tea.NewProgram(ui.New(conn, "").Open(open), tea.WithInput(hh.emu.in), tea.WithOutput(hh.emu),
		tea.WithWindowSize(w, h), tea.WithoutSignals())
	close(started)
	go func() { _, _ = hh.p.Run(); close(hh.done) }()
	t.Cleanup(func() {
		hh.p.Kill()
		select {
		case <-hh.done:
		case <-time.After(5 * time.Second):
			t.Error("the program did not stop")
		}
		conn.Shutdown()
		hh.emu.close()
	})
	lastHarness = nil
	return hh
}

// say appends a command for the fake hub, as a second device would act.
func (h *hubHarness) say(cmd string) {
	h.t.Helper()
	f, err := os.OpenFile(h.control, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(cmd + "\n"); err != nil {
		h.t.Fatal(err)
	}
}

// logged is the fake hub's log lines that start with prefix.
func (h *hubHarness) logged(prefix string) []string {
	var out []string
	for _, l := range strings.Split(h.agentLog(), "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func (h *hubHarness) waitLog(prefix string) string {
	h.t.Helper()
	var got string
	h.wait("log "+prefix, h.agentLog, func(s string) bool {
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(l, prefix) {
				got = l
				return true
			}
		}
		return false
	})
	return got
}

const (
	ctrlN = "\x0e"
	ctrlP = "\x10"
	ctrlG = "\x07"
	ctrlL = "\x0c"
	ctrlS = "\x13"
)

func contains(sub string) func(string) bool {
	return func(s string) bool { return strings.Contains(s, sub) }
}

func TestHubListThenOpenReplaysTheConversation(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "")
	s := h.waitScreen("the roster", contains("News"))
	if !strings.Contains(s, "●") { // Dev is unread
		t.Errorf("no unread mark:\n%s", s)
	}
	h.keys("\r") // the list starts on the first role
	s = h.waitScreen("the replay", contains("hello from dev"))
	if !strings.Contains(s, "› hi") {
		t.Errorf("Kira's own prompt is not replayed:\n%s", s)
	}
	// Looking at the latest turn tells relay.
	h.waitLog("seen fh-dev-1 1")
	h.waitScreen("no unread mark", func(s string) bool { return !strings.Contains(s, "●") })
	if n := len(h.logged("load ")); n != 1 {
		t.Errorf("%d loads", n)
	}
}

func TestHubOpenFlagAttachesAndPromptsEcho(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "dev")
	h.waitScreen("the replay", contains("hello from dev"))
	if l := h.waitLog("new dev"); l != "new dev attached=true" {
		t.Errorf("%q", l)
	}
	h.keys("what next\r")
	h.waitScreen("the echo", contains("echo: what next"))
	h.waitScreen("ready", contains("ready"))
	if got := h.waitLog("prompt fh-dev-1"); got != `prompt fh-dev-1 "what next"` {
		t.Errorf("%q", got)
	}
}

func TestHubSwitchingKeepsEveryConversationIntact(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.keys(ctrlN) // Mail has no conversation yet
	h.waitScreen("empty", contains("No conversation with Mail"))
	if n := len(h.logged("new mail")); n != 0 {
		t.Fatalf("moving to a role must not start a conversation: %v", h.logged("new"))
	}
	h.keys("to mail\r")
	h.waitScreen("mail echo", contains("echo: to mail"))
	if l := h.waitLog("new mail"); l != "new mail attached=false" {
		t.Errorf("%q", l)
	}
	h.keys(ctrlP)
	s := h.waitScreen("dev again", contains("hello from dev"))
	if strings.Contains(s, "echo: to mail") {
		t.Errorf("Mail's reply leaked into Dev:\n%s", s)
	}
	if count(s, "hello from dev") != 1 {
		t.Errorf("history shown twice:\n%s", s)
	}
	h.keys("\x1b2") // alt+2: Mail
	s = h.waitScreen("mail", contains("echo: to mail"))
	if strings.Contains(s, "hello from dev") {
		t.Errorf("Dev's history leaked into Mail:\n%s", s)
	}
	if n := len(h.logged("load ")) + len(h.logged("new dev")); n != 1 {
		t.Errorf("switching back and forth re-attached: %v", h.agentLog())
	}
}

func TestHubDraftsStayWithTheirRole(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.keys("half a thought")
	h.waitScreen("typed", contains("half a thought"))
	h.keys(ctrlN)
	s := h.waitScreen("mail", contains("No conversation with Mail"))
	if strings.Contains(s, "half a thought") {
		t.Errorf("the draft followed to the next role:\n%s", s)
	}
	h.keys(ctrlP)
	h.waitScreen("draft back", contains("half a thought"))
}

func TestHubBackgroundRequestShowsInTheSidebarAndIsAnsweredAfterSwitching(t *testing.T) {
	cfg := threeRoles()
	cfg.Roles[1].Live = &fakehub.Live{History: []fakehub.Said{{User: "a"}, {Agent: "b"}}}
	h := startHub(t, cfg, 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.say("ask mail")
	h.waitScreen("a request mark on Mail", contains("?1"))
	if s := h.screen(); strings.Contains(s, "permission needed") {
		t.Fatalf("the request is Mail's, and Mail is not on screen:\n%s", s)
	}
	h.keys("\x1b2")
	h.waitScreen("the request", contains("permission needed"))
	time.Sleep(700 * time.Millisecond) // past the arming delay
	h.keys("y")
	out := h.waitLog("permission fh-mail")
	if !strings.Contains(out, `"optionId":"allow"`) {
		t.Errorf("%q", out)
	}
	h.waitScreen("the request gone", func(s string) bool { return !strings.Contains(s, "permission needed") })
	h.waitScreen("no mark", func(s string) bool { return !strings.Contains(s, "?1") })
}

func TestHubAYKeyJustAfterSwitchingDoesNotApprove(t *testing.T) {
	cfg := threeRoles()
	cfg.Roles[1].Live = &fakehub.Live{History: []fakehub.Said{{User: "a"}, {Agent: "b"}}}
	h := startHub(t, cfg, 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.say("ask mail")
	h.waitScreen("a request mark", contains("?1"))
	time.Sleep(700 * time.Millisecond)
	h.keys("\x1b2y") // the switch and a y typed right behind it
	h.waitScreen("the request", contains("permission needed"))
	time.Sleep(200 * time.Millisecond)
	if got := h.logged("permission "); len(got) != 0 {
		t.Fatalf("approved by a stray key: %v", got)
	}
}

func TestHubAnotherDeviceSettlesARequest(t *testing.T) {
	cfg := threeRoles()
	h := startHub(t, cfg, 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.say("ask dev")
	h.waitScreen("the request", contains("permission needed"))
	h.say("settle dev")
	h.waitScreen("the request gone", func(s string) bool { return !strings.Contains(s, "permission needed") })
	h.waitScreen("a note", contains("answered elsewhere"))
	// Our handler was released, and relay is told (and drops it).
	h.waitLog("permission-late")
	// The input works again.
	h.keys("after\r")
	h.waitScreen("echo", contains("echo: after"))
}

func TestHubQueuedTurnWaitsForScheduledWork(t *testing.T) {
	cfg := threeRoles()
	cfg.Roles[0].Busy = true
	h := startHub(t, cfg, 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.keys("please\r")
	h.waitScreen("queued", contains("waiting for the role's scheduled work"))
	h.waitScreen("the mark", contains("…"))
	h.say("unbusy dev")
	h.waitScreen("the reply", contains("echo: please"))
	h.waitScreen("no longer waiting", func(s string) bool { return !strings.Contains(s, "scheduled work") })
}

func TestHubAnotherDevicesPromptAppears(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.say("other-prompt dev from the phone")
	s := h.waitScreen("the prompt", contains("› from the phone"))
	h.waitScreen("the reply", contains("echo: from the phone"))
	if strings.Contains(s, "hello from dev\n\nhello from dev") {
		t.Errorf("duplicated")
	}
}

func TestHubEndAsksFirstThenEnds(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.keys(ctrlS)
	h.waitScreen("the question", contains("End the conversation with Dev?"))
	h.keys("n")
	h.waitScreen("asked no", func(s string) bool { return !strings.Contains(s, "End the conversation") })
	if len(h.logged("end ")) != 0 {
		t.Fatal("ended after no")
	}
	h.keys(ctrlS)
	h.waitScreen("the question", contains("End the conversation with Dev?"))
	h.keys("y")
	h.waitLog("end fh-dev-1")
	h.waitScreen("gone", contains("No conversation with Dev"))
	// The role is still there, and typing starts a new conversation.
	h.keys("again\r")
	h.waitScreen("new conversation", contains("echo: again"))
	h.waitLog("new dev attached=false")
}

func TestHubConversationEndedElsewhereIsDropped(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.say("end dev")
	h.waitScreen("gone", contains("No conversation with Dev"))
}

func TestHubNarrowScreenShowsTheListOrTheConversation(t *testing.T) {
	h := startHub(t, threeRoles(), 44, 16, "")
	s := h.waitScreen("the list", contains("News"))
	if strings.Contains(s, "│ ") && strings.Contains(s, "relay") {
		t.Errorf("a sidebar on a narrow screen:\n%s", s)
	}
	h.keys("\r")
	s = h.waitScreen("the conversation", contains("hello from dev"))
	if strings.Contains(s, "News") || !strings.Contains(s, "ctrl+g roles") {
		t.Errorf("narrow conversation:\n%s", s)
	}
	h.keys(ctrlG)
	h.waitScreen("the list again", contains("News"))
	h.keys("\x1b") // esc goes back to the conversation
	h.waitScreen("the conversation again", contains("hello from dev"))
}

func TestHubWorkingFlagShowsInTheSidebar(t *testing.T) {
	cfg := threeRoles()
	h := startHub(t, cfg, 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.say("working news on")
	h.waitScreen("the mark", contains("~"))
	h.say("working news off")
	h.waitScreen("no mark", func(s string) bool { return !strings.Contains(s, "~") })
}

func TestHubHelpListsTheKeys(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 30, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.keys("?")
	h.waitScreen("the help", func(s string) bool { return strings.Contains(s, "ctrl+n ctrl+p") && strings.Contains(s, "ctrl+s") })
	h.keys("x")
	h.waitScreen("closed", func(s string) bool { return !strings.Contains(s, "any key closes") })
}

func TestHubCtrlCWithAnEmptyInputQuits(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.keys("\x03")
	h.waitDone()
}

func TestHubCancelsASlowTurn(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.keys("slow\r")
	h.waitScreen("running", contains("▶"))
	h.keys("\x03")
	h.waitLog("cancel fh-dev-1")
	h.waitScreen("idle", contains("ready"))
}

func TestHubWorkspacePaneAndBoardWorkForTheFocusedConversation(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.keys("\x18") // ctrl+x
	h.waitScreen("the pane", contains("nothing yet"))
	h.keys("b")
	h.waitScreen("the board", contains("This agent has no board."))
	h.keys("\x18")
	h.waitScreen("the conversation", func(s string) bool { return !strings.Contains(s, "nothing yet") })
	// The pane is the conversation's: another role's has its own.
	h.keys(ctrlN)
	s := h.waitScreen("mail", contains("No conversation with Mail"))
	if strings.Contains(s, "nothing yet") {
		t.Errorf("the pane followed to another role:\n%s", s)
	}
}

func TestHubScrollsTheConversation(t *testing.T) {
	cfg := threeRoles()
	var hist []fakehub.Said
	for i := 0; i < 30; i++ {
		hist = append(hist, fakehub.Said{User: "q" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Agent: "reply number " + string(rune('A'+i%26)) + string(rune('a'+i/26))})
	}
	cfg.Roles[0].Live = &fakehub.Live{Unread: true, History: hist}
	h := startHub(t, cfg, 100, 24, "dev")
	h.waitScreen("the end", contains("reply number Db"))
	h.waitLog("seen fh-dev-1 15")
	h.keys("\x1b[5~") // page up
	s := h.waitScreen("scrolled", contains("more"))
	if strings.Contains(s, "reply number Db") {
		t.Errorf("the end is still showing:\n%s", s)
	}
	h.keys("\x1b[6~") // page down
	h.waitScreen("back at the end", contains("reply number Db"))
}

// With the pane beside the conversation (160 columns) the text area is as
// wide as the box drawn, so a long draft wraps and every character shows.
func TestHubLongDraftIsAllVisibleWithThePaneBeside(t *testing.T) {
	h := startHub(t, threeRoles(), 160, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.keys(strings.Repeat("z", 100))
	h.waitScreen("the draft", func(s string) bool { return count(s, "z") == 100 })
}

// A paste in a hub conversation becomes an untrusted placeholder.
func TestHubPasteBecomesAnUntrustedPlaceholder(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.keys("\x1b[200~line one\nline two\x1b[201~")
	h.waitScreen("the placeholder", contains("untrusted #1"))
}

func manyTurns() fakehub.Config {
	cfg := threeRoles()
	var hist []fakehub.Said
	for i := 0; i < 30; i++ {
		hist = append(hist, fakehub.Said{User: "q" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Agent: "reply number " + string(rune('A'+i%26)) + string(rune('a'+i/26))})
	}
	cfg.Roles[0].Live = &fakehub.Live{Unread: true, History: hist}
	return cfg
}

// A long request is read by scrolling: Page Up moves the view while a
// request waits.
func TestHubPageKeysScrollWhileARequestWaits(t *testing.T) {
	h := startHub(t, manyTurns(), 100, 30, "dev")
	h.waitScreen("the end", contains("reply number Db"))
	h.say("ask dev")
	h.waitScreen("the request", contains("permission needed"))
	for i := 0; i < 10; i++ {
		h.keys("\x1b[5~")
	}
	h.waitScreen("scrolled", contains("↓ "))
	time.Sleep(200 * time.Millisecond)
	if got := h.logged("permission "); len(got) != 0 {
		t.Fatalf("a page key answered the request: %v", got)
	}
}

// In a window too small to show the request, y approves nothing, and the
// view says why.
func TestHubTooSmallWindowCannotApprove(t *testing.T) {
	h := startHub(t, threeRoles(), 40, 8, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.say("ask dev " + strings.Repeat("x", 500))
	h.waitScreen("the notice", contains("too small"))
	time.Sleep(800 * time.Millisecond)
	h.keys("y")
	time.Sleep(300 * time.Millisecond)
	if got := h.logged("permission "); len(got) != 0 {
		t.Fatalf("approved in a window too small to read the request: %v", got)
	}
}

// Ctrl-Y in a hub conversation marks the latest paste as trusted, after
// the fact, and a paste is untrusted whenever it arrives.
func TestHubCtrlYTrustsTheLatestPasteInTheInput(t *testing.T) {
	h := startHub(t, threeRoles(), 100, 24, "dev")
	h.waitScreen("dev", contains("hello from dev"))
	h.keys("\x19")
	h.waitScreen("nothing to trust", contains("no untrusted paste in the input to trust"))
	h.keys(bracketed("first"))
	h.keys(bracketed("second"))
	h.waitScreen("two placeholders", func(s string) bool { return strings.Contains(s, "untrusted #2]") })
	h.keys("\x19")
	h.waitScreen("only the latest", func(s string) bool {
		return strings.Contains(s, "trusted #2]") && !strings.Contains(s, "untrusted #2]") && strings.Contains(s, "untrusted #1]")
	})
}
