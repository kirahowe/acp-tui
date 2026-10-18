# acp-tui

acp-tui is a terminal chat client for agents that speak the
[Agent Client Protocol](https://agentclientprotocol.com). It starts the agent
as a child process, streams its replies into the terminal's own scrollback,
and keeps a status line that shows how long the agent has been working and
how long it has been silent.

## Using it

```sh
make build
bin/acp-tui -- agentloop acp request.json
```

Everything after `--` is the agent's command. It runs in the current
directory, which is also the session's working directory.

When the session starts, acp-tui prints a banner with the agent's name and
version, and the model if the agent names one in its session options. The
banner's title is `--title` if given, otherwise the title the agent reports
about itself, otherwise its name. The input sits in a box at the bottom,
with a status line under it showing what the agent is doing, for how long,
and how much of its context window is in use and what the session has
cost when the agent reports those. A message you send stays in its box,
so your turns stand apart from the agent's. Tool calls made one after
another share one line that names the tools.

Enter sends. Ctrl-J starts a new line in any terminal; Shift-Enter,
Ctrl-Enter and Alt-Enter do too where the terminal reports them. Pressing
Enter while the agent is working queues the text, and everything queued
goes out as one message when the turn ends. Ctrl-C cancels the turn and
puts any queued text back in the input. With nothing running and an empty
input, Ctrl-C or Ctrl-D quits. Ctrl-O, once the turn is over, prints the
latest run of tool calls in full: each call's arguments and the start of
its output.

The agent's stderr would garble the screen, so acp-tui keeps the last
64 KiB and prints them if the agent exits on its own. `--agent-log FILE`
writes all of it to a file.

When the agent asks permission for a tool call, the request replaces the
input box. It shows the tool's name and its input, indented and cut to ten
lines, and the options the agent offered. Keys: `y` picks the first
allow_once option, `n` the first reject_once, up/down and enter pick between
them (the highlight starts on a reject, so a bare enter refuses). Options
of any other kind, such as allow_always, are not offered: acp-tui never
grants standing permission. `v`
puts the whole input into the transcript, and Esc or Ctrl-C answers
cancelled. Keys that would approve do nothing for the first 600 ms, so a
`y` typed for the input cannot allow a call that appeared mid-word. Other
keys are ignored while a request waits, so no prompt can go out. Requests
queue and are answered in order; ending the turn cancels the rest.

Nothing the agent wrote in a request is dropped. Control characters, bidi
and other format characters, and any other code point that does not print
show as `\uXXXX` escapes, so the input reads back as JSON to exactly what
will run. Wrapping adds line breaks; nothing is cut except in the
collapsed box, where `v` shows the rest.

If the agent reports session labels in `_meta["agentloop/labels"]` on tool
calls, usage updates and session_info_update, the status line shows them: `[private]` in bold
and ` contaminated ` in reverse video. Labels only accumulate during a
session.

While the session is contaminated, URLs in what the agent writes (replies,
notices, tool names, arguments and output) show defanged, as
`hxxps://host[.]tld/path`, so a click cannot carry data out. acp-tui never
writes OSC 8 hyperlinks. The request box shows its input exactly, but
with its URLs defanged once the session is contaminated: a click on one,
to look at it, would send what it carries before you approve anything.
A paste while a request waits is dropped. acp-tui never sends
`resource_link` blocks.

## The workspace pane

Agents that run under relay put work in front of you as they go: what a
tool found, the calls waiting for your approval, and the documents the
latest reply cites. The workspace pane lists it. On a screen 120 columns
or wider it stands to the right of the chat. On a narrower one, Ctrl-X
opens it over the chat and the status line shows `ctrl+x workspace (3)`
with the item count.

Newest items come first. A shown item has a title, its labels and its
sources. A proposal is a call held for approval, with relay's preview of
what it will do and its status: waiting, allowed, rejected or cancelled.
Below the items, each reference in the latest reply, written `[[fact:12]]`
or `[[doc:notes/venues.md]]`, shows the title relay gives it. A reference
relay cannot find shows as `missing` in reverse video. A reference not yet
checked, or checked against an agent that cannot answer, shows as
`unchecked`. The pane never shows words from the model as a title: the
model's text is only searched for references.

Text from a contaminated item sits under an `untrusted` mark in reverse
video, with its URLs defanged. A link to a domain that no approved or
trusted item has shown so far in this run carries `[new domain]`.

Keys, once Ctrl-X has given the pane focus:

- Up and down, or k and j, select an item. Enter opens it; up and down,
  Page Up and Page Down scroll.
- t starts a trust of the selected item.
- Esc goes back from an item, then returns to the input. Ctrl-X does the
  same from anywhere.

While the pane has focus no key reaches the input box. Ctrl-C still stops
a running turn. A permission request takes the keys back from the pane,
and you answer it as before: the pane only highlights the waiting
proposal and shows its preview, here and in the request box. It adds no
second way to approve.

Trusting needs relay chat, which lets acp-tui ask relay to do it. Press t
on a contaminated shown item. The pane shows the text that would be
trusted, with its path, version and hash, and says "Trust this? It clears
contaminated from what it says now." After a short delay, y confirms;
any other key cancels. acp-tui then sends relay the version and hash it
listed, so a document that changed in between is refused. Where relay does
not take trust from this client, the pane shows the `relay trust` command
to run in a terminal and does nothing else. Against an agent without relay
the pane lists the displays and requests acp-tui saw itself, and offers no
trust.

## Pasting marks the conversation untrusted

A pasted email or web page is text an outsider may have written, and
pasting it is the usual way that text reaches the agent. So acp-tui does
not type a paste into the prompt. A bracketed paste is held, and the input
shows a placeholder such as `[pasted 42 lines · untrusted #1]`, even for a
single line: a short paste can carry an injected instruction as well as a
long one. When you send, the prompt is the text you typed, with each
placeholder replaced by a reference like `[paste:1]`, plus one embedded
`resource` block per paste (text/plain, uri `paste:1`). agentloop labels a
session `contaminated` when it receives a resource block, which is what
makes egress need your approval from then on. Delete a placeholder from
the input and that paste is not sent. A terminal that does not do
bracketed paste types the text in as keys, and acp-tui cannot tell.

To paste one block as trusted text, press Ctrl-T first. The status line
shows `[next paste: trusted]`, the next paste goes into the input as plain
text, the transcript notes that it was not labelled, and the toggle
resets. Ctrl-T again disarms it.

If the agent does not advertise `promptCapabilities.embeddedContext`, a
paste cannot be labelled. It goes in the prompt as plain text and the
transcript warns that it was not labelled untrusted.

`go test` also drives a real agentloop binary against a fake Messages API on
localhost, when one exists at `../agentloop/bin/agentloop` (or the path in
`-agentloop`); without it those tests skip.

## How it is built

The state of a chat, and every rule for changing it, lives in
`internal/core`. `core.Reduce` takes the state and one event (a chunk of
reply, a tool call, a key press, a clock tick) and returns the new state
and a list of effects, such as "send this prompt" or "print these entries".
It reads no clock and does no IO, so property tests can check it over long
random sessions. `internal/wire` turns ACP messages into those events, and
`internal/render` turns the state into terminal lines. The renderer uses
bold, italic and faint, and no colours.

The pane follows the same split. `core` holds its state and the trust
rules, `wire` reads relay's `_meta` and its `_relay/items`, `_relay/resolve`
and `_relay/trust` answers, and `render` draws it. The `agent` package
makes the three calls, one at a time and in order.

`internal/agent` runs the agent process, and `internal/ui` is the Bubble
Tea program. Both are thin code around the core that carries out its
effects.

## Building

Go 1.27 or later.

```sh
make build    # bin/acp-tui
make check    # gofmt, vet, and every test with the race detector
make update   # move every dependency to its latest version, then check
```

The property tests run 100 cases each by default. For a longer run:

```sh
go test ./... -rapid.checks=5000
```

`testdata/agentloop` holds ACP streams recorded from agentloop. The tests
replay them; nothing imports agentloop. The end-to-end tests run acp-tui
against `cmd/fakeagent`, which plays the scripts in `testdata/scripts`,
once in process on an emulated terminal and once as a built binary on a
real pseudo-terminal.

## License

MIT
