package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/kirahowe/acp-tui/internal/core"
)

// Hub mode draws the whole screen, from the hub's state alone: a sidebar
// of roles beside the conversation in focus. On a narrow screen it draws
// one or the other, as a phone would: the list of roles, or the
// conversation with a way back to the list.

// HubWideMin is the narrowest drawing width that gets the sidebar beside
// the conversation. The shell draws one column short of the screen, so
// this is a 73-column screen.
const HubWideMin = 72

// HubWide reports whether the sidebar stands beside the conversation.
func HubWide(width int) bool { return width >= HubWideMin }

// SidebarWidth is how wide the sidebar is at width.
func SidebarWidth(width int) int { return min(max(width/4, 22), 30) }

// HubMainWidth is the width the conversation and its input box are drawn
// at.
func HubMainWidth(width int) int {
	if HubWide(width) {
		return width - SidebarWidth(width) - gap
	}
	return width
}

// HubFrame is the whole screen in hub mode. Max is how far the
// conversation can scroll and Page how many lines a page key moves;
// DetailMax is the same for the pane's detail view. When Cursor is set
// the input box is on screen with its first row at InputX, InputY.
type HubFrame struct {
	Content        string
	Max, Page      int
	DetailMax      int
	Cursor         bool
	InputX, InputY int
}

// HubKeys is what the keys do in hub mode, in the list of keys.
var HubKeys = []KeyHelp{
	{[]string{"enter"}, "send"},
	{[]string{"shift+enter", "ctrl+j"}, "new line"},
	{[]string{"ctrl+n", "ctrl+p"}, "next, previous role"},
	{[]string{"alt+1"}, "role 1 (alt+2 for 2, up to 9)"},
	{[]string{"ctrl+l"}, "the role before this one"},
	{[]string{"ctrl+g"}, "list of roles (enter opens, esc back)"},
	{[]string{"ctrl+s"}, "end this conversation, after asking"},
	{[]string{"pgup", "pgdown"}, "scroll the conversation"},
	{[]string{"ctrl+t"}, "show or hide tool calls"},
	{[]string{"ctrl+o"}, "show or hide thinking"},
	{[]string{"ctrl+x"}, "workspace pane"},
	{[]string{"ctrl+y"}, "next paste goes in as trusted text"},
	{[]string{"ctrl+c"}, "stop the turn, clear, or quit"},
	{[]string{"ctrl+d"}, "quit, from an empty input"},
	{[]string{"?"}, "these keys, from an empty input"},
}

// ListKeys is what the keys do while the list of roles has them.
var ListKeys = []KeyHelp{
	{[]string{"up", "down", "k", "j"}, "move"},
	{[]string{"enter"}, "open the role's conversation"},
	{[]string{"esc", "ctrl+g"}, "back to the conversation"},
	{[]string{"alt+1"}, "role 1 (alt+2 for 2, up to 9)"},
	{[]string{"ctrl+c"}, "quit"},
	{[]string{"?"}, "these keys"},
}

// hubLegend explains the marks beside a role.
var hubLegend = []string{
	"▶ replying   … waiting for scheduled work",
	"~ scheduled work runs   ● unread",
	"?N requests to answer   !N held calls",
}

// marks are the indicators beside a role, styled, and their width. They
// are in the order of how much they need the operator.
func marks(h core.Hub, r core.Role, now time.Time) string {
	var parts []string
	if n := h.Approvals(r); n > 0 {
		parts = append(parts, styled(bold|reverse, fmt.Sprintf("?%d", n)))
	}
	if r.Holds > 0 {
		parts = append(parts, styled(bold, fmt.Sprintf("!%d", r.Holds)))
	}
	if r.Conv != nil && r.Conv.Unread {
		parts = append(parts, styled(bold, "●"))
	}
	if r.Conv != nil {
		switch r.Conv.Turn {
		case core.TurnRunning:
			parts = append(parts, "▶")
		case core.TurnQueued:
			parts = append(parts, "…")
		}
	}
	if r.Working {
		parts = append(parts, "~")
	}
	return strings.Join(parts, " ")
}

// roleRow is one role in the list: a pointer for the one in focus, the
// title and the marks at the right, width wide. The cursor's row is in
// reverse video.
func roleRow(h core.Hub, r core.Role, now time.Time, width int) string {
	lead := "  "
	if r.ID == h.Focus {
		lead = styled(bold, "› ")
	}
	m := marks(h, r, now)
	mw := ansi.StringWidth(m)
	room := max(width-2-mw-1, 1)
	title := truncate(oneLine(r.Title), room)
	if r.Conv == nil {
		title = styled(faint, title)
	}
	gapN := max(width-2-ansi.StringWidth(title)-mw, 0)
	line := lead + title + strings.Repeat(" ", gapN) + m
	if h.List && r.ID == h.Cursor {
		line = sgr(reverse) + ansi.Strip(line) + sgr(0)
	}
	return truncate(line, width)
}

