These are what relay acp really said about a board, recorded on
2026-10-20 by relay's `scripts/card_fixtures.clj` against artificial
data. `internal/wire/relay_fixture_test.go` reads them through the wire
and core code and draws the board.

- `cards.json`: `_relay/cards` with a checklist, a tracker and a note
  written by a contaminated session (a link, a zero-width space and a
  fake approval line, all made safe by relay and marked outsider).
- `op_tick.json`, `op_unchanged.json`, `op_refused.json`:
  `_relay/cards/op` ticking an item, ticking it again, and naming an
  item that does not exist.
- `changed.json`: the `_relay/cards/changed` notification relay sent
  after another process set a tracker cell.

Each request file holds the request relay was sent and its response.

To refresh them, from a relay checkout:

    bb -cp src:resources:test scripts/card_fixtures.clj <acp-tui>/testdata/relay

Card ids and revs are log seqs, so they stay the same from run to run;
`addedAt` changes.
