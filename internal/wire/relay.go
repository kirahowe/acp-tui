package wire

import (
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"strconv"
	"strings"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"

	"github.com/kirahowe/acp-tui/internal/core"
	"github.com/kirahowe/acp-tui/internal/strictjson"
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
			Title   string      `json:"title"`
			Body    string      `json:"body"`
			Labels  []string    `json:"labels"`
			Sources []rawSource `json:"sources"`
			Ref     string      `json:"ref"`
		}
		if err := strictjson.Unmarshal(raw, &m); err != nil {
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

// PreviewLabels reads the labels relay gave its preview, under
// _meta["relay/labels"] on the preview's text block. They say what the
// preview's text carries, not what the session holds.
func PreviewLabels(content []acp.ToolCallContent) core.Labels {
	var l core.Labels
	for _, c := range content {
		if c.Content == nil || c.Content.Content.Text == nil {
			continue
		}
		t := c.Content.Content.Text
		if string(t.Meta[previewKey]) != "true" {
			continue
		}
		var names []string
		if strictjson.Unmarshal(t.Meta[previewLabelsKey], &names) == nil {
			l = l.Union(labelNames(names))
		}
	}
	return l
}

// previewLabelsKey is where relay puts the labels of a preview's text.
const previewLabelsKey = "relay/labels"

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
	if b, e := json.Marshal(re.Data); e == nil && strictjson.Unmarshal(b, &data) == nil &&
		data.Message != "" && !strings.Contains(msg, data.Message) {
		msg += ": " + data.Message
	}
	return msg
}

