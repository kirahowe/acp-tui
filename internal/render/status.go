package render

import (
	"fmt"
	"math"
	"strconv"

	"github.com/charmbracelet/x/ansi"
	"time"

	"github.com/kirahowe/acp-tui/internal/core"
)

// StatusText formats the status line: faint, one row, at most width wide.
// While the agent works it starts with a spinner, so a moving spinner
// means the TUI is alive, and "no output for" means the agent is quiet.
// hint says how to type a new line; it is shown when idle. Context use
// and spend show once the agent has reported them.
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
		if l.Remote == core.TurnRunning {
			lead = spinner(now)
			parts = []string{"replying"}
		}
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
	case core.StatusAsking:
		lead = "?"
		add("waiting for you")
		if l.Asks > 1 {
			add(strconv.Itoa(l.Asks) + " requests")
		}
	}
	add(oneLine(l.Message))
	if l.Remote == core.TurnQueued {
		add("waiting for the role's scheduled work")
	}
	if l.PaneHint {
		add(fmt.Sprintf("%s workspace (%d)", PaneKey, l.PaneItems))
	}
	if l.Kind != core.StatusIdle && l.Kind != core.StatusStarting && l.Kind != core.StatusExited && !l.NoClock {
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
	if l.Spend != nil {
		add(Money(*l.Spend))
	}
	if l.Kind == core.StatusIdle && l.Remote != core.TurnRunning {
		add(hint)
		add("? keys")
	}
	// The first part always shows, cut if it must be. Each later part
	// shows only if it fits whole, so a narrow terminal loses the least
	// important ones instead of showing half a word.
	if len(parts) == 0 {
		parts = []string{""}
	}
	line := lead + " " + badges(l) + parts[0]
	for _, p := range parts[1:] {
		if next := line + " · " + p; ansi.StringWidth(next) <= width {
			line = next
		}
	}
	return styled(faint, truncate(line, width))
}

// badges shows the session's labels in front of the status, so they are
// always on screen: private in bold, contaminated in reverse video, which
// is the one that must not be missed. Each returns to faint, the style the
// whole line is in, rather than resetting.
func badges(sl core.StatusLine) string {
	l := sl.Labels
	var b string
	if l.Private {
		b += sgr(bold) + "[private]" + sgr(faint) + " "
	}
	if l.Contaminated {
		b += sgr(bold|reverse) + " contaminated " + sgr(faint) + " "
	}
	return b
}

// Money formats a cost to the cent: $1.23 in US dollars, else the
// amount and the currency code. It is empty for an amount that is not a
// number.
func Money(c core.Cost) string {
	if math.IsNaN(c.Amount) || math.IsInf(c.Amount, 0) {
		return ""
	}
	switch cur := oneLine(c.Currency); cur {
	case "USD", "":
		if c.Amount < 0 {
			return fmt.Sprintf("-$%.2f", -c.Amount)
		}
		return fmt.Sprintf("$%.2f", c.Amount)
	default:
		return fmt.Sprintf("%.2f %s", c.Amount, cur)
	}
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
