package wire

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/kirahowe/acp-tui/internal/core"
)

// What relay adds to ACP, in _meta and in extension methods. See relay's
// wire contract for the display channel.
const (
	displayKey = "relay/display"
	previewKey = "relay/preview"

	MethodItems   = "_relay/items"
	MethodResolve = "_relay/resolve"
	MethodTrust   = "_relay/trust"

	methodNotFound = -32601
)

// isRelayBlock says whether a text block is relay's display or preview,
// which are for the operator and never part of a tool's output.
func isRelayBlock(t *acp.ContentBlockText) bool {
	_, display := t.Meta[displayKey]
	_, preview := t.Meta[previewKey]
	return display || preview
}

// Displays reads the displays in a tool call's content: text blocks with
// _meta["relay/display"]. A display that cannot be read still shows, with
// the block's own text, marked private and contaminated: it fails closed.
func Displays(content []acp.ToolCallContent) []core.Display {
	var out []core.Display
	for _, c := range content {
		if c.Content == nil || c.Content.Content.Text == nil {
			continue
		}
		t := c.Content.Content.Text
		raw, ok := t.Meta[displayKey]
		if !ok {
			continue
		}
		var m struct {
			Title   string `json:"title"`
			Body    string `json:"body"`
			Labels  []string
			Sources []struct{ Title, Ref string }
			Ref     string `json:"ref"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			out = append(out, core.Display{Title: "(unreadable display)", Body: t.Text,
				Labels: core.Labels{Private: true, Contaminated: true}})
			continue
		}
		d := core.Display{Title: m.Title, Body: m.Body, Labels: labelNames(m.Labels), Ref: m.Ref}
		for _, src := range m.Sources {
			d.Sources = append(d.Sources, core.Source{Title: src.Title, Ref: src.Ref})
		}
		out = append(out, d)
	}
	return out
}

// Preview reads relay's preview of a request: the text blocks of the
// tool call's content with _meta["relay/preview"] true, joined.
func Preview(content []acp.ToolCallContent) string {
	var parts []string
	for _, c := range content {
		if c.Content == nil || c.Content.Content.Text == nil {
			continue
		}
		t := c.Content.Content.Text
		if string(t.Meta[previewKey]) == "true" {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ItemsParams, ResolveParams and TrustParams are the parameters of relay's
// extension methods.
func ItemsParams(session acp.SessionId) map[string]any {
	return map[string]any{"sessionId": session}
}

func ResolveParams(session acp.SessionId, ref string) map[string]any {
	return map[string]any{"sessionId": session, "ref": ref}
}

// TrustParams carries the version as the JSON number relay listed it with.
func TrustParams(session acp.SessionId, path, version, sha string) map[string]any {
	return map[string]any{"sessionId": session, "path": path, "version": json.Number(version), "sha256": sha}
}

// notFound says whether err is the agent saying it has no such method.
func notFound(err error) bool {
	var re *acp.RequestError
	return errors.As(err, &re) && re.Code == methodNotFound
}

// errText is an error's message for the operator, with the agent's own
// data message when it adds something, as Response does.
func errText(err error) string {
	var re *acp.RequestError
	if !errors.As(err, &re) {
		return err.Error()
	}
	msg := re.Message
	var data struct {
		Message string `json:"message"`
	}
	if b, e := json.Marshal(re.Data); e == nil && json.Unmarshal(b, &data) == nil &&
		data.Message != "" && !strings.Contains(msg, data.Message) {
		msg += ": " + data.Message
	}
	return msg
}

type rawSource struct{ Title, Ref string }

type rawItem struct {
	Kind         string          `json:"kind"`
	Ref          string          `json:"ref"`
	Path         string          `json:"path"`
	CallID       string          `json:"callId"`
	At           string          `json:"at"`
	Title        string          `json:"title"`
	Text         string          `json:"text"`
	Sources      []rawSource     `json:"sources"`
	Labels       []string        `json:"labels"`
	Version      json.RawMessage `json:"version"`
	SHA256       string          `json:"sha256"`
	CanTrust     bool            `json:"canTrust"`
	TrustCommand string          `json:"trustCommand"`
	Tool         string          `json:"tool"`
	Input        json.RawMessage `json:"input"`
	Preview      *string         `json:"preview"`
	Status       string          `json:"status"`
}

// Items translates the answer to _relay/items. A plain agent answers
// method-not-found, which says relay is not there.
func Items(raw json.RawMessage, err error, at time.Time) core.ItemsLoaded {
	if err != nil {
		return core.ItemsLoaded{At: at, Unavailable: notFound(err), Failed: !notFound(err)}
	}
	var r struct {
		Items []rawItem `json:"items"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return core.ItemsLoaded{At: at, Failed: true}
	}
	e := core.ItemsLoaded{At: at}
	for _, it := range r.Items {
		out := core.Item{Ref: it.Ref, Path: it.Path, CallID: it.CallID, At: it.At, Title: it.Title, Text: it.Text,
			Labels: labelNames(it.Labels), SHA256: it.SHA256, CanTrust: it.CanTrust, TrustCommand: it.TrustCommand,
			Tool: it.Tool, Status: it.Status}
		var n json.Number
		if json.Unmarshal(it.Version, &n) == nil {
			if _, err := n.Int64(); err == nil {
				out.Version = n.String()
			}
		}
		for _, src := range it.Sources {
			out.Sources = append(out.Sources, core.Source{Title: src.Title, Ref: src.Ref})
		}
		switch it.Kind {
		case "shown":
			out.Kind = core.ShownItem
		case "proposal":
			out.Kind = core.ProposalItem
			out.Input = rawJSON(it.Input)
			if it.Input != nil && out.Input == "" && string(it.Input) != "null" {
				out.Input = string(it.Input)
			}
			if it.Preview != nil {
				out.Preview = *it.Preview
			}
		default:
			continue // a kind this client does not know
		}
		e.Items = append(e.Items, out)
	}
	return e
}

// Resolution translates the answer to _relay/resolve for ref. Anything
// but a clear answer leaves the citation unchecked.
func Resolution(ref string, raw json.RawMessage, err error, at time.Time) core.CitationResolved {
	failed := core.CitationResolved{At: at, Cite: core.Citation{Ref: ref}, Failed: true}
	if err != nil {
		return failed
	}
	var r struct {
		Kind   *string  `json:"kind"`
		Status string   `json:"status"`
		Title  string   `json:"title"`
		Text   string   `json:"text"`
		Target string   `json:"target"`
		Labels []string `json:"labels"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Kind == nil {
		return failed
	}
	switch r.Status {
	case core.CiteOK, core.CiteMissing, core.CiteExternal:
	default:
		return failed
	}
	return core.CitationResolved{At: at, Cite: core.Citation{Ref: ref, Kind: *r.Kind, Status: r.Status,
		Title: r.Title, Text: r.Text, Target: r.Target, Labels: labelNames(r.Labels)}}
}

// TrustOutcome translates the answer to _relay/trust for path.
func TrustOutcome(path string, raw json.RawMessage, err error, at time.Time) core.TrustDone {
	if err != nil {
		return core.TrustDone{At: at, Path: path, Err: errText(err)}
	}
	var r struct {
		Trusted bool     `json:"trusted"`
		Labels  []string `json:"labels"`
	}
	if json.Unmarshal(raw, &r) != nil || !r.Trusted {
		return core.TrustDone{At: at, Path: path, Err: "relay did not say it trusted the item"}
	}
	return core.TrustDone{At: at, Path: path, Labels: labelNames(r.Labels)}
}
