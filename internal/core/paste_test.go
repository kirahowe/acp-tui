package core

import (
	"fmt"
	"strings"
	"testing"
	"time"

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
		var input string                  // the input box, as the shell keeps it
		var held = map[string]string{}    // secret -> token, for untrusted pastes still in the input
		var trusted = map[string]string{} // secret -> token, for pastes marked trusted still in the input
		sent := map[string]int{}          // secret -> times attached
		n := 0
		apply := func(e Event) []Effect {
			var fx []Effect
			s, fx = Reduce(s, e)
			for _, f := range fx {
				switch f := f.(type) {
				case InsertInput:
					input += f.Text
				case ReplaceInput:
					input = strings.Replace(input, f.Old, f.New, 1)
					for secret, tok := range held {
						if tok == f.Old {
							delete(held, secret)
							trusted[secret] = f.New
						}
					}
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
				before := input
				apply(Pasted{At: t0, Text: body})
				tok := strings.TrimPrefix(input, before)
				if !strings.HasPrefix(tok, "[pasted ") || strings.Contains(tok, secret) || !strings.Contains(tok, "untrusted") {
					t.Fatalf("the input got %q for a paste", tok)
				}
				held[secret] = tok
			case 2:
				apply(TrustPaste{At: t0, Draft: input})
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
				for secret, tok := range trusted {
					if strings.Contains(text, tok) {
						if !strings.Contains(p.Text, secret) {
							t.Fatalf("trusted %s lost", secret)
						}
						delete(trusted, secret)
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

// A paste is untrusted when it arrives, and ctrl+y marks the latest one
// still in the input as trusted, after the fact, on a placeholder in view.
func TestTrustPasteMarksTheLatestUntrustedPasteInTheInput(t *testing.T) {
	s := idleState(true)
	var input string
	apply := func(e Event) []Effect {
		var fx []Effect
		s, fx = Reduce(s, e)
		for _, f := range fx {
			switch f := f.(type) {
			case InsertInput:
				input += f.Text
			case ReplaceInput:
				input = strings.Replace(input, f.Old, f.New, 1)
			}
		}
		return fx
	}
	// Typing, then pasting: untrusted, attached.
	input = "see "
	apply(Pasted{At: t0, Text: "first\nemail"})
	if !strings.Contains(input, "[pasted 2 lines · untrusted #1]") {
		t.Fatalf("input %q", input)
	}
	// Ctrl+y trusts that one only.
	apply(Pasted{At: t0, Text: "second"})
	apply(TrustPaste{At: t0, Draft: input})
	if !strings.Contains(input, "untrusted #1]") || !strings.Contains(input, "[pasted 1 line · trusted #2]") {
		t.Fatalf("only the latest should be trusted: %q", input)
	}
	// Again: the earlier one. Again: none left, and it says so.
	apply(TrustPaste{At: t0, Draft: input})
	if strings.Contains(input, "untrusted") {
		t.Fatalf("%q", input)
	}
	before := input
	apply(TrustPaste{At: t0, Draft: input})
	if input != before || Status(s, t0).Message == "" || Status(s, t0.Add(time.Hour)).Message != "" {
		t.Fatalf("no-op: input %q, message %q", input, Status(s, t0).Message)
	}
	// A paste whose placeholder was deleted is not the latest in the input.
	s = idleState(true)
	input = ""
	apply(Pasted{At: t0, Text: "one"})
	apply(Pasted{At: t0, Text: "two"})
	input = strings.Replace(input, "[pasted 1 line · untrusted #2]", "", 1)
	apply(TrustPaste{At: t0, Draft: input})
	if !strings.Contains(input, "[pasted 1 line · trusted #1]") {
		t.Fatalf("%q", input)
	}
	// What goes out: the trusted one as plain text, the other as an attachment.
	s = idleState(true)
	input = ""
	apply(Pasted{At: t0, Text: "plain words"})
	apply(Pasted{At: t0, Text: "an email"})
	apply(TrustPaste{At: t0, Draft: input}) // trusts the email, the latest
	fx := apply(Submit{At: t0, Text: input})
	p := prompts(fx)[0]
	if !strings.Contains(p.Text, "an email") || len(p.Attachments) != 1 || p.Attachments[0].Text != "plain words" {
		t.Fatalf("%+v", p)
	}
	if n := s.Entries[len(s.Entries)-1]; !strings.Contains(n.Text, "went in as trusted text") && !strings.Contains(s.Entries[len(s.Entries)-2].Text, "went in as trusted text") {
		t.Fatalf("no note: %+v", s.Entries)
	}
	// A draft that still reads untrusted is attached, whatever was marked.
	s = idleState(true)
	apply(Pasted{At: t0, Text: "secret"})
	apply(TrustPaste{At: t0, Draft: "[pasted 1 line · untrusted #1]"})
	fx = apply(Submit{At: t0, Text: "[pasted 1 line · untrusted #1]"})
	if p := prompts(fx)[0]; len(p.Attachments) != 1 || strings.Contains(p.Text, "secret") {
		t.Fatalf("%+v", p)
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
