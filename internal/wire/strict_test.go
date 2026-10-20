package wire

import (
	"encoding/json"
	"testing"

	acp "github.com/caelis-labs/acp-go-sdk"
)

// What wire reads itself, from relay's answers and _meta, it reads by the
// dialect: names exactly, no duplicates, numbers as written.
func TestWireReadsStrictly(t *testing.T) {
	// A duplicate anywhere fails the whole answer.
	if e := Items(json.RawMessage(`{"items":[{"kind":"shown","ref":"a","ref":"b"}]}`), nil, t0); !e.Failed {
		t.Errorf("duplicate name read: %+v", e)
	}
	// Names match exactly; v1 would have read these.
	if e := Items(json.RawMessage(`{"Items":[{"kind":"shown","ref":"a"}]}`), nil, t0); e.Failed || len(e.Items) != 0 {
		t.Errorf("Items read as items: %+v", e)
	}
	if e, ok := CardsChanged(json.RawMessage(`{"Rev":7,"cards":[]}`), t0); !ok || e.Rev != 0 {
		t.Errorf("Rev read as rev: %+v", e)
	}
	if _, ok := CardsChanged(json.RawMessage(`{"rev":7,"cards":[]} {}`), t0); ok {
		t.Error("a second value read")
	}
	// A proposal's input and an item's version keep their literals.
	e := Items(json.RawMessage(`{"items":[{"kind":"proposal","ref":"p","tool":"send","version":9007199254740993,
		"input":{"id":9007199254740993,"x":1.10}}]}`), nil, t0)
	if len(e.Items) != 1 || e.Items[0].Input != `{"id":9007199254740993,"x":1.10}` || e.Items[0].Version != "9007199254740993" {
		t.Errorf("numbers changed: %+v", e.Items)
	}
	// Labels in _meta, a display's fields and sources: exact names.
	if _, ok := labelsOf(map[string]json.RawMessage{labelsKey: json.RawMessage(`["private","private"]`)}); !ok {
		t.Error("a list may repeat a value")
	}
	ds := Displays(textWithMeta(`{"relay/display":{"Title":"T","title":"t","sources":[{"title":"s","Ref":"r"}]}}`))
	if len(ds) != 1 || ds[0].Title != "t" || len(ds[0].Sources) != 1 || ds[0].Sources[0].Ref != "" {
		t.Errorf("display read loosely: %+v", ds)
	}
}

// textWithMeta is tool call content of one text block with that _meta.
func textWithMeta(meta string) []acp.ToolCallContent {
	var c []acp.ToolCallContent
	if err := json.Unmarshal([]byte(`[{"type":"content","content":{"type":"text","text":"x","_meta":`+meta+`}}]`), &c); err != nil {
		panic(err)
	}
	return c
}
