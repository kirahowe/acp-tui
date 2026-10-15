package core

import "strings"

// SplitStable splits streamed markdown into a stable part, which more text
// cannot change the rendering of, and the tail after it. The stable part
// ends at the last blank line outside a fenced code block. Only complete
// lines count, so stable(p) is a prefix of stable(t) whenever p is a
// prefix of t.
//
// Block-level markdown does not look past a blank line, with two
// exceptions accepted here: a list that turns loose after a blank line,
// and a reference definition that comes after its use.
func SplitStable(md string) (stable, tail string) {
	_, tail = splitBlocks(md)
	return md[:len(md)-len(tail)], tail
}

// splitBlocks cuts the stable part of md at every boundary, so the blocks
// do not depend on how the text arrived in chunks.
func splitBlocks(md string) (blocks []string, tail string) {
	var fence string // the opening run while inside a fenced block
	start, pos := 0, 0
	for {
		nl := strings.IndexByte(md[pos:], '\n')
		if nl < 0 {
			break
		}
		line := md[pos : pos+nl]
		pos += nl + 1
		switch {
		case fence != "":
			if ClosesFence(line, fence) {
				fence = ""
			}
		case strings.Trim(line, " \t\r") == "":
			blocks = append(blocks, md[start:pos])
			start = pos
		default:
			fence = OpensFence(line)
		}
	}
	return blocks, md[start:]
}

// OpensFence returns the run of backticks or tildes that opens a fenced
// code block on this line, or "".
func OpensFence(line string) string {
	run := fenceRun(line)
	if run == "" {
		return ""
	}
	info := strings.TrimLeft(line, " ")[len(run):]
	if run[0] == '`' && strings.Contains(info, "`") {
		return "" // CommonMark: a backtick fence's info string has no backticks
	}
	return run
}

// ClosesFence reports whether line closes the fenced block that open
// began.
func ClosesFence(line, open string) bool {
	run := fenceRun(line)
	return run != "" && run[0] == open[0] && len(run) >= len(open) &&
		strings.Trim(strings.TrimLeft(line, " ")[len(run):], " \t\r") == ""
}

// fenceRun returns the leading run of three or more backticks or tildes,
// after at most three spaces of indent.
func fenceRun(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || trimmed == "" || (trimmed[0] != '`' && trimmed[0] != '~') {
		return ""
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == trimmed[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return trimmed[:n]
}
