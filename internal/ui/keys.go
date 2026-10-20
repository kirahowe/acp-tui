package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kirahowe/acp-tui/internal/core"
)

// Caps is what the terminal told us about its keyboard.
type Caps struct {
	// Disambiguation means the kitty keyboard protocol is on, so
	// shift+enter reaches us as itself.
	Disambiguation bool
}

// Action is what a key press does.
type Action int

const (
	PassThrough  Action = iota // to the input box
	Send                       // submit the input (the core queues it when busy)
	Newline                    // insert a newline into the input
	Interrupt                  // Ctrl-C
	EOF                        // Ctrl-D on an empty input
	ShowTools                  // Ctrl-T: the transcript, with tool calls in full
	ShowThinking               // Ctrl-O: the transcript, with thinking in full
	TrustPaste                 // Ctrl-Y: the next paste goes in as trusted text
	TogglePane                 // Ctrl-X: focus the workspace pane
	Help                       // ? on an empty input, or Ctrl-/: the list of keys
	Move                       // in the transcript: scroll, or leave
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
	case "ctrl+t":
		return ShowTools
	case "ctrl+o":
		return ShowThinking
	case "ctrl+y":
		return TrustPaste
	case "ctrl+x":
		return TogglePane
	case "ctrl+/", "ctrl+_": // a terminal without the kitty protocol sends ctrl+/ as ctrl+_
		return Help
	case "?":
		if inputEmpty {
			return Help
		}
	case "ctrl+d":
		if inputEmpty {
			return EOF
		}
	}
	return PassThrough
}

// ClassifyTranscript decides what a key press does in the transcript
// view: Move, with the move, or one of ShowTools, ShowThinking, Help,
// Interrupt and EOF. Every other key is swallowed (PassThrough), so
// nothing typed reaches the input box. It is pure.
func ClassifyTranscript(k tea.KeyPressMsg, inputEmpty bool) (Action, core.TranscriptAction) {
	switch k.String() {
	case "up", "k":
		return Move, core.TranscriptUp
	case "down", "j":
		return Move, core.TranscriptDown
	case "pgup", "b":
		return Move, core.TranscriptPageUp
	case "pgdown", "space", "f":
		return Move, core.TranscriptPageDown
	case "home", "g":
		return Move, core.TranscriptTop
	case "end", "G":
		return Move, core.TranscriptBottom
	case "esc", "q":
		return Move, core.TranscriptClose
	case "ctrl+t", "t":
		return ShowTools, 0
	case "ctrl+o", "o":
		return ShowThinking, 0
	case "?", "ctrl+/", "ctrl+_":
		return Help, 0
	case "ctrl+c":
		return Interrupt, 0
	case "ctrl+d":
		if inputEmpty {
			return EOF, 0
		}
	}
	return PassThrough, 0
}

// closesHelp says a key only closes the list of keys; any other key
// closes it and then does what it does.
func closesHelp(k tea.KeyPressMsg) bool {
	switch k.String() {
	case "esc", "?", "ctrl+/", "ctrl+_":
		return true
	}
	return false
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

// ClassifyPane decides what a key press does while the workspace pane has
// focus. Every key is swallowed, so nothing typed reaches the input box. It
// is pure.
func ClassifyPane(k tea.KeyPressMsg) core.PaneAction {
	switch k.String() {
	case "up", "k", "shift+tab":
		return core.PaneUp
	case "down", "j", "tab":
		return core.PaneDown
	case "pgup":
		return core.PagePrev
	case "pgdown", "space":
		return core.PageNext
	case "enter":
		return core.PaneEnter
	case "esc":
		return core.PaneEsc
	case "ctrl+x":
		return core.PaneToggle
	case "t":
		return core.PaneTrust
	case "y", "Y":
		return core.PaneYes
	case "b":
		return core.PaneBoard
	}
	return core.PaneOther
}

// ClassifyBoard decides what a key press does while the pane shows the
// board. Every key is swallowed. It is pure.
func ClassifyBoard(k tea.KeyPressMsg) core.PaneAction {
	switch k.String() {
	case "up", "k", "shift+tab":
		return core.PaneUp
	case "down", "j", "tab":
		return core.PaneDown
	case "left", "h":
		return core.PaneLeft
	case "right", "l":
		return core.PaneRight
	case "pgup":
		return core.PagePrev
	case "pgdown":
		return core.PageNext
	case "enter":
		return core.PaneEnter
	case "esc":
		return core.PaneEsc
	case "ctrl+x":
		return core.PaneToggle
	case "b":
		return core.PaneBoard
	case "r":
		return core.PaneRefresh
	case "space", "x":
		return core.PaneMark
	case "a":
		return core.PaneAdd
	case "A":
		return core.PaneAddColumn
	case "e":
		return core.PaneEdit
	case "E":
		return core.PaneEditColumn
	case "X":
		return core.PaneArchive
	case "y", "Y":
		return core.PaneYes
	}
	return core.PaneOther
}

// ClassifyBoardInput decides what a key press does while the board's
// one-line input is open: enter sends, esc cancels, backspace deletes,
// and a key that types text types it. It is pure.
func ClassifyBoardInput(k tea.KeyPressMsg) (core.PaneAction, string) {
	switch k.String() {
	case "enter":
		return core.PaneEnter, ""
	case "esc":
		return core.PaneEsc, ""
	case "backspace", "ctrl+h":
		return core.PaneBackspace, ""
	case "ctrl+x":
		return core.PaneToggle, ""
	}
	if k.Text != "" && k.Mod&^tea.ModShift == 0 {
		return core.PaneChar, k.Text
	}
	return core.PaneOther, ""
}
