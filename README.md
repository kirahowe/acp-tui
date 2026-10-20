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
another share one line that names the tools, and the agent's thinking
shows as one faint line, `∴ thought`.

Enter sends. Ctrl-J starts a new line in any terminal; Shift-Enter,
Ctrl-Enter and Alt-Enter do too where the terminal reports them. Pressing
Enter while the agent is working queues the text, and everything queued
goes out as one message when the turn ends. Ctrl-C cancels the turn and
puts any queued text back in the input. With nothing running and an empty
input, Ctrl-C or Ctrl-D quits. `?` on an empty input, or Ctrl-/, lists
the keys; any key closes the list.

Ctrl-T and Ctrl-O open the transcript: the whole session on the
terminal's alternate screen, with tool calls (Ctrl-T) or thinking
(Ctrl-O) in full. A call in full shows its arguments and the first lines
of its output. In the transcript, Ctrl-T or `t` shows or hides tool calls
and Ctrl-O or `o` thinking. Up and down, `j` and `k`, the page keys, `g`
and `G`, and the mouse wheel scroll it, and Esc or `q` goes back. It
works while the agent is busy too. The chat prints into the terminal's
own scrollback, which cannot be redrawn, so the transcript is where
blocks open and close. Nothing goes into scrollback while it is open:
what the agent writes meanwhile is printed when you leave. A permission
request closes the transcript, so the request is always on screen, and
the session's labels lead its footer as they lead the status line.

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
video, with its URLs defanged. A preview's URLs are always defanged,
since a preview is what a service said. relay labels a preview's text
apart from the session (`relay/labels` on the preview block), and a
request or proposal is shown by both sets of labels. Arguments the
operator fixed in relay's settings are listed under "set by your
settings". A link to a domain that no approved or trusted item has shown
so far in this run carries `[new domain]`.

Keys, once Ctrl-X has given the pane focus:

- Up and down, or k and j, select an item. Enter opens it; up and down,
  Page Up and Page Down scroll.
- t starts a trust of the selected item.
- b switches to the board (see below), and back.
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

## The board

relay keeps cards: small structured things you work through over days,
such as a checklist or a tracker of accounts by month. Roles make them.
Press b in the focused pane to see them.
The first time, acp-tui asks relay for the board with `_relay/cards`.
After that relay sends `_relay/cards/changed` whenever a card changes,
here, in a terminal or by a role, and the board redraws from what relay
sent. An archived card leaves the board. Against
an agent without relay the board says "This agent has no board."

The list shows each card's title, kind, progress and labels. Enter opens
a card. A checklist shows its items with `[x]` or `[ ]`. A tracker shows
its columns, then one row per line, with `x` for done, `-` for skipped
and `·` for open. A note shows its text. A card of a kind acp-tui does not
know shows the text form relay sends with every card.

Keys on the board:

- Up and down, or k and j, select a card, or, in an open card, its title
  and then each item or row. Left and right, or h and l, select a
  tracker's column. When a change arrives, the cursor stays on the item,
  row or column it was on. If that one is gone, the cursor stays where it
  was and a note says so.
- x or space ticks or unticks the selected item. On a tracker it moves
  the selected cell from open to done to skipped and back to open.
- In an open card, a adds an item to a checklist or a row to a tracker,
  and A adds a column.
- e edits the selected line's text: the title, an item or a row's name.
  In the list it retitles the selected card. In an open tracker, E
  renames the selected column. A note's text cannot be edited here.
- X archives the selected card, after you confirm with y. relay's CLI can
  restore it.
- r asks relay for the board again; b goes back to the workspace items. An agent with
  no board is not asked again.

Adding and editing use a one-line input at the foot of the pane: Enter
sends, Esc cancels. An edit starts from the current text, unless that text
came from outside. What you send counts as your own text, so acp-tui
does not offer outside words for you to send back unchanged. Every edit carries the
revision you were looking at when you opened it, so relay refuses it if
someone changed that text in the meantime. A paste into the input goes
nowhere, as anywhere else in the pane.

acp-tui never changes a card itself. Each key sends relay an op with
`_relay/cards/op` and waits for the card relay sends back. Until then the
card is marked `sending…`, and a key for another op is dropped with a
note saying so. When relay refuses an op, the pane shows "Refused." with
relay's reason, until relay takes your next op or you close the input.
If the op carried text, the input opens again with what you typed. After a refused edit it carries the card's current revision,
so Enter sends your text over the newer change; Esc drops it. Leaving
the pane with ctrl+x keeps an open input as it was.

Card text is shown as relay sent it. relay has already made hidden
characters visible and defanged links in text from outside, which it
marks as such, and acp-tui puts that text under the `untrusted` mark.
Links are never clickable, and acp-tui fetches nothing.

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

To paste one block as trusted text, press Ctrl-Y first. The status line
shows `[next paste: trusted]`, the next paste goes into the input as plain
text, the transcript notes that it was not labelled, and the toggle
resets. Ctrl-Y again disarms it. A paste made while a permission request
waits, the pane has focus or the transcript is open goes nowhere, and it
uses up an armed trust: the next paste is untrusted again.

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

The pane follows the same split. `core` holds its state, the trust
rules and the board's cards, `wire` reads relay's `_meta`, its
`_relay/items`, `_relay/resolve`, `_relay/trust`, `_relay/cards` and
`_relay/cards/op` answers and its `_relay/cards/changed` notifications,
and `render` draws it all. The `agent` package makes the calls one at a
time and in order.

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
