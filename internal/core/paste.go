package core

import (
	"fmt"
	"strings"
	"time"
)

// A pasted block of text is the usual way untrusted text (an email, a web
// page) reaches the agent. So a paste is not typed into the prompt: it is
// held, the input shows a placeholder, and on send the prompt carries it
// as an embedded resource, which the agent labels contaminated. The
// person can then mark one block as trusted text (TrustPaste), after the
// fact and on a placeholder in view: a paste is always untrusted when it
// arrives, so nothing is armed ahead of it for a later paste to inherit.
// Where the agent takes no embedded context, the text goes in the prompt
// and the transcript says it was not labelled.

// Paste is a block of pasted text held until its placeholder is sent.
type Paste struct {
	ID   int
	Text string
	// Trusted says the person marked it trusted; its placeholder then
	// reads "trusted", and it is sent as plain text, not an attachment.
	Trusted bool
}

// Lines is how many lines the paste has.
func (p Paste) Lines() int { return pasteLines(p.Text) }

// Token is the placeholder the input shows for the paste. The id makes
// it unique, so deleting it from the input drops the paste.
func (p Paste) Token() string {
	n := p.Lines()
	unit := "lines"
	if n == 1 {
		unit = "line"
	}
	word := "untrusted"
	if p.Trusted {
		word = "trusted"
	}
	return fmt.Sprintf("[pasted %d %s · %s #%d]", n, unit, word, p.ID)
}

// untrustedToken is the placeholder the paste had when it arrived.
func (p Paste) untrustedToken() string {
	q := p
	q.Trusted = false
	return q.Token()
}

// Ref is how the prompt's text refers to the paste's resource.
func (p Paste) Ref() string { return fmt.Sprintf("[%s]", p.URI()) }

// URI is the paste's resource uri.
func (p Paste) URI() string { return fmt.Sprintf("paste:%d", p.ID) }

func pasteLines(text string) int {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\r\n")
	return strings.Count(text, "\n") + 1
}

// Attachment is a paste sent as an embedded text resource.
type Attachment struct{ URI, Text string }

// Events and effects for pastes.
type (
	// Pasted is a bracketed paste into the input.
	Pasted struct {
		At   time.Time
		Text string
	}
	// TrustPaste marks the latest paste whose untrusted placeholder is
	// still in the input as trusted. Draft is the input's text.
	TrustPaste struct {
		At    time.Time
		Draft string
	}
)

func (e Pasted) at() time.Time     { return e.At }
func (e TrustPaste) at() time.Time { return e.At }

// InsertInput asks the shell to insert text at the input's cursor.
type InsertInput struct{ Text string }

// ReplaceInput asks the shell to replace the first Old in the input with
// New, where it stands.
type ReplaceInput struct{ Old, New string }

func (InsertInput) effect()  {}
func (ReplaceInput) effect() {}

// Message is a short word for the status line that goes by itself.
type Message struct {
	Text string
	At   time.Time
}

// MessageFor is how long a Message shows.
const MessageFor = 6 * time.Second

func (r *reducer) pasted(e Pasted) {
	s := &r.s
	if !r.inputInPlay() || e.Text == "" {
		return // nothing pasted while a request waits or something else has the keys goes anywhere
	}
	s.NextPaste++
	p := Paste{ID: s.NextPaste, Text: e.Text}
	s.Pastes = append(s.Pastes, p)
	r.emit(InsertInput{Text: p.Token()})
}

// inputInPlay says the input box has the keys.
func (r *reducer) inputInPlay() bool {
	s := &r.s
	return !(s.Phase == Exited || len(s.Asks) > 0 || s.Pane.Focus || (s.Transcript.Open && !s.Shared))
}

// trustPaste marks the latest paste still in the draft as trusted and has
// the placeholder say so. With none to mark it says so on the status line.
func (r *reducer) trustPaste(e TrustPaste) {
	s := &r.s
	if !r.inputInPlay() {
		return
	}
	for i := len(s.Pastes) - 1; i >= 0; i-- {
		p := s.Pastes[i]
		if p.Trusted || !strings.Contains(e.Draft, p.Token()) {
			continue
		}
		old := p.Token()
		s.Pastes[i].Trusted = true
		r.emit(ReplaceInput{Old: old, New: s.Pastes[i].Token()})
		s.Message = Message{}
		return
	}
	s.Message = Message{Text: "no untrusted paste in the input to trust", At: e.At}
}

// resolve turns the text of a prompt into what is sent: the placeholders
// it still holds become references to attachments, or, where the agent
// takes no embedded context, the pasted text itself. Pastes whose
// placeholder was deleted are not sent. inlined counts the pastes that
// went in as plain text.
func (r *reducer) resolve(text string) (out string, atts []Attachment, inlined, trusted int) {
	s := &r.s
	var rest []Paste
	for _, p := range s.Pastes {
		// What the placeholder says decides, so a draft that still shows
		// "untrusted" is sent as an attachment whatever was marked.
		switch {
		case strings.Contains(text, p.untrustedToken()):
			tok := p.untrustedToken()
			if s.Embedded {
				text = strings.ReplaceAll(text, tok, p.Ref())
				atts = append(atts, Attachment{URI: p.URI(), Text: p.Text})
			} else {
				text = strings.ReplaceAll(text, tok, p.Text)
				inlined++
			}
		case p.Trusted && strings.Contains(text, p.Token()):
			text = strings.ReplaceAll(text, p.Token(), p.Text)
			trusted++
		default:
			rest = append(rest, p)
		}
	}
	s.Pastes = rest
	return text, atts, inlined, trusted
}
