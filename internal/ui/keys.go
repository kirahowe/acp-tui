package ui

import tea "charm.land/bubbletea/v2"

// Caps is what the terminal told us about its keyboard.
type Caps struct {
	// Disambiguation means the kitty keyboard protocol is on, so
	// shift+enter reaches us as itself.
	Disambiguation bool
}

// Action is what a key press does.
type Action int

const (
	PassThrough Action = iota // to the input box
	Send                      // submit the input (the core queues it when busy)
	Newline                   // insert a newline into the input
	Interrupt                 // Ctrl-C
	EOF                       // Ctrl-D on an empty input
	ShowTools                 // Ctrl-O: the latest tool calls in full
)

// Classify decides what a key press does. It is pure.
//
// Enter always sends. Four keys insert a newline, so one of them works in
// any terminal: shift+enter and ctrl+enter need the kitty protocol or
// xterm's modifyOtherKeys (Bubble Tea asks for both), alt+enter works in
// most terminals, and ctrl+j (a bare line feed) works everywhere.
func Classify(k tea.KeyPressMsg, inputEmpty bool) Action {
	switch k.String() {
	case "enter":
		return Send
	case "shift+enter", "ctrl+enter", "alt+enter", "ctrl+j":
		return Newline
	case "ctrl+c":
		return Interrupt
	case "ctrl+o":
		return ShowTools
	case "ctrl+d":
		if inputEmpty {
			return EOF
		}
	}
	return PassThrough
}

// NewlineHint tells the user how to type a new line.
func NewlineHint(c Caps) string {
	if c.Disambiguation {
		return "shift+enter for a new line"
	}
	return "ctrl+j for a new line"
}

// AskAction is what a key press does while a permission request waits.
type AskAction int

const (
	AskNone      AskAction = iota // swallowed: the input box is not in play
	AskUp                         // highlight the previous option
	AskDown                       // highlight the next option
	AskConfirm                    // answer with the highlighted option
	AskAllow                      // answer with the first allow_once
	AskReject                     // answer with the first reject_once
	AskCancel                     // answer cancelled
	AskShowAll                    // put the whole input in the transcript
	AskInterrupt                  // Ctrl-C: stop the turn, which cancels the request
	AskEOF                        // Ctrl-D
)

// ClassifyAsk decides what a key press does while a request waits. Every
// key is swallowed but these, so nothing typed reaches the input box and
// no Enter can send a prompt. It is pure.
func ClassifyAsk(k tea.KeyPressMsg) AskAction {
	switch k.String() {
	case "up", "k", "shift+tab":
		return AskUp
	case "down", "j", "tab":
		return AskDown
	case "enter":
		return AskConfirm
	case "y", "Y":
		return AskAllow
	case "n", "N":
		return AskReject
	case "esc":
		return AskCancel
	case "v":
		return AskShowAll
	case "ctrl+c":
		return AskInterrupt
	case "ctrl+d":
		return AskEOF
	}
	return AskNone
}