type rawSource struct {
	Title string `json:"title"`
	Ref   string `json:"ref"`
}

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
	FixedArgs    []string        `json:"fixedArgs"`
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
	if strictjson.Unmarshal(raw, &r) != nil {
		return core.ItemsLoaded{At: at, Failed: true}
	}
	e := core.ItemsLoaded{At: at}
	for _, it := range r.Items {
		out := core.Item{Ref: it.Ref, Path: it.Path, CallID: it.CallID, At: it.At, Title: it.Title, Text: it.Text,
			Labels: labelNames(it.Labels), SHA256: it.SHA256, CanTrust: it.CanTrust, TrustCommand: it.TrustCommand,
			Tool: it.Tool, Status: it.Status}
		// The version stays the literal relay listed, to send back exactly.
		if v := jsontext.Value(it.Version); v.Kind() == '0' {
			if _, err := strconv.ParseInt(string(v), 10, 64); err == nil {
				out.Version = string(v)
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
			out.FixedArgs = it.FixedArgs
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
	if strictjson.Unmarshal(raw, &r) != nil || r.Kind == nil {
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
	if strictjson.Unmarshal(raw, &r) != nil || !r.Trusted {
		return core.TrustDone{At: at, Path: path, Err: "relay did not say it trusted the item"}
	}
	return core.TrustDone{At: at, Path: path, Labels: labelNames(r.Labels)}
}

// The board: relay's cards, read with _relay/cards, changed with
// _relay/cards/op, and pushed with _relay/cards/changed once the client
// has read them. relay makes every text safe before it sends it.
const (
	MethodCards        = "_relay/cards"
	MethodCardOp       = "_relay/cards/op"
	MethodCardsChanged = "_relay/cards/changed"
)

// CardsParams is the parameters of _relay/cards: the active cards only.
func CardsParams(session acp.SessionId) map[string]any {
	return map[string]any{"sessionId": session}
}

// CardOpParams is the parameters of _relay/cards/op: the card and the
// op's fields, leaving out the ones not set.
func CardOpParams(session acp.SessionId, card int64, op core.CardOp) map[string]any {
	m := map[string]any{"sessionId": session, "card": card, "op": op.Op}
	if op.Item != nil {
		m["item"] = *op.Item
	}
	if op.Row != nil {
		m["row"] = *op.Row
	}
	if op.Column != nil {
		m["column"] = *op.Column
	}
	if op.State != nil {
		m["state"] = *op.State
	}
	if op.Text != nil {
		m["text"] = *op.Text
	}
	if op.Seen != nil {
		m["seen"] = *op.Seen
	}
	return m
}

// rawText is a piece of a card's text. A missing outsider flag reads as
// true: text relay did not vouch for is shown as outside text.
type rawText struct {
	ID       *int   `json:"id"`
	Text     string `json:"text"`
	Outsider *bool  `json:"outsider"`
	Rev      int64  `json:"rev"`
	Done     bool   `json:"done"`
}

func (t rawText) text() core.CardText {
	return core.CardText{Text: t.Text, Outsider: t.Outsider == nil || *t.Outsider, Rev: t.Rev}
}

type rawCard struct {
	ID       *int64   `json:"id"`
	Kind     string   `json:"kind"`
	Status   string   `json:"status"`
	Complete bool     `json:"complete"`
	Rev      int64    `json:"rev"`
	Owner    string   `json:"owner"`
	AddedAt  string   `json:"addedAt"`
	Title    *rawText `json:"title"`
	Progress *struct {
		Done  int `json:"done"`
		Total int `json:"total"`
	} `json:"progress"`
	Labels  []string  `json:"labels"`
	Items   []rawText `json:"items"`
	Rows    []rawText `json:"rows"`
	Columns []rawText `json:"columns"`
	Cells   []struct {
		Row    int    `json:"row"`
		Column int    `json:"column"`
		State  string `json:"state"`
	} `json:"cells"`
	Note *rawText `json:"note"`
	Text string   `json:"text"`
}

func elems(raw []rawText) ([]core.CardElem, bool) {
	var out []core.CardElem
	for _, t := range raw {
		if t.ID == nil {
			return nil, false
		}
		out = append(out, core.CardElem{ID: *t.ID, CardText: t.text(), Done: t.Done})
	}
	return out, true
}

// card reads one card in relay's wire form. A card without an id, or a
// native kind whose elements cannot be read, is left out; a card of a
// kind this client does not know keeps its text form.
func card(raw json.RawMessage) (core.Card, bool) {
	var r rawCard
	if strictjson.Unmarshal(raw, &r) != nil || r.ID == nil {
		return core.Card{}, false
	}
	c := core.Card{ID: *r.ID, Kind: r.Kind, Status: r.Status, Complete: r.Complete, Rev: r.Rev, Owner: r.Owner,
		AddedAt: r.AddedAt, Labels: labelNames(r.Labels), Text: r.Text}
	if c.Status == "" {
		c.Status = core.CardActive
	}
	if r.Title != nil {
		c.Title = r.Title.text()
	}
	if r.Progress != nil {
		c.Done, c.Total = r.Progress.Done, r.Progress.Total
	}
	var ok bool
	switch c.Kind {
	case core.CardChecklist:
		if c.Items, ok = elems(r.Items); !ok {
			c.Kind = "checklist?" // shows its text form
		}
	case core.CardTracker:
		rows, ok1 := elems(r.Rows)
		cols, ok2 := elems(r.Columns)
		if !ok1 || !ok2 {
			c.Kind = "tracker?"
			break
		}
		c.Rows, c.Columns = rows, cols
		for _, x := range r.Cells {
			if x.State != "" && x.State != core.CellOpen {
				c.Cells = append(c.Cells, core.Cell{Row: x.Row, Column: x.Column, State: x.State})
			}
		}
	case core.CardNote:
		if r.Note == nil {
			c.Kind = "note?"
			break
		}
		n := r.Note.text()
		c.Note = &n
	}
	return c, true
}

func cardList(raw []json.RawMessage) []core.Card {
	var out []core.Card
	for _, x := range raw {
		if c, ok := card(x); ok {
			out = append(out, c)
		}
	}
	return out
}

// Cards translates the answer to _relay/cards. A plain agent answers
// method-not-found: it has no board.
func Cards(raw json.RawMessage, err error, at time.Time) core.CardsLoaded {
	if err != nil {
		if notFound(err) {
			return core.CardsLoaded{At: at, Unavailable: true}
		}
		return core.CardsLoaded{At: at, Err: errText(err)}
	}
	var r struct {
		Rev   int64             `json:"rev"`
		Cards []json.RawMessage `json:"cards"`
	}
	if strictjson.Unmarshal(raw, &r) != nil || r.Cards == nil {
		return core.CardsLoaded{At: at, Err: "relay's answer could not be read"}
	}
	return core.CardsLoaded{At: at, Rev: r.Rev, Cards: cardList(r.Cards)}
}

// CardOpOutcome translates the answer to _relay/cards/op on card id. A
// JSON-RPC error's message is relay's refusal, and "changed": false says
// the op changed nothing.
func CardOpOutcome(id int64, raw json.RawMessage, err error, at time.Time) core.CardOpDone {
	if err != nil {
		return core.CardOpDone{At: at, CardID: id, Err: errText(err)}
	}
	var r struct {
		Card    json.RawMessage `json:"card"`
		Changed *bool           `json:"changed"`
	}
	if strictjson.Unmarshal(raw, &r) != nil || r.Card == nil {
		return core.CardOpDone{At: at, CardID: id, Err: "relay's answer could not be read"}
	}
	c, ok := card(r.Card)
	if !ok || c.ID != id {
		return core.CardOpDone{At: at, CardID: id, Err: "relay's answer could not be read"}
	}
	// An answer without the field is taken as a change.
	return core.CardOpDone{At: at, CardID: id, Card: &c, Unchanged: r.Changed != nil && !*r.Changed}
}

// CardsChanged translates a _relay/cards/changed notification. ok is
// false when it cannot be read.
func CardsChanged(params json.RawMessage, at time.Time) (core.CardsChanged, bool) {
	var r struct {
		Rev   int64             `json:"rev"`
		Cards []json.RawMessage `json:"cards"`
	}
	if strictjson.Unmarshal(params, &r) != nil {
		return core.CardsChanged{}, false
	}
	return core.CardsChanged{At: at, Rev: r.Rev, Cards: cardList(r.Cards)}, true
}
