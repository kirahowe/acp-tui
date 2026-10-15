# acp-tui

acp-tui is a terminal chat client for agents that speak the
[Agent Client Protocol](https://agentclientprotocol.com). It starts the agent
as a child process, streams its replies into the terminal's own scrollback,
and keeps a status line that shows how long the agent has been working and
how long it has been silent.

Only the core exists so far. It has no command yet.

## How it is built

The state of a chat, and every rule for changing it, lives in
`internal/core`. `core.Reduce` takes the state and one event (a chunk of
reply, a tool call, a key press, a clock tick) and returns the new state
and a list of effects, such as "send this prompt" or "print these entries".
It reads no clock and does no IO, so property tests can check it over long
random sessions. `internal/wire` turns ACP messages into those events.

The terminal UI and the agent process are thin code around the core that
runs the effects.

## Building

Go 1.27 or later.

```sh
make check    # gofmt, vet, and every test with the race detector
make update   # move every dependency to its latest version, then check
```

The property tests run 100 cases each by default. For a longer run:

```sh
go test ./... -rapid.checks=5000
```

`testdata/agentloop` holds ACP streams recorded from agentloop. The tests
replay them; nothing imports agentloop.

## License

MIT
