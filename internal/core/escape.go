package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file is how text the agent wrote is shown where the person is
// deciding something. Nothing is dropped: a character that cannot be seen
// as itself shows as a \u escape, so what is approved is what runs. The
// escapes are JSON's, so the text reads back to the original.

// escapeRune appends r to b, as itself when it prints, else as \uXXXX
// (a surrogate pair above U+FFFF, as JSON does).
func escapeRune(b *strings.Builder, r rune) {
	switch {
	case r > 0xFFFF && r <= unicode.MaxRune:
		r -= 0x10000
		fmt.Fprintf(b, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
	default:
		fmt.Fprintf(b, `\u%04x`, r)
	}
}

// printable says whether r can be shown as itself. A space is the only
// blank that can: unicode.IsPrint rejects every other separator, the
// controls, the format characters (bidi marks among them), private use,
// surrogates and unassigned code points.
func printable(r rune) bool { return unicode.IsPrint(r) }

// EscapeNonPrinting escapes every character in s that does not print,
// except newline, which the caller uses for layout. Backslashes and
// quotes stay as they are, so it suits text that is already JSON.
func EscapeNonPrinting(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			b.WriteString(`�`) // a byte that is not UTF-8
		case r == '\n' || printable(r):
			b.WriteRune(r)
		default:
			escapeRune(&b, r)
		}
		i += n
	}
	return b.String()
}

// QuoteJSON is s as a JSON string literal with every non-printing
// character escaped. It is what text that is not JSON shows as.
func QuoteJSON(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	return EscapeNonPrinting(strings.TrimRight(b.String(), "\n"))
}

// FaithfulLines is a call's input, as JSON arguments, for the person to
// read: indented, one member per line, with non-printing characters
// escaped, so joined with newlines it parses back to the same value. Text
// that is not JSON shows as one quoted string. Nothing is cut.
func FaithfulLines(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	if json.Valid([]byte(input)) {
		var b bytes.Buffer
		if json.Indent(&b, []byte(input), "", "  ") == nil {
			return strings.Split(EscapeNonPrinting(b.String()), "\n")
		}
	}
	return []string{QuoteJSON(input)}
}

// EscapeLabel is a name or title on one line: backslash and the
// non-printing characters escaped, quotes left alone.
func EscapeLabel(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			b.WriteString(`�`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case printable(r):
			b.WriteRune(r)
		default:
			escapeRune(&b, r)
		}
		i += n
	}
	return b.String()
}
