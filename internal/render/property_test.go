package render

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"pgregory.net/rapid"

	"github.com/kirahowe/acp-tui/internal/core"
)

var wordGen = rapid.StringMatching(`[a-z0-9]{1,8}`)

// genInline is one line of words with inline markup, and its words.
func genInline(t *rapid.T) (string, []string) {
	var parts, words []string
	for range rapid.IntRange(1, 8).Draw(t, "inlineWords") {
		w := wordGen.Draw(t, "word")
		words = append(words, w)
		switch rapid.IntRange(0, 6).Draw(t, "markup") {
		case 0:
			parts = append(parts, "**"+w+"**")
		case 1:
			parts = append(parts, "*"+w+"*")
		case 2:
			parts = append(parts, "_"+w+"_")
		case 3:
			parts = append(parts, "`"+w+"`")
		case 4:
			parts = append(parts, "["+w+"](https://example.com/"+w+")")
		default:
			parts = append(parts, w)
		}
	}
	return strings.Join(parts, " "), words
}

// genDoc is markdown built from blocks, and the words of its text in
// order. Two lists are never adjacent: a blank line between them would
// make one loose list, which block splitting does not reproduce (the
// accepted risk R5).
func genDoc(t *rapid.T) (string, []string) {
	var blocks, words []string
	lastList := false
	for range rapid.IntRange(0, 8).Draw(t, "blocks") {
		kinds := []string{"para", "heading", "code", "quote", "rule"}
		if !lastList {
			kinds = append(kinds, "list", "ordered")
		}
		kind := rapid.SampledFrom(kinds).Draw(t, "block")
		lastList = kind == "list" || kind == "ordered"
		var lines []string
		for i := range rapid.IntRange(1, 3).Draw(t, "lines") {
			line, ws := genInline(t)
			words = append(words, ws...)
			switch kind {
			case "heading":
				line = "## " + line
			case "code":
				// Code is cut hard, not reflowed, so keep each line
				// narrower than any width tested.
				words = append(words[:len(words)-len(ws)], ws[0])
				line = ws[0]
			case "quote":
				line = "> " + line
			case "list":
				line = "- " + line
			case "ordered":
				line = string(rune('1'+i)) + ". " + line
			case "rule":
				words = words[:len(words)-len(ws)]
				line = "---"
			}
			lines = append(lines, line)
			if kind == "heading" || kind == "rule" {
				break
			}
		}
		block := strings.Join(lines, "\n")
		if kind == "code" {
			block = "```\n" + block + "\n```"
		}
		blocks = append(blocks, block)
	}
	return strings.Join(blocks, "\n\n"), words
}

func plainLines(lines []string) string { return ansi.Strip(strings.Join(lines, "\n")) }

// P7: rendering a message block by block, as core splits it while it
// streams, gives the same text as rendering it whole. This holds for whole
// generated documents. A prefix can differ while it streams: a lone "-"
// that will become a rule parses as a list item and can make the list
// before it loose (risk R5). Only the live tail shows that, briefly.
func TestRenderSplitInvariant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc, _ := genDoc(t)
		width := rapid.IntRange(20, 100).Draw(t, "width")
		s := streaming(doc)
		var agent []core.Entry
		for _, e := range s.Entries {
			if e.Kind == core.Agent {
				agent = append(agent, e)
			}
		}
		got := plainLines(Entries(nil, agent, width, t0))
		want := plainLines(Markdown(doc, width))
		if got != want {
			t.Fatalf("markdown %q in %d blocks\nrendered in blocks:\n%s\nwhole:\n%s", doc, len(agent), got, want)
		}
	})
}

// streaming is a busy state after the agent sent md.
func streaming(md string) core.State {
	s, _ := core.Reduce(core.State{}, core.Connected{At: t0})
	s, _ = core.Reduce(s, core.Submit{At: t0, Text: "go"})
	s, _ = core.Reduce(s, core.TextChunk{At: t0, MessageID: "a1", Text: md})
	return s
}

var wordsRE = regexp.MustCompile(`[a-z0-9]+`)

