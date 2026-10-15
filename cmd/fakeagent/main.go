// Command fakeagent is a scripted ACP agent for acp-tui's tests:
//
//	fakeagent [-log FILE] [-title TITLE] [-model MODEL] SCRIPT
//
// See package fakeagent for the script format.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	acp "github.com/caelis-labs/acp-go-sdk"
	"github.com/caelis-labs/acp-go-sdk/transport/stdio"

	"github.com/kirahowe/acp-tui/internal/fakeagent"
)

func main() {
	logPath := flag.String("log", "", "append each prompt and cancel to this file")
	var info fakeagent.Info
	flag.StringVar(&info.Title, "title", "", "the title in agentInfo")
	flag.StringVar(&info.Model, "model", "", "offer this model as a session config option")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: fakeagent [-log FILE] [-title TITLE] [-model MODEL] SCRIPT")
		os.Exit(2)
	}
	if err := run(flag.Arg(0), *logPath, info); err != nil {
		fmt.Fprintln(os.Stderr, "fakeagent:", err)
		os.Exit(1)
	}
}

func run(scriptPath, logPath string, info fakeagent.Info) error {
	script, err := os.ReadFile(scriptPath)
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
	a, err := fakeagent.New(script, log, info)
	if err != nil {
		return err
	}
	conn, err := stdio.NewAgentConnection(a, acp.ConnectionOptions{})
	if err != nil {
		return err
	}
	defer conn.Close()
	return fakeagent.Serve(context.Background(), a, conn)
}
