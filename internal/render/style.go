// Package render turns core state into terminal text. It is pure: state,
// a width and a time in, strings out.
//
// The look is plain on purpose: no colours, only bold, italic and faint,
// so it reads the same on light and dark themes. Every line it returns is
// self-contained: it opens the styles it needs and resets them at its end,
// so lines can be printed, cut or truncated one at a time.
package render

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// style is a set of SGR attributes.
type style uint8

const (
	bold style = 1 << iota
	faint
	italic
)

// sgr is the sequence that switches the terminal to exactly st.
func sgr(st style) string {
	if st == 0 {
		return "\x1b[m"
	}
	codes := "0"
	if st&bold != 0 {
		codes += ";1"
	}
	if st&faint != 0 {
		codes += ";2"
	}
	if st&italic != 0 {
		codes += ";3"
	}
	return "\x1b[" + codes + "m"
}

// styled wraps text, which has no escapes of its own, in st.
func styled(st style, text string) string {
	if st == 0 || text == "" {
		return text
	}
	return sgr(st) + text + sgr(0)
}

var sgrRE = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// parseSGR returns the style a sequence made by sgr switches to.
func parseSGR(seq string) style {
	var st style
	for _, c := range strings.Split(sgrRE.FindStringSubmatch(seq)[1], ";") {
		switch n, _ := strconv.Atoi(c); n {
		case 1:
			st |= bold
		case 2:
			st |= faint
		case 3:
			st |= italic
		}
	}
	return st
}

// selfContained makes each line open the style the previous line left
// on, and reset at its end.
func selfContained(lines []string) []string {
	var cur style
	out := make([]string, len(lines))
	for i, l := range lines {
		open := cur
		for _, seq := range sgrRE.FindAllString(l, -1) {
			cur = parseSGR(seq)
		}
		if open != 0 {
			l = sgr(open) + l
		}
		if cur != 0 {
			l += sgr(0)
		}
		out[i] = l
	}
	return out
}

// sanitize drops control characters, including ESC, so text from the agent
// cannot move the cursor or change styles, and format characters. Newlines stay; a tab becomes a
// space.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			return -1
		case unicode.Is(unicode.Cf, r) && r != 0x200d:
			// Format characters (bidi controls, U+0600 and the like) have
			// no width of their own but change how their neighbours
			// cluster, which breaks width sums. The zero-width joiner stays
			// for emoji.
			return -1
		}
		return r
	}, s)
}

// oneLine is sanitized text with line breaks turned into spaces.
func oneLine(s string) string { return strings.ReplaceAll(sanitize(s), "\n", " ") }

// expandTabs replaces tabs with spaces to the next multiple of 4 columns,
// then sanitizes.
func expandTabs(line string) string {
	if !strings.Contains(line, "\t") {
		return sanitize(line)
	}
	var b strings.Builder
	col := 0
	for _, r := range line {
		if r == '\t' {
			n := 4 - col%4
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += ansi.StringWidth(string(r))
	}
	return sanitize(b.String())
}

// wrap word-wraps styled text to width and returns self-contained lines.
// ansi.Wrap can leave a line too wide (it does with "  -  ·   -" at 8
// columns), so any such line is then cut hard.
func wrap(s string, width int) []string {
	width = max(width, 1)
	var lines []string
	for _, l := range strings.Split(ansi.Wrap(s, width, ""), "\n") {
		if ansi.StringWidth(l) > width {
			lines = append(lines, strings.Split(ansi.Hardwrap(l, width, true), "\n")...)
		} else {
			lines = append(lines, l)
		}
	}
	return selfContained(lines)
}

// hardwrap cuts unstyled text at width without looking for word breaks.
func hardwrap(s string, width int) []string {
	return strings.Split(ansi.Hardwrap(s, max(width, 1), true), "\n")
}

// truncate cuts one line to width, ending it with … when cut. ansi's
// Truncate can measure a cluster differently from StringWidth (it did
// with "A" and U+FE0F), so the result is measured and cut again if needed.
func truncate(s string, width int) string {
	if ansi.StringWidth(s) <= width {
		return s
	}
	for n := width; n > 0; n-- {
		r := selfContained([]string{ansi.Truncate(s, n, "…")})[0]
		if ansi.StringWidth(r) <= width {
			return r
		}
	}
	return ""
}