// describe says in words where a role stands, for the list screen.
func describe(h core.Hub, r core.Role, now time.Time) string {
	var parts []string
	c := r.Conv
	if c == nil {
		parts = append(parts, "no conversation")
	} else {
		switch c.Turn {
		case core.TurnRunning:
			parts = append(parts, "replying")
		case core.TurnQueued:
			parts = append(parts, "waiting for scheduled work")
		}
		if n := h.Approvals(r); n > 0 {
			parts = append(parts, plural(n, "request")+" for you")
		}
		if c.Unread {
			parts = append(parts, "unread")
		}
		parts = append(parts, plural(c.Turns, "turn"))
		if !c.LastActivity.IsZero() {
			parts = append(parts, Duration(max(now.Sub(c.LastActivity), 0))+" ago")
		}
	}
	if r.Working {
		parts = append(parts, "scheduled work runs")
	}
	if r.Holds > 0 {
		parts = append(parts, plural(r.Holds, "held call"))
	}
	return strings.Join(parts, " · ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// sidebar is the roles beside the conversation: height rows, width wide.
func sidebar(h core.Hub, now time.Time, width, height int) []string {
	lines := []string{styled(bold, truncate("relay", width))}
	switch {
	case !h.RosterLoaded:
		lines = append(lines, styled(faint, truncate("loading roles…", width)))
	case len(h.Roles) == 0:
		lines = append(lines, styled(faint, truncate("no roles", width)))
	}
	for _, r := range h.Roles {
		lines = append(lines, roleRow(h, r, now, width))
	}
	foot := []string{styled(faint, truncate("ctrl+g roles · ? keys", width))}
	room := max(height-len(foot), 0)
	if len(lines) > room {
		lines = lines[:room]
	}
	for len(lines) < room {
		lines = append(lines, "")
	}
	return append(lines, foot...)
}

// listScreen is the narrow screen's list of roles, with a line in words
// under each.
func listScreen(h core.Hub, now time.Time, width, height int) []string {
	lines := []string{styled(bold, truncate("roles", width))}
	switch {
	case !h.RosterLoaded:
		lines = append(lines, styled(faint, truncate("loading roles…", width)))
	case len(h.Roles) == 0:
		lines = append(lines, styled(faint, truncate("no roles", width)))
	}
	for _, r := range h.Roles {
		lines = append(lines, roleRow(h, r, now, width), styled(faint, truncate("  "+describe(h, r, now), width)))
	}
	foot := styled(faint, truncate("enter open · up/down move · ? keys", width))
	if h.Notice != "" {
		foot = styled(faint, truncate("! "+oneLine(h.Notice), width))
	}
	room := max(height-1, 0)
	if len(lines) > room {
		lines = lines[:room]
	}
	for len(lines) < room {
		lines = append(lines, "")
	}
	return append(lines, foot)
}

// HubView draws the whole screen at width, height rows. input is the text
// area as already drawn, InputWidth(HubMainWidth(width)) wide.
func HubView(h core.Hub, input, hint string, now time.Time, width, height int) HubFrame {
	height = max(height, 2)
	wide := HubWide(width)
	if h.Help {
		return withHelp(h, input, hint, now, width, height, wide)
	}
	return hubFrame(h, input, hint, now, width, height, wide, nil)
}

func withHelp(h core.Hub, input, hint string, now time.Time, width, height int, wide bool) HubFrame {
	mw := HubMainWidth(width)
	rows := HubKeys
	title := "keys"
	if h.List {
		rows, title = ListKeys, "list keys"
	}
	help := helpBox(title, rows, mw)
	legend := make([]string, len(hubLegend))
	for i, l := range hubLegend {
		legend[i] = styled(faint, truncate(l, InputWidth(mw)))
	}
	help = append(help, legend...)
	return hubFrame(h, input, hint, now, width, height, wide, help)
}

func hubFrame(h core.Hub, input, hint string, now time.Time, width, height int, wide bool, help []string) HubFrame {
	if wide {
		sw := SidebarWidth(width)
		mw := HubMainWidth(width)
		side := sidebar(h, now, sw, height)
		f := hubMain(h, input, hint, now, mw, height, false, help)
		f.Content = strings.Join(joinColumns(side, strings.Split(f.Content, "\n"), sw), "\n")
		f.InputX += sw + gap
		return f
	}
	if h.List || h.Focus == "" {
		lines := listScreen(h, now, width, height)
		if help != nil {
			lines = append(lines[:max(height-len(help), 0)], help...)
			lines = lines[:min(len(lines), height)]
		}
		return HubFrame{Content: strings.Join(lines, "\n")}
	}
	return hubMain(h, input, hint, now, width, height, true, help)
}

// elsewhere summarises the other roles that want the operator, for the
// narrow conversation's header.
func elsewhere(h core.Hub) string {
	unread, asks := 0, 0
	for _, r := range h.Roles {
		if r.ID == h.Focus {
			continue
		}
		if r.Conv != nil && r.Conv.Unread {
			unread++
		}
		asks += h.Approvals(r)
	}
	var parts []string
	if asks > 0 {
		parts = append(parts, styled(bold|reverse, fmt.Sprintf("?%d", asks)))
	}
	if unread > 0 {
		parts = append(parts, styled(bold, fmt.Sprintf("● %d", unread)))
	}
	return strings.Join(parts, " ")
}

// hubMain is the conversation in focus: a header, the conversation
// bottom-aligned, then queued messages, the input box (or the request
// that takes its place) and the status line. help, when set, covers the
// bottom of the conversation.
func hubMain(h core.Hub, input, hint string, now time.Time, width, height int, narrow bool, help []string) HubFrame {
	role := h.RoleByID(h.Focus)
	conv := h.Focused()
	var s core.State
	if conv != nil {
		s = conv.State
	}

	// The header.
	title := h.Focus
	if role != nil {
		title = role.Title
	}
	head := styled(bold, oneLine(title))
	if narrow {
		head = styled(faint, "‹ ctrl+g roles · ") + head
		if e := elsewhere(h); e != "" {
			head += "  " + e
		}
	}
	if conv != nil && role != nil && role.Conv != nil && role.Conv.Turns > 0 {
		head += styled(faint, " · "+plural(role.Conv.Turns, "turn"))
	}
	scroll := 0

	// The bottom: queued messages, a note, the input or what replaces it,
	// and the status line.
	var bottom []string
	for _, q := range s.Queue {
		first, _, more := strings.Cut(strings.TrimSpace(q), "\n")
		first = oneLine(first)
		if more {
			first += "…"
		}
		bottom = append(bottom, styled(faint, truncate("queued › "+first, width)))
	}
	if h.Notice != "" {
		bottom = append(bottom, styled(faint, truncate("! "+oneLine(h.Notice), width)))
	}
	bottom = append(bottom, "")
	inputAt, x := 0, 0
	showInput := false
	switch {
	case h.EndAsk:
		ask := fmt.Sprintf("End the conversation with %s? y to end it, n to keep it", oneLine(title))
		bottom = append(bottom, box("end conversation", wrap(ask, InputWidth(width)), width)...)
	case len(s.Asks) > 0:
		bottom = append(bottom, permissionBox(s, now, width, height-1-len(bottom)-2)...)
	default:
		inputAt = len(bottom)
		if boxed(width) {
			inputAt++
			x = 2
		}
		bottom = append(bottom, box("", strings.Split(input, "\n"), width)...)
		showInput = true
	}
	var status string
	if conv == nil {
		status = styled(faint, truncate("  no conversation · type a message to start one · ? keys", width))
	} else {
		st := core.Status(s, now)
		if role != nil { // the roster may not have come yet
			st.Labels = st.Labels.Union(role.Labels)
			if role.Conv != nil && role.Conv.Session == conv.Session {
				st.Remote = role.Conv.Turn
			}
		}
		st.PaneHint = st.PaneItems > 0
		status = StatusText(st, hint, now, width)
	}
	bottom = append(bottom, status)

	// The conversation, or what covers it.
	room := max(height-1-len(bottom), 0)
	var lines []string
	mx, detailMax := 0, 0
	switch {
	case s.Pane.Focus && conv != nil && len(s.Asks) == 0:
		lines, detailMax = paneRender(s, now, width, room)
	case conv == nil:
		lines = []string{styled(faint, "No conversation with "+oneLine(title)+" yet.")}
	default:
		ex := expand{tools: s.Transcript.Tools, thinking: s.Transcript.Thinking}
		lines = entriesAs(nil, s.Entries, ex, width, now)
		mx = max(len(lines)-room, 0)
		scroll = min(s.Transcript.Scroll, mx)
		lines = lines[:len(lines)-scroll]
	}
	if scroll > 0 {
		head += styled(faint, fmt.Sprintf(" · ↓ %d more", scroll))
	}
	if help != nil {
		lines = append(lines, help...)
	}
	if len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	pad := make([]string, max(room-len(lines), 0))
	all := append(append(append([]string{truncate(head, width)}, pad...), lines...), bottom...)
	y := 1 + len(pad) + len(lines) + inputAt
	if over := len(all) - height; over > 0 { // a screen too short for the bottom: the top goes
		all = all[over:]
		y -= over
	}
	for i := range all {
		all[i] = truncate(all[i], width)
	}
	return HubFrame{
		Content: strings.Join(all, "\n"), Max: mx, Page: max(room-1, 1), DetailMax: detailMax,
		Cursor: showInput && help == nil && !s.Pane.Focus && y >= 0, InputX: x, InputY: max(y, 0),
	}
}
