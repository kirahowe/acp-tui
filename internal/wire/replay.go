package wire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/kirahowe/acp-tui/internal/core"
)

// Replay decodes a recorded stream, one JSON object per line: session
// updates, then the prompt's result ({"stopReason": ...} or {"error":
// ...}). It returns one event per line, all at time at. The fixtures in
// testdata/agentloop have this shape.
func Replay(jsonl []byte, at time.Time) ([]core.Event, error) {
	var events []core.Event
	sc := bufio.NewScanner(bytes.NewReader(jsonl))
	sc.Buffer(nil, 1<<20)
	for line := 1; sc.Scan(); line++ {
		e, err := replayLine(sc.Bytes(), at)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		events = append(events, e)
	}
	return events, sc.Err()
}

func replayLine(b []byte, at time.Time) (core.Event, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return nil, err
	}
	switch {
	case keys["sessionUpdate"] != nil:
		var u acp.SessionUpdate
		if err := u.UnmarshalJSON(b); err != nil {
			return nil, err
		}
		return Update(acp.SessionNotification{SessionId: "replay", Update: u}, at), nil
	case keys["stopReason"] != nil:
		var r acp.PromptResponse
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		return Response(r, nil, at), nil
	case keys["error"] != nil:
		var re acp.RequestError
		if err := json.Unmarshal(keys["error"], &re); err != nil {
			return nil, err
		}
		return Response(acp.PromptResponse{}, &re, at), nil
	}
	return nil, fmt.Errorf("not an update or a result: %s", b)
}
