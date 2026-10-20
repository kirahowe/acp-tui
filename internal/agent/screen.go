package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/kirahowe/acp-tui/internal/wire"
)

// maxLine is the longest line the SDK takes by default (10 MiB). A longer
// one ends the connection, as it does in the SDK.
const maxLine = 10 << 20

// screened is the agent's stdout as the SDK reads it: each line through
// wire.Screen. A line that fails is dropped, counted and logged, and the
// SDK never sees it. Blank lines are dropped quietly, as the SDK would.
type screened struct {
	sc      *bufio.Scanner
	pending []byte
	log     io.Writer
	dropped atomic.Int64
}

func newScreened(r io.Reader, log io.Writer) *screened {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	return &screened{sc: sc, log: log}
}

func (s *screened) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		if !s.sc.Scan() {
			if err := s.sc.Err(); err != nil {
				return 0, err // "token too long" is what the SDK looks for
			}
			return 0, io.EOF
		}
		line := s.sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		out, err := wire.Screen(line)
		if err != nil {
			n := s.dropped.Add(1)
			fmt.Fprintf(s.log, "acp-tui: dropped a line from the agent that is not strict JSON (%d so far): %v\n", n, err)
			continue
		}
		s.pending = append(append(s.pending[:0:0], out...), '\n')
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

// lockedWriter lets the agent's stderr and acp-tui's own notes about the
// agent share one writer.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}
