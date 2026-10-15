package core

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"pgregory.net/rapid"
)

// genText is arbitrary text, weighted towards what hides: controls,
// bidi marks, zero-width characters, separators, private use, tags,
// unassigned code points, astral characters, backslashes and quotes.
func genText(t *rapid.T, label string) string {
	piece := rapid.OneOf(
		rapid.String(),
		rapid.SampledFrom([]string{"\x00", "\x1b[2J", "\x07", "\u202e", "\u202a", "\u2066", "\u200b", "\u200d", "\ufeff",
			"\u00a0", "\u2028", "\u2029", "\u3000", "\ue000", "\U000e0001", "\U0001f600", "\u0378", "\\", `"`, `\u202e`, "\n", "\t", "\r", "\u00e9", "\u4e16\u754c", "\u0085", "\x7f"}),
	)
	var b strings.Builder
	for range rapid.IntRange(0, 8).Draw(t, label+"N") {
		b.WriteString(piece.Draw(t, label))
	}
	return b.String()
}

// nothing hidden is left: every rune is printable or a space or newline.
func visible(s string) (rune, bool) {
	for _, r := range s {
		if r != '\n' && !unicode.IsPrint(r) {
			return r, false
		}
	}
	return 0, true
}

// P-esc1: whatever string the agent puts in a call's input, the approval
// view has no raw control or format character and reads back, as JSON, to
// exactly the value that was sent.
func TestFaithfulLinesRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		v := map[string]any{genText(t, "key"): genText(t, "val"), "list": []any{genText(t, "item"), 1.5, nil, true}}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Skip()
		}
		var want any
		_ = json.Unmarshal(raw, &want) // what the agent's input is, after invalid UTF-8 is cleaned
		shown := strings.Join(FaithfulLines(string(raw)), "\n")
		if r, ok := visible(shown); !ok {
			t.Fatalf("U+%04X is raw in %q", r, shown)
		}
		var got any
		if err := json.Unmarshal([]byte(shown), &got); err != nil {
			t.Fatalf("does not parse: %v\n%s", err, shown)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("shown %s\nreads back as %#v\nwant %#v", shown, got, want)
		}
	})
}

// A string that is not JSON shows as a quoted string that reads back.
func TestFaithfulLinesPlainText(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genText(t, "text")
		if json.Valid([]byte(s)) || strings.TrimSpace(s) == "" {
			t.Skip()
		}
		lines := FaithfulLines(s)
		if len(lines) != 1 {
			t.Fatalf("%d lines", len(lines))
		}
		if r, ok := visible(lines[0]); !ok {
			t.Fatalf("U+%04X is raw in %q", r, lines[0])
		}
		var got string
		if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
			t.Fatalf("%q does not parse: %v", lines[0], err)
		}
		// Invalid UTF-8 cannot come from JSON; genText has none, but
		// rapid.String is valid too, so the value is exact.
		if got != s {
			t.Fatalf("shown %q reads back as %q, want %q", lines[0], got, s)
		}
	})
}

func TestEscapeKnown(t *testing.T) {
	for in, want := range map[string]string{
		"a\u202eb":                `a\u202eb`,
		"\U000e0001":              `\udb40\udc01`,
		"zero\u200bw":             `zero\u200bw`,
		"nbsp\u00a0x":             `nbsp\u00a0x`,
		"\u00e9\u4e16 \U0001f600": "\u00e9\u4e16 \U0001f600",
		"\x1b[2J":                 `\u001b[2J`,
	} {
		if got := EscapeNonPrinting(in); got != want {
			t.Errorf("EscapeNonPrinting(%q) = %s, want %s", in, got, want)
		}
	}
	if got := EscapeLabel("a\\b\u202e\n\"q\""); got != `a\\b\u202e\n"q"` {
		t.Errorf("EscapeLabel = %s", got)
	}
}

// unescapeLabel undoes EscapeLabel.
func unescapeLabel(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '\\' {
			b.WriteRune(rs[i])
			continue
		}
		i++
		switch rs[i] {
		case '\\':
			b.WriteRune('\\')
		case 'n':
			b.WriteRune('\n')
		case 'r':
			b.WriteRune('\r')
		case 't':
			b.WriteRune('\t')
		case 'u':
			n, _ := strconv.ParseUint(string(rs[i+1:i+5]), 16, 32)
			i += 4
			r := rune(n)
			if r >= 0xD800 && r < 0xDC00 { // the low half follows
				lo, _ := strconv.ParseUint(string(rs[i+3:i+7]), 16, 32)
				i += 6
				r = 0x10000 + (r-0xD800)<<10 + (rune(lo) - 0xDC00)
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestEscapeLabelRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genText(t, "label")
		e := EscapeLabel(s)
		if r, ok := visible(e); !ok || strings.ContainsRune(e, '\n') {
			t.Fatalf("U+%04X or a newline is raw in %q", r, e)
		}
		if got := unescapeLabel(e); got != s {
			t.Fatalf("%q -> %q -> %q", s, e, got)
		}
	})
}

// The note of an answer escapes the label too.
func TestAnswerNoteIsEscaped(t *testing.T) {
	s := busyState()
	s, _ = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: 1, Name: "ok\u202etool", Options: []PermOption{{ID: "a", Kind: AllowOnce}}}})
	s, _ = Reduce(s, PermissionQuick{At: t0.Add(ArmDelay), Kind: AllowOnce})
	if n := s.Entries[len(s.Entries)-1]; n.Text != `allowed: ok\u202etool` {
		t.Fatalf("note %q", n.Text)
	}
}
