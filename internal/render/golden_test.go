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
	"strong":              "Some **strong** and __also strong__ text.",
	"emphasis":            "Some *em* and _also em_ text.",
	"code_span":           "Run `go test ./...` now.",
	"heading":             "# Title\n\n### Smaller *title*",
	"paragraphs":          "A first paragraph that is long enough to need wrapping when the terminal is only forty columns wide.\nIts second source line joins it.\n\nA second paragraph.",
	"bullets":             "- one\n- two\n  - nested\n  - nested **bold**\n* three\n+ four",
	"ordered":             "3. three\n4. four\n5. a long item that wraps onto a second line at forty columns",
	"loose_list":          "- one\n\n- two",
	"fenced":              "```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```",
	"indented":            "Code:\n\n    x := 1\n    y := 2",
	"long_code":           "```\n" + strings.Repeat("0123456789", 9) + "\n```",
	"quote":               "> quoted **text** that goes on for a while, enough to wrap at forty\n>\n> second paragraph",
	"links":               "[the docs](https://example.com/docs), [https://x.io](https://x.io) and <https://auto.link>",
	"rule":                "above\n\n---\n\nbelow",
	"table":               "| a | b |\n|---|---|\n| 1 | 2 |",
	"html":                "<div>\n  raw html\n</div>",
	"mixed":               "## Plan\n\nI'll check **three** things:\n\n1. the `go.mod`\n2. the tests\n\n> Note: this is *quick*.\n\n```sh\nmake check\n```\n\nDone. See [the README](README.md).",
	"numbered_after_text": "Two of these:\n3. **Add** one\n4. Add two",
	"numbered_in_list":    "1. one\n   still one\n3. three\n\n```\nText\n2. in code\n```",
	"hostile":             "bad \x1b[31mred\x1b[0m and a bell\x07 and **wide 世界 text 🎉**",
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
		{Kind: core.StatusIdle, Context: 12, HasContext: true},
		{Kind: core.StatusIdle, Context: 12, HasContext: true, Spend: &core.Cost{Amount: 1.234, Currency: "USD"}},
		{Kind: core.StatusWriting, Elapsed: 3 * time.Second, Spend: &core.Cost{Amount: 0.5, Currency: "EUR"}},
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
	// At 40 columns the parts that do not fit whole are left out.
	busy := core.StatusLine{Kind: core.StatusWaiting, Elapsed: 130 * time.Second, Quiet: 45 * time.Second, Queued: 2, Context: 12, HasContext: true}
	out = append(out, StatusText(busy, "", t0, 80), StatusText(busy, "", t0, 40))
	out = append(out, StatusText(core.StatusLine{Kind: core.StatusIdle, Context: 12, HasContext: true}, "shift+enter for a new line", t0, 40))
	requireGolden(t, strings.Join(out, "\n"))
}

func TestToolLinesGolden(t *testing.T) {
	call := func(st core.ToolStatus, name, input, output string) core.Call {
		return core.Call{ID: "c", Name: name, Title: name + " " + input, Status: st, Input: input, Output: output}
	}
	run := func(cs ...core.Call) core.Entry { return core.Entry{Kind: core.Tool, Calls: cs} }
	failed := call(core.Failed, "read_file", `{"path":"go.mod"}`, "no such file\nat /x")
	ok := call(core.OK, "fact_add", `{"topic":"operator/career","text":"A long fact that goes on and on, past the edge of the screen","n":3,"tags":["a","b"]}`, "added\n\tline two\n"+strings.Repeat("more\n", 10))
	es := []core.Entry{
		run(call(core.Pending, "bash", `{"cmd":"ls -la"}`, "")),
		run(call(core.Running, "bash", `{"cmd":"ls -la"}`, "")),
		run(call(core.OK, "roles", "", "")),
		run(failed),
		run(call(core.Stopped, "echo", `{"text":"hi"}`, "")),
		run(call(core.OK, "roles", "", ""), failed, ok, ok, ok, call(core.OK, "role_add", "{}", ""), call(core.OK, "role_add", "{}", ""), call(core.OK, "journal_write", "", "")),
		run(call(core.OK, "", "", ""), core.Call{Title: "検索 東京の天気を調べて、明日の予報を教えてください。それから大阪も", Status: core.OK}),
		run(core.Call{Title: "multi\nline\x1b[31m title", Status: core.OK}),
		{Kind: core.Details, Calls: []core.Call{call(core.OK, "roles", "", ""), failed, ok, call(core.Stopped, "echo", "not json", "")}},
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
			for _, w := range []int{60, 40} {
				f := View(s, input, "ctrl+j for a new line", now, w, 16)
				t.Run(strconv.Itoa(w), func(t *testing.T) { requireGolden(t, f.Content) })
			}
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

// scrollback runs events through core and lays out a 24-row screen after
// each, as the shell does, and returns what scrolled into scrollback, a
// rule, and the final frame.
func scrollback(events []core.Event, width int) string {
	var s core.State
	var out []string
	for _, e := range events {
		s, _ = core.Reduce(s, e)
		f := View(s, "› ", "", t0, width, 24)
		out = append(out, f.Print...)
		s, _ = core.Reduce(s, core.Printed{At: t0, Upto: f.PrintTo})
	}
	return strings.Join(out, "\n") + "\n" + strings.Repeat("~", 20) + "\n" + View(s, "› ", "", t0, width, 24).Content
}

func TestBannerGolden(t *testing.T) {
	banner := func(title, text string) core.Entry {
		return core.Entry{Kind: core.Banner, Title: title, Text: text, Final: true}
	}
	es := []core.Entry{
		banner("relay · hub", "agentloop 0.3 · claude-opus-5-5"),
		banner("", ""),
		banner("a title much too long to fit in the border of a narrow box", "agentloop 0.3 · a model name that is long"),
	}
	var out []string
	for _, w := range []int{80, 40, 12, 7} {
		out = append(out, "width "+strconv.Itoa(w)+":")
		for _, e := range es {
			out = append(out, Entries(nil, []core.Entry{e}, w, t0)...)
		}
	}
	requireGolden(t, strings.Join(out, "\n"))
}
