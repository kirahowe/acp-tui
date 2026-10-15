package ui

import "strings"

// PrintChunks splits lines into texts of at most room lines, each to be
// its own tea.Println. Bubble Tea #1822: one print taller than the room
// above the view removes the view. A chunk that is one blank line is
// sent as a space, since Bubble Tea drops an empty print. It is pure.
func PrintChunks(lines []string, room int) []string {
	room = max(room, 1)
	var chunks []string
	for len(lines) > 0 {
		n := min(room, len(lines))
		text := strings.Join(lines[:n], "\n")
		if text == "" {
			text = " "
		}
		chunks = append(chunks, text)
		lines = lines[n:]
	}
	return chunks
}
