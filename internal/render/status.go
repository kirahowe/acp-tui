package render

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/x/ansi"
	"time"

	"github.com/kirahowe/acp-tui/internal/core"
)

// StatusText formats the status line: faint, one row, at most width wide.
// While the agent works it starts with a spinner, so a moving spinner
// means the TUI is alive, and "no output for" means the agent is quiet.
// hint says how to type a new line; it is shown when idle. Context use
// shows once the agent has reported it.
func StatusText(l core.StatusLine, hint string, now time.Time, width int) string {
	var parts []string
	add := func(s string) {
		if s != "" {
			parts = append(parts, s)
		}
	}
	lead := spinner(now)
	switch l.Kind {
	case core.StatusStarting:
		add("starting")
	case core.StatusIdle:
		lead = " "
		add("ready")
	case core.StatusExited:
		lead = " "
		add("the agent exited")
	case core.StatusWaiting:
		add("waiting")
	case core.StatusThinking:
		add("thinking")
	case core.StatusWriting:
		add("writing")
	case core.StatusRunning:
		if l.Tools == 1 {
			add("running " + oneLine(l.Tool))
		} else {
			add(fmt.Sprintf("running %d tools", l.Tools))
		}
	case core.StatusRetrying:
		add("retrying in " + Duration((l.RetryIn + time.Second - 1).Truncate(time.Second))) // round up: never "0s" with time left
		add(oneLine(l.Reason))
	case core.StatusCancelling:
		lead = " "
		add("cancelling")
	}
	if l.Kind != core.StatusIdle && l.Kind != core.StatusStarting && l.Kind != core.StatusExited {
		add(Duration(l.Elapsed))
	}
	if l.Quiet > 0 {
		add("no output for " + Duration(l.Quiet))
	}
	if l.Queued > 0 {
		add(strconv.Itoa(l.Queued) + " queued")
	}
	if l.HasContext {
		add(strconv.Itoa(l.Context) + "% context")
	}
	if l.Kind == core.StatusIdle {
		add(hint)
	}
	// The first part always shows, cut if it must be. Each later part
	// shows only if it fits whole, so a narrow terminal loses the least
	// important ones instead of showing half a word.
	if len(parts) == 0 {
		parts = []string{""}
	}
	line := lead + " " + parts[0]
	for _, p := range parts[1:] {
		if next := line + " · " + p; ansi.StringWidth(next) <= width {
			line = next
		}
	}
	return styled(faint, truncate(line, width))
}

// Duration formats d as 9s, 1m02s or 1h04m, rounding down.
func Duration(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}
