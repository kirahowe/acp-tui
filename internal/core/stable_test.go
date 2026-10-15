package core

import (
	"strings"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"
)

// genMarkdown builds text from pieces that matter to block splitting:
// blank lines, fences of both kinds and lengths, indents, and wide runes.
func genMarkdown(t *rapid.T) string {
	pieces := rapid.SliceOfN(rapid.SampledFrom([]string{
		"a", "word ", " ", "\n", "\n\n", " \t\n", "\r\n", "```", "````", "~~~", "```go\n",
		"``` x`y\n", "    ", "`", "- ", "# ", "é", "世界", "**b**",
	}), 0, 60).Draw(t, "pieces")
	return strings.Join(pieces, "")
}

// P6: stable(p) is a prefix of stable(t) for every prefix p of t, and the
// split never loses or adds text.
func TestSplitStableMonotonic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		text := genMarkdown(t)
		full, _ := SplitStable(text)
		for i := 0; i <= len(text); i++ {
			if i < len(text) && !utf8.RuneStart(text[i]) {
				continue
			}
			p := text[:i]
			stable, tail := SplitStable(p)
			if stable+tail != p {
				t.Fatalf("SplitStable(%q) = %q + %q", p, stable, tail)
			}
			if !strings.HasPrefix(full, stable) {
				t.Fatalf("stable(%q) = %q is not a prefix of stable(%q) = %q", p, stable, text, full)
			}
			// The stable part ends outside a fence, so it is stable on its own.
			if s2, t2 := SplitStable(stable); s2 != stable || t2 != "" {
				t.Fatalf("SplitStable(%q) = %q + %q, want all stable", stable, s2, t2)
			}
		}
	})
}

func TestSplitStable(t *testing.T) {
	cases := []struct{ in, stable string }{
		{"", ""},
		{"one", ""},
		{"one\n\n", "one\n\n"},
		{"one\n\ntwo", "one\n\n"},
		{"one\n\ntwo\n\nthr", "one\n\ntwo\n\n"},
		{"one\n  \t\ntwo", "one\n  \t\n"},
		{"```\na\n\nb", ""},
		{"```\na\n\nb\n```\n\nc", "```\na\n\nb\n```\n\n"},
		{"~~~\na\n```\n\nb", ""},        // a backtick line does not close a tilde fence
		{"````\na\n```\n\nb", ""},       // a shorter run does not close it
		{"    ```\n\nb", "    ```\n\n"}, // four spaces: indented code, not a fence
		{"``` a`b\n\nc", "``` a`b\n\n"}, // backtick in the info string: not a fence
		{"one\n", ""},
	}
	for _, c := range cases {
		stable, tail := SplitStable(c.in)
		if stable != c.stable || tail != c.in[len(c.stable):] {
			t.Errorf("SplitStable(%q) = %q, %q; want stable %q", c.in, stable, tail, c.stable)
		}
	}
}
