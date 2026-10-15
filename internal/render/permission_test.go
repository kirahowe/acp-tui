package render

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"pgregory.net/rapid"

	"github.com/kirahowe/acp-tui/internal/core"
)

// P-perm4: whatever the agent puts in a request, the live view stays
// within the screen, every row within the width, and no escape sequence
// of the agent's gets through.
func TestPermissionViewBounded(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		text := rapid.OneOf(rapid.String(), rapid.SampledFrom([]string{"", "\x1b[2J", "世界\n\n", `{"a":"` + strings.Repeat("x", 300) + `"}`})).Draw(t, "text")
		var opts []core.PermOption
		for range rapid.IntRange(0, 4).Draw(t, "n") {
			opts = append(opts, core.PermOption{ID: "o", Name: rapid.String().Draw(t, "name"),
				Kind: rapid.SampledFrom([]core.OptionKind{core.AllowOnce, core.RejectOnce, core.AllowAlways, "other"}).Draw(t, "kind")})
		}
		s, _ := core.Reduce(core.State{}, core.Connected{At: t0})
		s, _ = core.Reduce(s, core.Submit{At: t0, Text: "x"})
		for range rapid.IntRange(1, 3).Draw(t, "asks") {
			s, _ = core.Reduce(s, core.PermissionAsked{At: t0, Permission: core.Permission{ID: 1, Name: text, Title: text, Input: text, Options: opts}})
		}
		width := rapid.IntRange(1, 90).Draw(t, "width")
		height := rapid.IntRange(2, 40).Draw(t, "height")
		now := t0.Add(time.Duration(rapid.IntRange(0, 2000).Draw(t, "ms")) * time.Millisecond)
		f := View(s, "› ", "", now, width, height)
		rows := strings.Split(f.Content, "\n")
		if len(rows) != height-1 {
			t.Fatalf("%d rows, want %d", len(rows), height-1)
		}
		for _, r := range rows {
			if w := ansi.StringWidth(r); w > width {
				t.Fatalf("row %q is %d wide at %d", r, w, width)
			}
		}
		// Only our own SGR sequences are in the output.
		stripped := sgrRE.ReplaceAllString(f.Content, "")
		if strings.ContainsRune(stripped, 0x1b) || strings.ContainsRune(stripped, 0x07) {
			t.Fatalf("an escape got through: %q", stripped)
		}
	})
}

// With a request on offer the live view shows the tool, its input and
// the option keys, and the long input is cut with the way to see it all.
func TestPermissionBoxContent(t *testing.T) {
	s, _ := core.Reduce(core.State{}, core.Connected{At: t0})
	s, _ = core.Reduce(s, core.Submit{At: t0, Text: "x"})
	var in []string
	for i := range 40 {
		in = append(in, `"k`+strings.Repeat("0", i%3)+`":1`)
	}
	s, _ = core.Reduce(s, core.PermissionAsked{At: t0, Permission: core.Permission{ID: 1, Name: "memo_send",
		Input:   "{" + strings.Join(in, ",") + "}",
		Options: []core.PermOption{{ID: "a", Name: "Allow once", Kind: core.AllowOnce}, {ID: "r", Name: "Reject", Kind: core.RejectOnce}}}})
	out := ansi.Strip(View(s, "› ", "", t0.Add(time.Second), 60, 30).Content)
	for _, want := range []string{"permission needed", "memo_send", `"k": 1`, "more lines · v to see all", "Allow once (y)", "▸ Reject (n)", "esc cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from:\n%s", want, out)
		}
	}
}
