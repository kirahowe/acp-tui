package render

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var parser = goldmark.DefaultParser()

// Markdown renders agent markdown as terminal lines at most width wide.
// Blocks are separated by one blank line. Emphasis becomes bold or italic
// and the markers go; code is indented and faint; quotes get a bar; lists
// get bullets. Tables and raw HTML are shown as written.
func Markdown(md string, width int) []string {
	src := []byte(md)
	m := &mdRenderer{src: src}
	return m.blocks(parser.Parse(text.NewReader(src)), width, 0, false)
}

type mdRenderer struct{ src []byte }

// minInner is the narrowest a nested block may get before its indent is
// dropped, so deep nesting cannot push text past the width.
const minInner = 4

// blocks renders the block children of n, a blank line between each
// unless tight (the items of a tight list).
func (m *mdRenderer) blocks(n ast.Node, width int, base style, tight bool) []string {
	var out []string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		lines := m.block(c, width, base)
		if len(lines) == 0 {
			continue
		}
		if len(out) > 0 && !tight {
			out = append(out, "")
		}
		out = append(out, lines...)
	}
	return out
}

func (m *mdRenderer) block(n ast.Node, width int, base style) []string {
	switch n := n.(type) {
	case *ast.Paragraph:
		if m.isTable(n) {
			return m.verbatim(n.Lines(), width, base)
		}
		return wrap(m.inline(n, base), width)
	case *ast.TextBlock:
		return wrap(m.inline(n, base), width)
	case *ast.Heading:
		return wrap(m.inline(n, base|bold), width)
	case *ast.ThematicBreak:
		return []string{styled(base|faint, strings.Repeat("─", min(width, 40)))}
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		return indent(m.verbatim(n.Lines(), width-2, base|faint), "  ", "  ", width)
	case *ast.HTMLBlock:
		lines := n.Lines()
		if n.HasClosure() {
			lines.Append(n.ClosureLine)
		}
		return m.verbatim(lines, width, base)
	case *ast.Blockquote:
		bar := styled(base|faint, "│ ")
		return indent(m.blocks(n, width-2, base|faint, false), bar, bar, width)
	case *ast.List:
		return m.list(n, width, base)
	}
	// Anything else: its text, so nothing is lost.
	return wrap(m.inline(n, base), width)
}

func (m *mdRenderer) list(l *ast.List, width int, base style) []string {
	var out []string
	num := l.Start
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "• "
		if l.IsOrdered() {
			marker = strconv.Itoa(num) + ". "
			num++
		}
		pad := strings.Repeat(" ", ansi.StringWidth(marker))
		lines := m.blocks(item, width-len(pad), base, l.IsTight)
		if len(lines) == 0 {
			lines = []string{""}
		}
		if !l.IsTight && len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, indent(lines, marker, pad, width)...)
	}
	return out
}

// indent puts first before the first line and rest before the others,
// unless that would leave less than minInner columns.
func indent(lines []string, first, rest string, width int) []string {
	if width-ansi.StringWidth(first) < minInner {
		return lines
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		switch {
		case i > 0 && l == "" && strings.TrimSpace(rest) == "":
			// no trailing spaces on a blank line
		case i == 0:
			out[i] = first + l
		default:
			out[i] = rest + l
		}
	}
	return out
}

// verbatim shows source lines as written, cut hard at width.
func (m *mdRenderer) verbatim(segs *text.Segments, width int, st style) []string {
	var out []string
	for i := range segs.Len() {
		seg := segs.At(i)
		line := strings.TrimRight(string(seg.Value(m.src)), "\r\n")
		for _, l := range hardwrap(expandTabs(line), width) {
			out = append(out, styled(st, l))
		}
	}
	return out
}

// isTable reports whether every line of a paragraph starts with a pipe:
// a table, which reflowing would garble.
func (m *mdRenderer) isTable(p *ast.Paragraph) bool {
	lines := p.Lines()
	if lines.Len() < 2 {
		return false
	}
	for i := range lines.Len() {
		if seg := lines.At(i); !strings.HasPrefix(strings.TrimSpace(string(seg.Value(m.src))), "|") {
			return false
		}
	}
	return true
}

// inline renders the inline children of n as one styled string, with
// newlines only where the markdown has hard line breaks.
func (m *mdRenderer) inline(n ast.Node, base style) string {
	var b strings.Builder
	cur := style(0)
	put := func(st style, s string) {
		if s == "" {
			return
		}
		if st != cur {
			b.WriteString(sgr(st))
			cur = st
		}
		b.WriteString(s)
	}
	var walk func(n ast.Node, st style)
	walk = func(n ast.Node, st style) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				put(st, oneLine(string(c.Value(m.src))))
				switch {
				case c.HardLineBreak():
					put(st, "\n")
				case c.SoftLineBreak():
					put(st, " ")
				}
			case *ast.String:
				put(st, oneLine(string(c.Value)))
			case *ast.CodeSpan:
				walk(c, st)
			case *ast.Emphasis:
				if c.Level >= 2 {
					walk(c, st|bold)
				} else {
					walk(c, st|italic)
				}
			case *ast.Link:
				label := m.plain(c)
				walk(c, st)
				if dest := string(c.Destination); dest != label {
					put(st, " ("+oneLine(dest)+")")
				}
			case *ast.Image:
				put(st, "[image: "+m.plain(c)+"] ("+oneLine(string(c.Destination))+")")
			case *ast.AutoLink:
				put(st, oneLine(string(c.URL(m.src))))
			case *ast.RawHTML:
				for i := range c.Segments.Len() {
					seg := c.Segments.At(i)
					put(st, oneLine(string(seg.Value(m.src))))
				}
			default:
				walk(c, st)
			}
		}
	}
	walk(n, base)
	if cur != 0 {
		b.WriteString(sgr(0))
	}
	return b.String()
}

// plain is the text of n's inline children, without styles.
func (m *mdRenderer) plain(n ast.Node) string {
	return ansi.Strip(m.inline(n, 0))
}
