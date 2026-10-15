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
// person can paste one block as trusted text instead (ToggleTrustedPaste).
// Where the agent takes no embedded context, the text goes in the prompt
// and the transcript says it was not labelled.

// Paste is a block of pasted text held until its placeholder is sent.
type Paste struct {
	ID   int
	Text string
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
	return fmt.Sprintf("[pasted %d %s · untrusted #%d]", n, unit, p.ID)
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
	// ToggleTrustedPaste arms (or disarms) the next paste to go in as
	// plain, trusted text.
	ToggleTrustedPaste struct{ At time.Time }
)

func (e Pasted) at() time.Time             { return e.At }
func (e ToggleTrustedPaste) at() time.Time { return e.At }

// InsertInput asks the shell to insert text at the input's cursor.
type InsertInput struct{ Text string }

func (InsertInput) effect() {}

func (r *reducer) pasted(e Pasted) {
	s := &r.s
	if s.Phase == Exited || len(s.Asks) > 0 || e.Text == "" {
		return
	}
	if s.TrustNext {
		s.TrustNext = false
		r.emit(InsertInput{Text: e.Text})
		unit := "lines"
		if pasteLines(e.Text) == 1 {
			unit = "line"
		}
		r.add(Entry{Kind: Note, Text: fmt.Sprintf("pasted %d %s as trusted text, not labelled", pasteLines(e.Text), unit), Final: true})
		return
	}
	s.NextPaste++
	p := Paste{ID: s.NextPaste, Text: e.Text}
	s.Pastes = append(s.Pastes, p)
	r.emit(InsertInput{Text: p.Token()})
}

// resolve turns the text of a prompt into what is sent: the placeholders
// it still holds become references to attachments, or, where the agent
// takes no embedded context, the pasted text itself. Pastes whose
// placeholder was deleted are not sent. inlined counts the pastes that
// went in as plain text.
func (r *reducer) resolve(text string) (out string, atts []Attachment, inlined int) {
	s := &r.s
	var rest []Paste
	for _, p := range s.Pastes {
		tok := p.Token()
		if !strings.Contains(text, tok) {
			rest = append(rest, p)
			continue
		}
		if s.Embedded {
			text = strings.ReplaceAll(text, tok, p.Ref())
			atts = append(atts, Attachment{URI: p.URI(), Text: p.Text})
		} else {
			text = strings.ReplaceAll(text, tok, p.Text)
			inlined++
		}
	}
	s.Pastes = rest
	return text, atts, inlined
}