// P11: every word of the text survives, in order, and no emphasis
// marker does.
func TestNoContentLoss(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		doc, words := genDoc(t)
		width := rapid.IntRange(20, 100).Draw(t, "width")
		out := plainLines(Markdown(doc, width))
		got := wordsRE.FindAllString(out, -1)
		i := 0
		for _, g := range got {
			if i < len(words) && g == words[i] {
				i++
			}
		}
		if i < len(words) {
			t.Fatalf("word %q (%d of %d) missing from\n%s\nfor %q", words[i], i, len(words), out, doc)
		}
		if strings.Contains(out, "**") || strings.Contains(out, "`") {
			t.Fatalf("markers left in\n%s", out)
		}
	})
}

// genText is arbitrary Unicode mixed with markdown and terminal hazards:
// wide and combining characters, emoji, tabs, control bytes, escapes.
func genText(t *rapid.T) string {
	pieces := rapid.SliceOfN(rapid.OneOf(
		rapid.String(),
		rapid.SampledFrom([]string{
			"\n", "\n\n", "\t", "\x1b[31m", "\x07", "\r", "\u0085", "世界", "🎉", "👩‍👩‍👧", "é", "​",
			"**", "*", "`", "```\n", "> ", "- ", "1. ", "# ", "---\n", "    ", "|a|b|\n", "<div>", "[x](y)",
			strings.Repeat("w", 30), strings.Repeat("世", 30),
		}),
	), 0, 20).Draw(t, "pieces")
	return strings.Join(pieces, "")
}

func checkWidth(t *rapid.T, what string, lines []string, width int) {
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > width {
			t.Fatalf("%s: line %q is %d wide, more than %d", what, l, w, width)
		}
	}
}

// P10: nothing render returns is wider than the width it was given.
func TestWidthBound(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		width := rapid.IntRange(8, 120).Draw(t, "width")
		text := genText(t)
		now := t0.Add(time.Duration(rapid.Int64().Draw(t, "nowNs")))
		checkWidth(t, "markdown", Markdown(text, width), width)
		es := []core.Entry{
			{Kind: core.User, Text: text, Final: true},
			{Kind: core.Agent, Text: text, Final: true},
			{Kind: core.Tool, Title: text, Status: core.Running},
			{Kind: core.Tool, Title: text, Detail: text, Status: core.Failed, Final: true},
			{Kind: core.Note, Text: text, Detail: text, Final: true},
			{Kind: core.Banner, Title: text, Text: text, Final: true},
		}
		checkWidth(t, "entries", Entries(nil, es, width, now), width)
		l := core.StatusLine{
			Kind: core.StatusKind(rapid.IntRange(int(core.StatusStarting), int(core.StatusExited)).Draw(t, "kind")),
			Tool: text, Tools: rapid.IntRange(0, 3).Draw(t, "tools"), Reason: text,
			RetryIn: time.Duration(rapid.Int64().Draw(t, "retryIn")),
			Elapsed: time.Duration(rapid.Int64().Draw(t, "elapsed")),
			Queued:  rapid.IntRange(0, 3).Draw(t, "queued"),
			Context: rapid.IntRange(0, 100).Draw(t, "context"), HasContext: rapid.Bool().Draw(t, "hasContext"),
		}
		checkWidth(t, "status", []string{StatusText(l, text, now, width)}, width)

		s := streaming(text)
		s, _ = core.Reduce(s, core.Submit{At: t0, Text: text})
		height := rapid.IntRange(1, 40).Draw(t, "height")
		if rapid.Bool().Draw(t, "usage") {
			s, _ = core.Reduce(s, core.Usage{At: t0, Used: rapid.Uint64().Draw(t, "used"), Size: rapid.Uint64().Draw(t, "size")})
		}
		input := truncate("› "+oneLine(text), InputWidth(width))
		f := View(s, input, text, now, width, height)
		view := strings.Split(f.Content, "\n")
		checkWidth(t, "view", view, width)
		if len(view) > max(height-1, 1) {
			t.Fatalf("view has %d rows for height %d", len(view), height)
		}
		if f.InputY < 0 || f.InputY >= len(view) || f.InputX < 0 || f.InputX >= width {
			t.Fatalf("input at (%d, %d) in a %d-row view", f.InputX, f.InputY, len(view))
		}
	})
}

