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
//
// The SDK reads every line first as its own envelope, with params and
// result raw, and dispatches on the method and id it reads there. Screen
// therefore always prepares the line for that envelope first, and drops a
// line the SDK could not read as one, so that a member such as "Method"
// is gone even when the typed params that follow cannot be decoded.
func Screen(line []byte) ([]byte, error) {
	if err := strictjson.Message(line); err != nil {
		return nil, err
	}
	// The SDK's anyMessage, which also takes "id" by its exact name; a
	// folded "ID" is removed here, so the two lookups agree.
	out, err := strictjson.ForV1(line, &envelope[json.RawMessage, json.RawMessage]{})
	if err != nil {
		// Undecodable included: the SDK cannot read it either.
		return nil, err
	}
	var m struct {
		Method *string        `json:"method"`
		Result jsontext.Value `json:"result"`
	}
	if err := strictjson.Unmarshal(out, &m); err != nil {
		return nil, err
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
	}
	// Extension notifications carry raw params, which wire reads strictly,
	// so the envelope pass above is all they need.
	for _, t := range targets {
		next, err := strictjson.ForV1(out, t)
		if errors.Is(err, strictjson.ErrUndecodable) {
			// The SDK fails on the params or result as this type, after
			// it has dispatched on the envelope screened above.
			continue
		}
		if err != nil {
			return nil, err
		}
		out = next
	}
	return out, nil
}
