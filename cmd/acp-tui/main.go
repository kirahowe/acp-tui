// Command acp-tui is a terminal chat client for an ACP agent:
//
//	acp-tui [--title TITLE] [--open ROLE] [--agent-log FILE] -- COMMAND [ARGS...]
//
// It starts COMMAND as the agent, in the current directory. Exit status:
// 0 after a normal quit, 1 when the agent fails to start or exits on its
// own, 2 for a usage error.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/kirahowe/acp-tui/internal/agent"
	"github.com/kirahowe/acp-tui/internal/core"
	"github.com/kirahowe/acp-tui/internal/ui"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("acp-tui", flag.ContinueOnError)
	logPath := fs.String("agent-log", "", "also write the agent's stderr to `FILE`")
	title := fs.String("title", "", "name the session `TITLE` in the banner (default: the agent's title or name)")
	open := fs.String("open", "", "with relay's hub, focus the role `ROLE` at the start (open or attach to its conversation)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: acp-tui [--title TITLE] [--open ROLE] [--agent-log FILE] -- COMMAND [ARGS...]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	command := fs.Args()
	if len(command) == 0 {
		fs.Usage()
		return 2
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "acp-tui:", err)
		return 1
	}

	// The agent's stderr would garble the screen, so keep its tail to
	// show after the TUI exits, and copy it all to --agent-log.
	tail := &ring{max: 64 << 10}
	var stderr io.Writer = tail
	if *logPath != "" {
		f, err := os.Create(*logPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "acp-tui:", err)
			return 1
		}
		defer f.Close()
		stderr = io.MultiWriter(tail, f)
	}

	var p *tea.Program
	started := make(chan struct{})
	conn, err := agent.Start(command, dir, stderr, func(e core.Event) {
		<-started
		p.Send(ui.EventMsg{Event: e})
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "acp-tui: starting the agent:", err)
		return 1
	}
	p = tea.NewProgram(ui.New(conn, *title).Open(*open))
	close(started)
	final, err := p.Run()
	if err != nil {
		conn.Shutdown()
		fmt.Fprintln(os.Stderr, "acp-tui:", err)
		return 1
	}
	if died, why := final.(ui.Model).Died(); died {
		fmt.Fprintln(os.Stderr, "acp-tui: the agent exited:", why)
		os.Stderr.Write(tail.bytes())
		return 1
	}
	if known, keeps := final.(ui.Model).Keeps(); known {
		if keeps {
			fmt.Fprintln(os.Stderr, "acp-tui: conversations keep running in the relay service")
		} else {
			fmt.Fprintln(os.Stderr, "acp-tui: relay service not running: the conversations ended")
		}
	}
	return 0
}

// ring keeps the last max bytes written to it.
type ring struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (r *ring) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, b...)
	if over := len(r.buf) - r.max; over > 0 {
		r.buf = append([]byte(nil), r.buf[over:]...)
	}
	return len(b), nil
}

func (r *ring) bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.buf...)
}
