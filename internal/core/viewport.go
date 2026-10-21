package core

// MinTranscriptRows is how many rows of conversation a hub window must
// still show, beyond the role's name, the request and the status line,
// for a request to be answerable. After v the request's whole input is in
// the conversation, and these rows are what it is read through, by
// scrolling.
const MinTranscriptRows = 3

// RequestFits is the one rule for whether a window shows a permission
// request well enough to answer it: rows is the window's height, fixed is
// the rows everything but the conversation takes (the name, the request's
// box, the status line), and spare is how many rows of conversation must
// be left over. Render decides TooSmall by it, and the core turns the keys
// that approve off by what render said.
func RequestFits(rows, fixed, spare int) bool { return rows-fixed >= spare }
