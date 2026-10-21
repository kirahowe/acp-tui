package ui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kirahowe/acp-tui/internal/core"
)

func hubModel(t *testing.T) Model {
	t.Helper()
	step := func(m Model, msg tea.Msg) Model {
		next, _ := m.Update(msg)
		return next.(Model)
	}
	m := New(nopConn{}, "").Open("dev")
	m = step(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	now := time.Now()
	m = step(m, EventMsg{Event: core.HubConnected{At: now}})
	m = step(m, EventMsg{Event: core.RosterUpdate{At: now, Roles: []core.Role{{ID: "dev", Title: "Dev", Conv: &core.RosterConv{Session: "s1", Turn: core.TurnIdle}}}}})
	m = step(m, EventMsg{Event: core.Attached{At: now, Role: "dev", Session: "s1"}})
	for i := 0; i < 60; i++ {
		m = step(m, EventMsg{Event: core.Routed{Session: "s1", Event: core.UserChunk{At: now, MessageID: fmt.Sprint("u", i), Text: fmt.Sprint("question ", i)}}})
	}
	return m
}

// Page keys scroll the conversation, a pending request included; when the
// workspace pane has the focus they go to the pane.
func TestPageKeysGoToThePaneWhenItHasTheFocus(t *testing.T) {
	step := func(m Model, msg tea.Msg) Model {
		next, _ := m.Update(msg)
		return next.(Model)
	}
	scroll := func(m Model) int { return m.hub.Focused().State.Transcript.Scroll }
	m := hubModel(t)
	m = step(m, press(t, "pgup"))
	if scroll(m) == 0 {
		t.Fatal("page up did not scroll the conversation")
	}
	m = step(m, press(t, "pgdown"))
	m = step(m, press(t, "pgdown"))
	if scroll(m) != 0 {
		t.Fatalf("scroll %d after paging back down", scroll(m))
	}
	// With a request pending the conversation still scrolls.
	ask := core.PermissionAsked{At: time.Now(), Permission: core.Permission{ID: 1, CallID: "c", Options: []core.PermOption{{ID: "r", Kind: core.RejectOnce}}}}
	mp := step(m, EventMsg{Event: core.Routed{Session: "s1", Event: ask}})
	mp = step(mp, press(t, "pgup"))
	if scroll(mp) == 0 {
		t.Fatal("page up did nothing while a request waited")
	}
	// With the pane focused they are the pane's.
	m = step(m, press(t, "ctrl+x"))
	if !m.hub.Focused().State.Pane.Focus {
		t.Fatal("the pane did not take the focus")
	}
	m = step(m, press(t, "pgup"))
	if scroll(m) != 0 {
		t.Fatalf("the conversation scrolled (%d) while the pane had the focus", scroll(m))
	}
}