// Regression: a time before 1970 gave a negative spinner index and
// panicked.
func TestSpinnerBefore1970(t *testing.T) {
	if f := spinner(time.Unix(-1, 0)); f == "" {
		t.Fatal("no frame")
	}
}

// Regression: ansi.Wrap left this line 10 wide at width 8.
func TestWrapHyphensAndSpaces(t *testing.T) {
	for _, l := range wrap("  -  ·   -", 8) {
		if w := ansi.StringWidth(l); w > 8 {
			t.Fatalf("line %q is %d wide", l, w)
		}
	}
}

// A box is either whole, every row exactly the width with its corners in
// place, or, too narrow for one, not drawn at all.
func TestBoxNeverBroken(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		width := rapid.IntRange(1, 120).Draw(t, "width")
		title := genText(t)
		lines := rapid.SliceOfN(rapid.Custom(genText), 0, 4).Draw(t, "lines")
		for i, l := range lines {
			lines[i] = oneLine(l)
		}
		out := box(title, lines, width)
		if !boxed(width) {
			checkWidth(t, "unboxed", out, width)
			for _, l := range out {
				if strings.ContainsAny(ansi.Strip(l), "╭╮╰╯│") && !strings.ContainsAny(oneLine(title)+strings.Join(lines, ""), "╭╮╰╯│") {
					t.Fatalf("a box piece at width %d: %q", width, l)
				}
			}
			return
		}
		if len(out) != len(lines)+2 {
			t.Fatalf("%d rows for %d lines", len(out), len(lines))
		}
		for i, l := range out {
			if w := ansi.StringWidth(l); w != width {
				t.Fatalf("row %d is %d wide, want %d: %q", i, w, width, l)
			}
			plain := []rune(ansi.Strip(l))
			first, last := string(plain[0]), string(plain[len(plain)-1])
			want := [2]string{"│", "│"}
			switch i {
			case 0:
				want = [2]string{"╭", "╮"}
			case len(out) - 1:
				want = [2]string{"╰", "╯"}
			}
			if first != want[0] || last != want[1] {
				t.Fatalf("row %d is %q", i, l)
			}
		}
	})
}

// Regression: the titled top border came out one column short.
func TestTitledBoxWidth(t *testing.T) {
	if w := ansi.StringWidth(box("A", nil, 8)[0]); w != 8 {
		t.Fatalf("top border is %d wide, want 8", w)
	}
}

// Regression: a line starting with a combining mark joined the padding
// space before it, and the row came out a column short.
func TestBoxLineStartingWithCombiningMark(t *testing.T) {
	if w := ansi.StringWidth(box("", []string{"\u0903"}, 8)[1]); w != 8 {
		t.Fatalf("row is %d wide, want 8", w)
	}
	if w := ansi.StringWidth(box("\u0903", nil, 8)[0]); w != 8 {
		t.Fatalf("top border is %d wide, want 8", w)
	}
}

// Regression: U+0600 joins the character after it, so a box row holding
// it came out a column wider than measured.
func TestFormatCharactersDropped(t *testing.T) {
	es := []core.Entry{{Kind: core.Banner, Title: "x", Text: "<div>\u0600 more", Final: true}}
	checkWidthT(t, Entries(nil, es, 9, t0), 9)
}

func checkWidthT(t *testing.T, lines []string, width int) {
	t.Helper()
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > width {
			t.Fatalf("line %q is %d wide, more than %d", l, w, width)
		}
	}
}

// Regression: ansi.Truncate kept "A" plus U+FE0F as one column, and the
// row it went into came out a column wide.
func TestTruncateVariationSelector(t *testing.T) {
	if w := ansi.StringWidth(truncate("AAA\ufe0fAAAAA", 4)); w > 4 {
		t.Fatalf("%d wide, want at most 4", w)
	}
}
