package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/kirahowe/acp-tui/internal/core"
)

// TranscriptFrame is the full-screen transcript view, as drawn. Max is
// how far it can scroll and Page how many lines a page key moves.
type TranscriptFrame struct {
	Content   string
	Max, Page int
}

// TranscriptView draws the whole session at width, height rows, with
// tool calls and thinking in full as s.Transcript says. The last row is a
// footer: the session's labels, what the agent is doing, and the keys.
// The transcript is bottom-aligned, as inline, and ends Scroll lines
// above its last line. With s.Help the list of keys covers its bottom.
func TranscriptView(s core.State, now time.Time, width, height int) TranscriptFrame {
	rows := max(height-1, 1)
	x := expand{tools: s.Transcript.Tools, thinking: s.Transcript.Thinking}
	lines := entriesAs(nil, s.Entries, x, width, now)
	mx := max(len(lines)-rows, 0)
	scroll := min(s.Transcript.Scroll, mx)
	end := len(lines) - scroll
	lines = lines[max(end-rows, 0):end]
	if s.Help {
		help := helpBox("transcript keys", TranscriptKeys, width)
		lines = append(lines, help...)
		lines = lines[max(len(lines)-rows, 0):]
	}
	pad := make([]string, max(rows-len(lines), 0))
	all := append(append(pad, lines...), transcriptFooter(s, now, scroll, width))
	return TranscriptFrame{Content: strings.Join(all, "\n"), Max: mx, Page: max(rows-1, 1)}
}

// transcriptFooter is the view's status line. The labels lead it, as on
// the inline status line, so contamination is never off screen.
func transcriptFooter(s core.State, now time.Time, scroll, width int) string {
	st := core.Status(s, now)
	lead := " "
	if s.Phase == core.Busy || s.Phase == core.Starting {
		lead = spinner(now)
	}
	parts := []string{"transcript"}
	if scroll > 0 {
		parts = append(parts, fmt.Sprintf("↓ %d more", scroll))
	}
	parts = append(parts, "esc back", toggleWord("ctrl+t", "tools", s.Transcript.Tools),
		toggleWord("ctrl+o", "thinking", s.Transcript.Thinking), "? keys")
	line := lead + " " + badges(st) + parts[0]
	for _, p := range parts[1:] {
		if next := line + " · " + p; ansi.StringWidth(next) <= width {
			line = next
		}
	}
	return styled(faint, truncate(line, width))
}

func toggleWord(key, what string, shown bool) string {
	if shown {
		return key + " hide " + what
	}
	return key + " show " + what
}

// KeyHelp is one row of the list of keys: the keys as Bubble Tea names
// them, and what they do. package ui decides what keys do; its tests
// check these rows against it.
type KeyHelp struct {
	Keys []string
	What string
}

// InlineKeys is what the keys do in the inline view.
var InlineKeys = []KeyHelp{
	{[]string{"enter"}, "send"},
	{[]string{"shift+enter", "ctrl+j"}, "new line"},
	{[]string{"ctrl+t"}, "transcript with tool calls in full"},
	{[]string{"ctrl+o"}, "transcript with thinking in full"},
	{[]string{"ctrl+x"}, "workspace pane"},
	{[]string{"ctrl+y"}, "next paste goes in as trusted text"},
	{[]string{"ctrl+c"}, "stop the turn, clear, or quit"},
	{[]string{"ctrl+d"}, "quit, from an empty input"},
	{[]string{"?"}, "these keys, from an empty input"},
}

// TranscriptKeys is what the keys do in the transcript view.
var TranscriptKeys = []KeyHelp{
	{[]string{"up", "down", "k", "j"}, "scroll"},
	{[]string{"pgup", "pgdown", "space"}, "page"},
	{[]string{"home", "end", "g", "G"}, "top, bottom"},
	{[]string{"ctrl+t", "t"}, "show or hide tool calls"},
	{[]string{"ctrl+o", "o"}, "show or hide thinking"},
	{[]string{"esc", "q"}, "back"},
	{[]string{"?"}, "these keys"},
}

var keyGlyphs = map[string]string{"up": "↑", "down": "↓"}

// helpBox lists keys in a box, at width.
func helpBox(title string, rows []KeyHelp, width int) []string {
	names := make([]string, len(rows))
	w := 0
	for i, r := range rows {
		ks := make([]string, len(r.Keys))
		for j, k := range r.Keys {
			ks[j] = k
			if g, ok := keyGlyphs[k]; ok {
				ks[j] = g
			}
		}
		names[i] = strings.Join(ks, " ")
		w = max(w, ansi.StringWidth(names[i]))
	}
	inner := InputWidth(width)
	var lines []string
	for i, r := range rows {
		lines = append(lines, truncate(names[i]+strings.Repeat(" ", w-ansi.StringWidth(names[i]))+"  "+styled(faint, r.What), inner))
	}
	lines = append(lines, styled(faint, truncate("any key closes this", inner)))
	return box(title, lines, width)
}

// HelpLines is the list of keys for the inline view, bottom-aligned in
// room rows.
func HelpLines(width, room int) []string {
	lines := helpBox("keys", InlineKeys, width)
	if len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	return append(make([]string, max(room-len(lines), 0)), lines...)
}
