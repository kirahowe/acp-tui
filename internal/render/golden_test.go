package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/golden"

	"github.com/kirahowe/acp-tui/internal/core"
	"github.com/kirahowe/acp-tui/internal/wire"
)

var t0 = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

// requireGolden compares out with testdata/<test name>.golden, with ESC
// shown as ␛ so the files can be read. Run with -update to rewrite them.
func requireGolden(t *testing.T, out string) {
	t.Helper()
	golden.RequireEqual(t, strings.ReplaceAll(out, "\x1b", "␛")+"\n")
}

// render goes no further than core: no Bubble Tea, no SDK.
func TestRenderImportsNoShell(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "os/exec" || strings.HasPrefix(dep, "charm.land/") ||
			strings.HasPrefix(dep, "github.com/charmbracelet/bubbletea") ||
			strings.HasPrefix(dep, "github.com/caelis-labs/") {
			t.Errorf("internal/render depends on %s", dep)
		}
	}
}

var corpus = map[string]string{
	"strong":     "Some **strong** and __also strong__ text.",
	"emphasis":   "Some *em* and _also em_ text.",
	"code_span":  "Run `go test ./...` now.",
	"heading":    "# Title\n\n### Smaller *title*",
	"paragraphs": "A first paragraph that is long enough to need wrapping when the terminal is only forty columns wide.\nIts second source line joins it.\n\nA second paragraph.",
	"bullets":    "- one\n- two\n  - nested\n  - nested **bold**\n* three\n+ four",
	"ordered":    "3. three\n4. four\n5. a long item that wraps onto a second line at forty columns",
	"loose_list": "- one\n\n- two",
	"fenced":     "```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```",
	"indented":   "Code:\n\n    x := 1\n    y := 2",
	"long_code":  "```\n" + strings.Repeat("0123456789", 9) + "\n```",
	"quote":      "> quoted **text** that goes on for a while, enough to wrap at forty\n>\n> second paragraph",
	"links":      "[the docs](https://example.com/docs), [https://x.io](https://x.io) and <https://auto.link>",
	"rule":       "above\n\n---\n\nbelow",
	"table":      "| a | b |\n|---|---|\n| 1 | 2 |",
	"html":       "<div>\n  raw html\n</div>",
	"mixed":      "## Plan\n\nI'll check **three** things:\n\n1. the `go.mod`\n2. the tests\n\n> Note: this is *quick*.\n\n```sh\nmake check\n```\n\nDone. See [the README](README.md).",
	"hostile":    "bad \x1b[31mred\x1b[0m and a bell\x07 and **wide 世界 text 🎉**",
}

func TestMarkdownGolden(t *testing.T) {
	for name, md := range corpus {
		for _, w := range []int{40, 80} {
			t.Run(name+"_"+strconv.Itoa(w), func(t *testing.T) {
				requireGolden(t, strings.Join(Markdown(md, w), "\n"))
			})
		}
	}
}

func TestStatusGolden(t *testing.T) {
	lines := []core.StatusLine{
		{Kind: core.StatusStarting},
		{Kind: core.StatusIdle},
		{Kind: core.StatusIdle, Queued: 2},
		{Kind: core.StatusWaiting, Elapsed: 2*time.Minute + 10*time.Second, Quiet: 45 * time.Second},
		{Kind: core.StatusThinking, Elapsed: 14 * time.Second},
		{Kind: core.StatusWriting, Elapsed: 3 * time.Second, Queued: 1},
		{Kind: core.StatusRunning, Tool: `echo {"text":"hi"}`, Tools: 1, Elapsed: 62 * time.Second},
		{Kind: core.StatusRunning, Tools: 3, Elapsed: time.Hour + 4*time.Minute},
		{Kind: core.StatusRetrying, RetryIn: 8500 * time.Millisecond, Reason: "rate limit exceeded", Elapsed: 41 * time.Second},
		{Kind: core.StatusCancelling, Elapsed: 3 * time.Second},
		{Kind: core.StatusExited},
	}
	var out []string
	for _, l := range lines {
		out = append(out, StatusText(l, "shift+enter for a new line", t0, 80))
	}
	out = append(out, StatusText(lines[6], "", t0, 20))
	requireGolden(t, strings.Join(out, "\n"))
}

func TestToolLinesGolden(t *testing.T) {
	tool := func(st core.ToolStatus, title, detail string) core.Entry {
		return core.Entry{Kind: core.Tool, ID: "c", Title: title, Status: st, Detail: detail, Final: st != core.Running && st != core.Pending}
	}
	es := []core.Entry{
		tool(core.Pending, "bash ls -la", ""),
		tool(core.Running, "bash ls -la", ""),
		tool(core.OK, "bash ls -la", ""),
		tool(core.Failed, "read_file go.mod", "no such file"),
		tool(core.Stopped, "echo hi", ""),
		tool(core.OK, "bash "+strings.Repeat("very long arguments ", 6), ""),
		tool(core.OK, "検索 東京の天気を調べて、明日の予報を教えてください。それから大阪も", ""),
		tool(core.OK, "multi\nline\x1b[31m title", ""),
	}
	requireGolden(t, strings.Join(Entries(nil, es, 60, t0), "\n"))
}

func TestViewGolden(t *testing.T) {
	ready, _ := core.Reduce(core.State{}, core.Connected{At: t0})
	busy, _ := core.Reduce(ready, core.Submit{At: t0, Text: "how big is the repo?"})
	busy, _ = core.Reduce(busy, core.TextChunk{At: t0, MessageID: "a1", Text: "Let me check. It has **three** packages"})
	queued, _ := core.Reduce(busy, core.Submit{At: t0, Text: "and the tests?\nall of them"})
	queued, _ = core.Reduce(queued, core.Submit{At: t0, Text: "thanks"})
	long, _ := core.Reduce(busy, core.TextChunk{At: t0, MessageID: "a1", Text: "\n\n```\n" + strings.Repeat("line\n", 30)})
	input := "› " + "\x1b[7m \x1b[m"
	now := t0.Add(14 * time.Second)
	cases := map[string]core.State{"idle": ready, "busy": busy, "queued": queued, "long_block": long}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			requireGolden(t, View(s, input, "ctrl+j for a new line", now, 60, 16))
		})
	}
}

// Each agentloop fixture, as it would reach scrollback at 80 columns,
// then the live view after it.
func TestAgentloopGolden(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "agentloop", "*.jsonl"))
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			events, err := wire.Replay(b, t0)
			if err != nil {
				t.Fatal(err)
			}
			requireGolden(t, scrollback(append([]core.Event{
				core.Connected{At: t0}, core.TerminalReady{At: t0}, core.Submit{At: t0, Text: "hi"},
			}, events...), 80))
		})
	}
}

// scrollback runs events through core, printing as the shell will, and
// returns the scrollback, a rule, and the final live view.
func scrollback(events []core.Event, width int) string {
	var s core.State
	var out []string
	for _, e := range events {
		var fx []core.Effect
		s, fx = core.Reduce(s, e)
		for _, f := range fx {
			if p, ok := f.(core.Print); ok {
				var prev *core.Entry
				if p.From > 0 {
					prev = &s.Entries[p.From-1]
				}
				out = append(out, Entries(prev, p.Entries, width, t0)...)
				s, _ = core.Reduce(s, core.Printed{At: t0, Upto: p.To})
			}
		}
	}
	return strings.Join(out, "\n") + "\n" + strings.Repeat("~", 20) + "\n" + View(s, "› ", "", t0, width, 24)
}
