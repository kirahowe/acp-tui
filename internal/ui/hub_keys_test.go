package ui

import (
	"testing"

	"github.com/kirahowe/acp-tui/internal/render"
)

// Every key the hub's list of keys names does something there.
func TestHubHelpRowsMatchKeys(t *testing.T) {
	for _, r := range render.HubKeys {
		for _, name := range r.Keys {
			k := press(t, name)
			if a, _ := ClassifyHub(k); a != HubNone {
				continue
			}
			if name == "pgup" || name == "pgdown" { // handled before Classify
				continue
			}
			if a := Classify(k, true); a == PassThrough {
				t.Errorf("hub %q (%s) does nothing", name, r.What)
			}
		}
	}
	for _, r := range render.ListKeys {
		for _, name := range r.Keys {
			k := press(t, name)
			if a, _ := ClassifyHub(k); a != HubNone {
				continue
			}
			if ClassifyList(k) == ListNone {
				t.Errorf("list %q (%s) does nothing", name, r.What)
			}
		}
	}
}

func TestClassifyHub(t *testing.T) {
	for name, want := range map[string]HubAction{"ctrl+n": HubNext, "ctrl+p": HubPrev, "ctrl+g": HubList, "ctrl+l": HubLast,
		"ctrl+s": HubEnd, "alt+1": HubIndex, "alt+9": HubIndex, "ctrl+t": HubNone, "1": HubNone, "alt+0": HubNone} {
		if name == "alt+0" {
			continue // press() would reject it as a plain key; ClassifyHub's range is 1-9
		}
		if got, _ := ClassifyHub(press(t, name)); got != want {
			t.Errorf("%q: %v, want %v", name, got, want)
		}
	}
	if _, i := ClassifyHub(press(t, "alt+3")); i != 2 {
		t.Errorf("alt+3 is index %d", i)
	}
}

func TestClassifyEndOnlyYesEnds(t *testing.T) {
	for name, want := range map[string][2]bool{"y": {true, true}, "n": {true, false}, "esc": {true, false},
		"enter": {false, false}, "x": {false, false}, "ctrl+c": {true, false}} {
		a, y := ClassifyEnd(press(t, name))
		if a != want[0] || y != want[1] {
			t.Errorf("%q: %v %v, want %v", name, a, y, want)
		}
	}
}
