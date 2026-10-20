package wire

import (
	"bufio"
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/kirahowe/acp-tui/internal/core"
	"github.com/kirahowe/acp-tui/internal/strictjson"
)

// Replay decodes a recorded stream, one JSON object per line: session
// updates, then the prompt's result ({"stopReason": ...} or {"error":
// ...}). It returns an event per line, all at time at, preceded by a
// LabelsSeen when the line carries labels. The fixtures in
// testdata/agentloop have this shape.
func Replay(jsonl []byte, at time.Time) ([]core.Event, error) {
	var events []core.Event
	sc := bufio.NewScanner(bytes.NewReader(jsonl))
	sc.Buffer(nil, 1<<20)
	for line := 1; sc.Scan(); line++ {
		es, err := replayLine(sc.Bytes(), at)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		events = append(events, es...)
	}
	return events, sc.Err()
}

func replayLine(b []byte, at time.Time) ([]core.Event, error) {
	// A recording is what another process wrote: read it as the stream is.
	if err := strictjson.Message(b); err != nil {
		return nil, err
	}
	var keys map[string]jsontext.Value
	if err := strictjson.Unmarshal(b, &keys); err != nil {
		return nil, err
	}
	switch {
	case keys["sessionUpdate"] != nil:
		var u acp.SessionUpdate
		if _, err := strictjson.ForV1(b, &u); err != nil {
			return nil, err
		}
		n := acp.SessionNotification{SessionId: "replay", Update: u}
		if l, ok := Labels(n, at); ok {
			return []core.Event{l, Update(n, at)}, nil
		}
		return []core.Event{Update(n, at)}, nil
	case keys["stopReason"] != nil:
		var r acp.PromptResponse
		if _, err := strictjson.ForV1(b, &r); err != nil {
			return nil, err
		}
		return []core.Event{Response(r, nil, at)}, nil
	case keys["error"] != nil:
		var re acp.RequestError
		if _, err := strictjson.ForV1(keys["error"], &re); err != nil {
			return nil, err
		}
		return []core.Event{Response(acp.PromptResponse{}, &re, at)}, nil
	}
	return nil, fmt.Errorf("not an update or a result: %s", b)
}
