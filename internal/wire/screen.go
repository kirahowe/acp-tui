package wire

import (
	"encoding/json"
	"encoding/json/jsontext"
	"errors"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/kirahowe/acp-tui/internal/strictjson"
)

// envelope mirrors the SDK's JSON-RPC message, which it decodes with
// encoding/json v1, with params and result typed as the SDK decodes them.
type envelope[P, R any] struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      *json.RawMessage  `json:"id,omitempty"`
	Method  string            `json:"method,omitempty"`
	Params  P                 `json:"params,omitempty"`
	Result  R                 `json:"result,omitempty"`
	Error   *acp.RequestError `json:"error,omitempty"`
}

// Screen is the one reader of what the agent sends. It takes one line
// and returns what the SDK may read, or an error when the line must not
// be acted on.
//
// A line must be one JSON object in the shared dialect (see
// internal/strictjson). The SDK decodes with encoding/json v1, which
// matches member names case-insensitively, so Screen also removes any
// member v1 would read under a name other than the one written, given the
// types the SDK decodes the message into. A line any JSON encoder wrote
// comes back unchanged.
func Screen(line []byte) ([]byte, error) {
	if err := strictjson.Message(line); err != nil {
		return nil, err
	}
	var m struct {
		Method *string        `json:"method"`
		Result jsontext.Value `json:"result"`
	}
	if err := strictjson.Unmarshal(line, &m); err != nil {
		// A method that is not a string: the SDK refuses it too.
		return line, nil
	}
	var targets []any
	switch {
	case m.Method != nil && *m.Method == acp.ClientMethodSessionUpdate:
		targets = []any{&envelope[*acp.SessionNotification, json.RawMessage]{}}
	case m.Method != nil && *m.Method == acp.ClientMethodSessionRequestPermission:
		targets = []any{&envelope[*acp.RequestPermissionRequest, json.RawMessage]{}}
	case m.Method == nil && m.Result != nil:
		// A response does not say which request it answers; read it as
		// each answer acp-tui waits for. An extension's answer is raw,
		// and wire reads it strictly itself.
		targets = []any{
			&envelope[json.RawMessage, *acp.InitializeResponse]{},
			&envelope[json.RawMessage, *acp.NewSessionResponse]{},
			&envelope[json.RawMessage, *acp.PromptResponse]{},
		}
	default:
		// Extension notifications carry raw params, which wire reads
		// strictly; anything else the SDK does not dispatch.
		targets = []any{&envelope[json.RawMessage, json.RawMessage]{}}
	}
	out := line
	for _, t := range targets {
		next, err := strictjson.ForV1(out, t)
		if errors.Is(err, strictjson.ErrUndecodable) {
			continue // the SDK cannot decode it as this either
		}
		if err != nil {
			return nil, err
		}
		out = next
	}
	return out, nil
}
