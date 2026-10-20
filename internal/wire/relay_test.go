package wire

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	acp "github.com/caelis-labs/acp-go-sdk"
	"pgregory.net/rapid"

	"github.com/kirahowe/acp-tui/internal/core"
)

const displayUpdate = `{"sessionUpdate":"tool_call_update","toolCallId":"t","status":"completed","content":[
 {"type":"content","content":{"type":"text","text":"plain result"}},
 {"type":"content","content":{"type":"text","text":"Title\n\nbody","_meta":{"relay/display":{"title":"Title","body":"body https://a.test","labels":["contaminated","private","odd"],
   "sources":[{"title":"S","ref":"web:https://a.test"}],"ref":"shown:2026-10-18/t"}}}}]}`

func TestDisplayMetaIsParsedAndKeptOutOfOutput(t *testing.T) {
	u := Update(decode(t, displayUpdate), t0).(core.ToolUpdated)
	want := []core.Display{{Title: "Title", Body: "body https://a.test", Labels: core.Labels{Private: true, Contaminated: true},
		Sources: []core.Source{{Title: "S", Ref: "web:https://a.test"}}, Ref: "shown:2026-10-18/t"}}
	if !reflect.DeepEqual(u.Displays, want) {
		t.Errorf("displays %#v", u.Displays)
	}
	if u.Output == nil || *u.Output != "plain result" {
		t.Errorf("output %v: a display block leaked into the tool output", u.Output)
	}
	// The same on a call that arrives finished.
	started := Update(decode(t, `{"sessionUpdate":"tool_call","toolCallId":"t","title":"x","status":"completed","content":[
	 {"type":"content","content":{"type":"text","text":"out"}},
	 {"type":"content","content":{"type":"text","text":"shown","_meta":{"relay/display":{"title":"T"}}}}]}`), t0).(core.ToolStarted)
	if started.Output != "out" || len(started.Displays) != 1 || started.Displays[0].Title != "T" {
		t.Errorf("started %#v", started)
	}
}

func TestUnreadableDisplayFailsClosed(t *testing.T) {
	u := Update(decode(t, `{"sessionUpdate":"tool_call_update","toolCallId":"t","content":[
	 {"type":"content","content":{"type":"text","text":"raw words","_meta":{"relay/display":"not an object"}}}]}`), t0).(core.ToolUpdated)
	if len(u.Displays) != 1 || !u.Displays[0].Labels.Contaminated || !u.Displays[0].Labels.Private || u.Displays[0].Body != "raw words" {
		t.Errorf("displays %#v", u.Displays)
	}
	if *u.Output != "" {
		t.Errorf("output %q", *u.Output)
	}
}

// However the blocks are mixed, none that carries a display or preview
// marker reaches a tool's output.
func TestMarkedBlocksNeverReachOutput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var content []acp.ToolCallContent
		var plain []string
		for range rapid.IntRange(0, 6).Draw(t, "blocks") {
			text := rapid.StringMatching(`[a-z ]{1,12}`).Draw(t, "text")
			block := &acp.ContentBlockText{Type: "text", Text: text}
			switch rapid.IntRange(0, 2).Draw(t, "marker") {
			case 0:
				plain = append(plain, text)
			case 1:
				block.Meta = map[string]json.RawMessage{"relay/display": json.RawMessage(rapid.SampledFrom([]string{`{}`, `7`, `null`, `"x"`}).Draw(t, "v"))}
			case 2:
				block.Meta = map[string]json.RawMessage{"relay/preview": json.RawMessage(`true`)}
			}
			content = append(content, acp.ToolCallContent{Content: &acp.ToolCallContentContent{Content: acp.ContentBlock{Text: block}}})
		}
		got := contentText(content)
		want := ""
		for i, p := range plain {
			if i > 0 {
				want += "\n"
			}
			want += p
		}
		if got != want {
			t.Fatalf("output %q, want %q", got, want)
		}
	})
}

func TestPermissionPreviewAndLabels(t *testing.T) {
	var req acp.RequestPermissionRequest
	if err := json.Unmarshal([]byte(`{"sessionId":"s","toolCall":{"toolCallId":"c1","name":"send","rawInput":{"g":1},
		"content":[{"type":"content","content":{"type":"text","text":"Sends X","_meta":{"relay/preview":true}}},
		{"type":"content","content":{"type":"text","text":"model words"}},
		{"type":"content","content":{"type":"text","text":"not a preview","_meta":{"relay/preview":false}}}],
		"_meta":{"agentloop/labels":["private"]}},
		"options":[{"optionId":"a","name":"Allow","kind":"allow_once"}]}`), &req); err != nil {
		t.Fatal(err)
	}
	p := Permission(req, "", 3, t0).Permission
	if p.Preview != "Sends X" || p.CallID != "c1" || p.Labels != (core.Labels{Private: true}) {
		t.Errorf("permission %#v", p)
	}
	if l, ok := PermissionLabels(req, t0); !ok || !l.Labels.Private {
		t.Errorf("labels %#v %v", l, ok)
	}
}

