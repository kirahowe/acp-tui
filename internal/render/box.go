package render

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/kirahowe/acp-tui/internal/core"
)

// minBox is the narrowest width that gets a box. Below it, the content is
// shown without one, so a box is never drawn broken.
const minBox = 8

// boxed reports whether width is wide enough for a box.
func boxed(width int) bool { return width >= minBox }

// box draws lines in a faint rounded box exactly width wide, with title in
// the top border. Each line is cut or padded to the inside width.
func box(title string, lines []string, width int) []string {
	if !boxed(width) {
		var out []string
		if title != "" {
			out = append(out, truncate(oneLine(title), width))
		}
		for _, l := range lines {
			out = append(out, truncate(l, width))
		}
		return out
	}
	inner := width - 4
	top := styled(faint, "╭"+strings.Repeat("─", width-2)+"╮")
	if title = oneLine(title); title != "" {
		title = truncate(title, width-6)
		top = row(styled(faint, "╭─")+" "+title+" ", "─", styled(faint, "╮"), width)
	}
	out := []string{top}
	for _, l := range lines {
		out = append(out, row(styled(faint, "│")+" "+truncate(l, inner), " ", " "+styled(faint, "│"), width))
	}
	return append(out, styled(faint, "╰"+strings.Repeat("─", width-2)+"╯"))
}

// row fills the gap between left and right with fill to make a row
// exactly width wide. It measures the joined row, not the parts: a mark
// that combines with its neighbour makes the parts' widths add up wrong.
func row(left, fill, right string, width int) string {
	n := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	for range 3 {
		r := left + styled(faint, strings.Repeat(fill, max(n, 0))) + right
		w := ansi.StringWidth(r)
		if w == width || n < 0 {
			return r
		}
		n += width - w
	}
	return left + styled(faint, strings.Repeat(fill, max(n, 0))) + right
}

// InputWidth is how wide the input box's text area is at width.
func InputWidth(width int) int {
	if boxed(width) {
		return width - 4
	}
	return max(width, 1)
}

// keyHints is the banner's reminder of the keys that stop things.
const keyHints = "ctrl-c cancel · ctrl-d quit"

// bannerLines draws the session banner: the title in the border, the
// agent and model, and the keys that stop things.
func bannerLines(e core.Entry, width int) []string {
	var body []string
	if e.Text != "" {
		body = append(body, oneLine(e.Text))
	}
	body = append(body, styled(faint, keyHints))
	return box(e.Title, body, width)
}
