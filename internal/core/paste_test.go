package core

import (
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func idleState(embedded bool) State {
	s, _ := Reduce(State{}, Connected{At: t0, Embedded: embedded})
	return s
}

func prompts(fx []Effect) []SendPrompt {
	var out []SendPrompt
	for _, f := range fx {
		if p, ok := f.(SendPrompt); ok {
			out = append(out, p)
		}
	}
	return out
}

// P-paste1: a paste reaches the agent as a resource, never in the prompt
// text, unless the person armed trusted paste first. Pastes whose
// placeholder was deleted are not sent. Each paste is sent once.
func TestPasteAlwaysAttached(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := idleState(true)
		var input string               // the input box, as the shell keeps it
		var held = map[string]string{} // secret -> token, for pastes still in the input
		var trusted []string           // secrets inserted as plain text
		sent := map[string]int{}       // secret -> times attached
		n := 0
		apply := func(e Event) []Effect {
			var fx []Effect
			s, fx = Reduce(s, e)
			for _, f := range fx {
				if in, ok := f.(InsertInput); ok {
					input += in.Text
				}
			}
			return fx
		}
		for range rapid.IntRange(1, 30).Draw(t, "steps") {
			switch rapid.IntRange(0, 5).Draw(t, "step") {
			case 0, 1:
				n++
				secret := fmt.Sprintf("<<S%d>>", n)
				body := secret + strings.Repeat("\nmore text", rapid.IntRange(0, 3).Draw(t, "lines"))
				armed := s.TrustNext
				before := input
				apply(Pasted{At: t0, Text: body})
				if armed {
					trusted = append(trusted, secret)
				} else {
					tok := strings.TrimPrefix(input, before)
					if !strings.HasPrefix(tok, "[pasted ") || strings.Contains(tok, secret) {
						t.Fatalf("the input got %q for a paste", tok)
					}
					held[secret] = tok
				}
			case 2:
				apply(ToggleTrustedPaste{At: t0})
			case 3:
				input += "typed "
			case 4: // delete a placeholder
				for secret, tok := range held {
					if strings.Contains(input, tok) {
						input = strings.Replace(input, tok, "", 1)
						delete(held, secret)
						break
					}
				}
			case 5:
				text := input
				if strings.TrimSpace(text) == "" {
					continue
				}
				input = ""
				fx := apply(Submit{At: t0, Text: text})
				ps := prompts(fx)
				if len(ps) != 1 {
					t.Fatalf("%d prompts", len(ps))
				}
				p := ps[0]
				got := map[string]bool{}
				for _, a := range p.Attachments {
					for secret := range held {
						if strings.Contains(a.Text, secret) {
							got[secret] = true
							sent[secret]++
						}
					}
					if !strings.HasPrefix(a.URI, "paste:") || !strings.Contains(p.Text, "["+a.URI+"]") {
						t.Fatalf("attachment %q not referenced in %q", a.URI, p.Text)
					}
				}
				for secret, tok := range held {
					inText := strings.Contains(text, tok)
					if inText != got[secret] {
						t.Fatalf("%s: placeholder in the input %v, attached %v", secret, inText, got[secret])
					}
					if strings.Contains(p.Text, secret) {
						t.Fatalf("%s is in the prompt text %q", secret, p.Text)
					}
					if inText {
						delete(held, secret)
					}
				}
				for _, secret := range trusted {
					if strings.Contains(text, secret) && !strings.Contains(p.Text, secret) {
						t.Fatalf("trusted %s lost", secret)
					}
				}
				apply(PromptDone{At: t0, Stop: EndTurn})
			}
		}
		for secret, c := range sent {
			if c != 1 {
				t.Fatalf("%s sent %d times", secret, c)
			}
		}
	})
}