func TestItems(t *testing.T) {
	raw := json.RawMessage(`{"items":[
	 {"kind":"proposal","callId":"c","tool":"send","input":{"g":"Kira only"},"preview":null,"labels":["private"],"status":"waiting","at":"2026-10-18T20:02:00Z"},
	 {"kind":"shown","ref":"shown:d/x","path":"shown/d/x.md","callId":"x","title":"T","text":"body","sources":[{"title":"S","ref":"web:https://a.test"}],
	  "labels":["contaminated"],"version":412,"sha256":"ab","canTrust":true,"trustCommand":"relay trust doc shown/d/x.md"},
	 {"kind":"future","title":"unknown kind"}]}`)
	got := Items(raw, nil, t0)
	if got.Failed || got.Unavailable || len(got.Items) != 2 {
		t.Fatalf("got %#v", got)
	}
	p, sh := got.Items[0], got.Items[1]
	if p.Kind != core.ProposalItem || p.Tool != "send" || p.Input != `{"g":"Kira only"}` || p.Preview != "" || p.Status != "waiting" || !p.Labels.Private {
		t.Errorf("proposal %#v", p)
	}
	if sh.Kind != core.ShownItem || sh.Version != "412" || sh.SHA256 != "ab" || !sh.CanTrust || sh.Path != "shown/d/x.md" ||
		!sh.Labels.Contaminated || sh.Sources[0].Ref != "web:https://a.test" || sh.TrustCommand == "" {
		t.Errorf("shown %#v", sh)
	}
}

func TestItemsErrors(t *testing.T) {
	if got := Items(nil, acp.NewMethodNotFound("_relay/items"), t0); !got.Unavailable || got.Failed {
		t.Errorf("method not found: %#v", got)
	}
	if got := Items(nil, errors.New("boom"), t0); got.Unavailable || !got.Failed {
		t.Errorf("other error: %#v", got)
	}
	if got := Items(json.RawMessage(`[1]`), nil, t0); !got.Failed {
		t.Errorf("malformed: %#v", got)
	}
	// A version that is not a number is not echoed back to relay.
	got := Items(json.RawMessage(`{"items":[{"kind":"shown","version":"7; drop"}]}`), nil, t0)
	if got.Items[0].Version != "" {
		t.Errorf("version %q", got.Items[0].Version)
	}
}

func TestResolution(t *testing.T) {
	ok := Resolution("[[fact:1]]", json.RawMessage(`{"ref":"[[fact:1]]","kind":"fact","status":"ok","title":"Fact 1","text":"t","labels":["private"]}`), nil, t0)
	if ok.Failed || ok.Cite.Title != "Fact 1" || ok.Cite.Status != "ok" || !ok.Cite.Labels.Private {
		t.Errorf("ok %#v", ok)
	}
	ext := Resolution("[[web:u]]", json.RawMessage(`{"kind":"web","status":"external","target":"https://a.test"}`), nil, t0)
	if ext.Cite.Target != "https://a.test" || ext.Cite.Status != "external" {
		t.Errorf("external %#v", ext)
	}
	for name, r := range map[string]core.CitationResolved{
		"error":      Resolution("[[x]]", nil, errors.New("e"), t0),
		"null kind":  Resolution("[[x]]", json.RawMessage(`{"kind":null,"status":"ok"}`), nil, t0),
		"odd status": Resolution("[[x]]", json.RawMessage(`{"kind":"fact","status":"fine"}`), nil, t0),
		"garbage":    Resolution("[[x]]", json.RawMessage(`7`), nil, t0),
	} {
		if !r.Failed {
			t.Errorf("%s: not failed", name)
		}
	}
}

func TestTrustOutcome(t *testing.T) {
	if d := TrustOutcome("p", json.RawMessage(`{"trusted":true,"labels":["private"]}`), nil, t0); d.Err != "" || !d.Labels.Private {
		t.Errorf("ok %#v", d)
	}
	if d := TrustOutcome("p", json.RawMessage(`{"trusted":false}`), nil, t0); d.Err == "" {
		t.Errorf("a refusal reads as success: %#v", d)
	}
	err := &acp.RequestError{Code: -32000, Message: "the doc changed", Data: map[string]any{"message": "version 413 now"}}
	if d := TrustOutcome("p", nil, err, t0); d.Err != "the doc changed: version 413 now" || d.Path != "p" {
		t.Errorf("error %#v", d)
	}
}

func TestTrustParams(t *testing.T) {
	b, err := json.Marshal(TrustParams("s", "shown/x.md", "412", "ab"))
	if err != nil || string(b) != `{"path":"shown/x.md","sessionId":"s","sha256":"ab","version":412}` {
		t.Errorf("%s %v", b, err)
	}
}

// The preview's labels and the fixed arguments are read apart from the
// session's labels, which they do not change.
func TestPermissionPreviewLabelsAndFixedArgs(t *testing.T) {
	var req acp.RequestPermissionRequest
	if err := json.Unmarshal([]byte(`{"sessionId":"s","toolCall":{"toolCallId":"c1","name":"send","rawInput":{"group":"Kira only"},
		"content":[{"type":"content","content":{"type":"text","text":"Sends X","_meta":{"relay/preview":true,"relay/labels":["contaminated","private"]}}}],
		"_meta":{"agentloop/labels":["private"],"agentloop/fixed_args":{"send_now":false,"group":"Kira only"}}},
		"options":[{"optionId":"a","name":"Allow","kind":"allow_once"}]}`), &req); err != nil {
		t.Fatal(err)
	}
	p := Permission(req, "", 3, t0).Permission
	if p.PreviewLabels != (core.Labels{Private: true, Contaminated: true}) || p.Labels != (core.Labels{Private: true}) {
		t.Errorf("labels %#v / %#v", p.Labels, p.PreviewLabels)
	}
	if strings.Join(p.FixedArgs, ",") != "group,send_now" {
		t.Errorf("fixed %v", p.FixedArgs)
	}
	if l, _ := PermissionLabels(req, t0); l.Labels.Contaminated {
		t.Error("the preview's labels reached the session's")
	}
	got := Items(json.RawMessage(`{"items":[{"kind":"proposal","callId":"c","tool":"send","input":{},"labels":["contaminated"],"fixedArgs":["group"],"status":"waiting"}]}`), nil, t0)
	if len(got.Items) != 1 || strings.Join(got.Items[0].FixedArgs, ",") != "group" {
		t.Errorf("items %#v", got)
	}
}
