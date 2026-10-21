// Command fakehub is a stand-in for relay's hub, for acp-tui's tests:
//
//	fakehub [-log FILE] [-control FILE] CONFIG
//
// See package fakehub for the config, the control file and the log.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	acp "github.com/caelis-labs/acp-go-sdk"
	"github.com/caelis-labs/acp-go-sdk/transport/stdio"

	"github.com/kirahowe/acp-tui/internal/fakehub"
)

func main() {
	logPath := flag.String("log", "", "append what the client does to this file")
	control := flag.String("control", "", "read commands from this file as they are appended")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: fakehub [-log FILE] [-control FILE] CONFIG")
		os.Exit(2)
	}
	if err := run(flag.Arg(0), *logPath, *control); err != nil {
		fmt.Fprintln(os.Stderr, "fakehub:", err)
		os.Exit(1)
	}
}

func run(configPath, logPath, control string) error {
	cfg, err := fakehub.Load(configPath)
	if err != nil {
		return err
	}
	var log io.Writer = io.Discard
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer func() {
			fmt.Fprintln(f, "exit")
			f.Close()
		}()
		log = f
	}
	h := fakehub.New(cfg, log)
	conn, err := stdio.NewAgentConnection(h, acp.ConnectionOptions{})
	if err != nil {
		return err
	}
	defer conn.Close()
	return fakehub.Serve(context.Background(), h, conn, control)
}