// The trusted toggle is shown, applies to one paste, and says so in the
// transcript.
func TestTrustedPasteIsOneShot(t *testing.T) {
	s := idleState(true)
	s, _ = Reduce(s, ToggleTrustedPaste{At: t0})
	if l := Status(s, t0); !l.TrustNext {
		t.Fatal("the status does not show the armed toggle")
	}
	s, fx := Reduce(s, Pasted{At: t0, Text: "a\nb"})
	if len(fx) != 1 || fx[0] != (InsertInput{Text: "a\nb"}) {
		t.Fatalf("effects %+v", fx)
	}
	if n := s.Entries[len(s.Entries)-1]; !strings.Contains(n.Text, "2 lines as trusted text") {
		t.Fatalf("note %+v", n)
	}
	if s.TrustNext || len(s.Pastes) != 0 {
		t.Fatalf("still armed or held: %+v", s)
	}
	_, fx = Reduce(s, Pasted{At: t0, Text: "next"})
	if in, ok := fx[0].(InsertInput); !ok || !strings.HasPrefix(in.Text, "[pasted 1 line · untrusted #1]") {
		t.Fatalf("second paste %+v", fx)
	}
	// Toggling twice disarms.
	s, _ = Reduce(s, ToggleTrustedPaste{At: t0})
	s, _ = Reduce(s, ToggleTrustedPaste{At: t0})
	if s.TrustNext {
		t.Fatal("still armed")
	}
}

// Where the agent takes no embedded context, the text goes in the prompt
// and the transcript says it was not labelled.
func TestPasteInlinedWithoutEmbeddedContext(t *testing.T) {
	s := idleState(false)
	var tok string
	s, fx := Reduce(s, Pasted{At: t0, Text: "SECRET"})
	tok = fx[0].(InsertInput).Text
	s, fx = Reduce(s, Submit{At: t0, Text: "read " + tok})
	ps := prompts(fx)
	if len(ps) != 1 || ps[0].Text != "read SECRET" || len(ps[0].Attachments) != 0 {
		t.Fatalf("prompt %+v", ps)
	}
	var warned bool
	for _, e := range s.Entries {
		warned = warned || (e.Kind == Note && strings.Contains(e.Text, "not labelled untrusted"))
	}
	if !warned {
		t.Fatalf("no warning in %+v", s.Entries)
	}
}

// A paste in a message that was queued behind a running turn is held
// until that message goes out, and is not lost if the turn fails and the
// message goes back to the input.
func TestPasteSurvivesQueue(t *testing.T) {
	s := idleState(true)
	s, _ = Reduce(s, Submit{At: t0, Text: "first"})
	s, fx := Reduce(s, Pasted{At: t0, Text: "SECRET"})
	tok := fx[0].(InsertInput).Text
	s, _ = Reduce(s, Submit{At: t0, Text: "second " + tok})
	_, fx = Reduce(s, PromptDone{At: t0, Stop: EndTurn})
	ps := prompts(fx)
	if len(ps) != 1 || ps[0].Text != "second [paste:1]" || len(ps[0].Attachments) != 1 || ps[0].Attachments[0].Text != "SECRET" {
		t.Fatalf("prompt %+v", ps)
	}
	// Failure: the text returns to the input, and the paste is still held.
	s2, fx := Reduce(s, PromptFailed{At: t0, Message: "x"})
	if len(prompts(fx)) != 0 || len(s2.Pastes) != 1 {
		t.Fatalf("effects %+v pastes %+v", fx, s2.Pastes)
	}
}

func TestPasteIgnoredWhileAsking(t *testing.T) {
	s := idleState(true)
	s, _ = Reduce(s, Submit{At: t0, Text: "go"})
	s, _ = Reduce(s, PermissionAsked{At: t0, Permission: Permission{ID: 1}})
	s2, fx := Reduce(s, Pasted{At: t0, Text: "x"})
	if len(fx) != 0 || len(s2.Pastes) != 0 {
		t.Fatalf("a paste got through: %+v", fx)
	}
}
