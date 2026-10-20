package strictjson

import (
	"bytes"
	jsonv1 "encoding/json"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/kirahowe/acp-tui/internal/jsoncases"
)

func TestSharedTable(t *testing.T) {
	for _, c := range jsoncases.Read(t) {
		t.Run(c.Name, func(t *testing.T) {
			err := Message(c.Line)
			if c.Reject {
				if err == nil {
					t.Fatal("accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			var m struct {
				Method *string `json:"method"`
			}
			if err := Unmarshal(c.Line, &m); err != nil {
				t.Fatal(err)
			}
			if !eqPtr(m.Method, c.Method) {
				t.Fatalf("method = %v, want %v", show(m.Method), show(c.Method))
			}
		})
	}
}

func eqPtr(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func show(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

func TestValidLimits(t *testing.T) {
	deep := func(n int) []byte { return []byte(strings.Repeat("[", n) + strings.Repeat("]", n)) }
	if err := Valid(deep(MaxDepth)); err != nil {
		t.Errorf("%d levels: %v", MaxDepth, err)
	}
	if Valid(deep(MaxDepth+1)) == nil {
		t.Errorf("%d levels accepted", MaxDepth+1)
	}
	num := func(n int) []byte { return []byte(strings.Repeat("9", n)) }
	if err := Valid(num(MaxNumber)); err != nil {
		t.Errorf("%d digits: %v", MaxNumber, err)
	}
	if Valid(num(MaxNumber+1)) == nil {
		t.Errorf("%d digits accepted", MaxNumber+1)
	}
	if Valid([]byte("-0.5e-7")) != nil || Valid([]byte(`"x"`)) != nil {
		t.Error("a scalar at the top is a value")
	}
	if Message([]byte(`"x"`)) == nil || Message([]byte(`[]`)) == nil {
		t.Error("a message must be an object")
	}
	for _, s := range []string{"", " ", "{} {}", "{}x", "1 2"} {
		if Valid([]byte(s)) == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

type envelope struct {
	JSONRPC string             `json:"jsonrpc"`
	ID      *jsonv1.RawMessage `json:"id,omitempty"`
	Method  string             `json:"method,omitempty"`
	Params  *params            `json:"params,omitempty"`
}

type params struct {
	SessionID string                       `json:"sessionId"`
	Title     string                       `json:"title,omitempty"`
	Meta      map[string]jsonv1.RawMessage `json:"_meta,omitempty"`
	Any       any                          `json:"rawInput,omitempty"`
}

func TestForV1(t *testing.T) {
	cases := []struct{ in, out string }{
		// What a JSON encoder writes is passed on as it is.
		{`{"jsonrpc":"2.0","method":"m","params":{"sessionId":"s","_meta":{"Title":1,"title":2}}}`,
			`{"jsonrpc":"2.0","method":"m","params":{"sessionId":"s","_meta":{"Title":1,"title":2}}}`},
		{` {"jsonrpc" : "2.0", "unknown": {"Method": 1}} `, ` {"jsonrpc" : "2.0", "unknown": {"Method": 1}} `},
		// Map keys and any values are read exactly by v1 too.
		{`{"jsonrpc":"2.0","params":{"sessionId":"s","rawInput":{"ID":1,"id":2,"n":12345678901234567890123}}}`,
			`{"jsonrpc":"2.0","params":{"sessionId":"s","rawInput":{"ID":1,"id":2,"n":12345678901234567890123}}}`},
		// A member v1 would fold is removed.
		{`{"jsonrpc":"2.0","Method":"_relay/x","params":{}}`, `{"jsonrpc":"2.0","params":{}}`},
		{`{"jsonrpc":"2.0","method":"m","Method":"_relay/x"}`, `{"jsonrpc":"2.0","method":"m"}`},
		{`{"jsonrpc":"2.0","Method":"_relay/x","method":"m"}`, `{"jsonrpc":"2.0","method":"m"}`},
		{`{"jsonrpc":"2.0","method":"m","paramſ":{"sessionId":"x"}}`, `{"jsonrpc":"2.0","method":"m"}`},
		{`{"jsonrpc":"2.0","method":"m","params":{"sessionId":"s","TITLE":"t","n":1.50}}`,
			`{"jsonrpc":"2.0","method":"m","params":{"sessionId":"s","n":1.50}}`},
		{`{"jsonrpc":"2.0","method":"m","params":{"sessionId":"s","_META":{"relay/display":{}}}}`,
			`{"jsonrpc":"2.0","method":"m","params":{"sessionId":"s"}}`},
		{`{"jsonrpc":"2.0","ID":7,"method":"m"}`, `{"jsonrpc":"2.0","method":"m"}`},
		{`{"jsonrpc":"2.0","method":"m","params":{"sessionId":"s","s":"a \"b"},"JSONRPC":"1"}`,
			`{"jsonrpc":"2.0","method":"m","params":{"sessionId":"s","s":"a \"b"}}`},
	}
	for _, c := range cases {
		var e envelope
		got, err := ForV1([]byte(c.in), &e)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if string(got) != c.out {
			t.Errorf("%s\n got %s\nwant %s", c.in, got, c.out)
		}
	}
}

func TestForV1Ambiguous(t *testing.T) {
	// v1 takes the last; with the zero value omitted, which one it took
	// cannot be seen in what it read.
	for _, in := range []string{
		`{"jsonrpc":"2.0","method":"m","Method":""}`,
		`{"jsonrpc":"2.0","Method":"m","METHOD":""}`,
		`{"jsonrpc":"2.0","method":"m","params":{"sessionId":"s","Title":"t","TITLE":""}}`,
	} {
		var e envelope
		if _, err := ForV1([]byte(in), &e); err == nil {
			t.Errorf("%s: accepted", in)
		}
	}
}

// Case variants of a name the type does not have are unknown to v1 as
// to this dialect, so the line passes as written.
func TestForV1PassesUnknownCaseVariants(t *testing.T) {
	for _, in := range []string{
		`{"jsonrpc":"2.0","foo":1,"Foo":2}`,
		`{"jsonrpc":"2.0","method":"x/y","ext":1,"Ext":2,"EXT":3}`,
		`{"jsonrpc":"2.0","method":"m","params":{"sessionId":"s","xFoo":1,"xfoo":2}}`,
	} {
		var e envelope
		got, err := ForV1([]byte(in), &e)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if string(got) != in {
			t.Errorf("%s: changed to %s", in, got)
		}
	}
}

// After ForV1, v1 reads every member name that this dialect reads, and
// nothing under another name.
func TestForV1AgreesWithV2(t *testing.T) {
	names := []string{"jsonrpc", "JSONRPC", "method", "Method", "METHOD", "methoD", "id", "ID", "Id",
		"params", "Params", "paramſ", "x", "sessionId", "sessionID", "SessionId", "title", "Title", "_meta", "_META"}
	rapid.Check(t, func(t *rapid.T) {
		var b bytes.Buffer
		b.WriteString(`{`)
		seen := map[string]bool{}
		for i, n := range rapid.SliceOfN(rapid.SampledFrom(names), 0, 6).Draw(t, "top") {
			if seen[n] {
				continue
			}
			seen[n] = true
			if i > 0 && b.Len() > 1 {
				b.WriteString(",")
			}
			b.WriteString(`"` + n + `":`)
			if strings.EqualFold(n, "params") || n == "paramſ" {
				b.WriteString(`{"sessionId":"s","Title":"T","title":"t"}`)
			} else if strings.EqualFold(n, "id") {
				b.WriteString(`3`)
			} else {
				b.WriteString(`"` + rapid.SampledFrom([]string{"", "v", "_relay/x"}).Draw(t, "v") + `"`)
			}
		}
		b.WriteString(`}`)
		in := b.Bytes()
		if Message(in) != nil {
			return
		}
		var e envelope
		out, err := ForV1(in, &e)
		if err != nil {
			return // refused: not acted on
		}
		var strict struct {
			JSONRPC string `json:"jsonrpc"`
			Method  string `json:"method"`
			Params  *struct {
				Title string `json:"title"`
			} `json:"params"`
		}
		if err := Unmarshal(in, &strict); err != nil {
			t.Fatal(err)
		}
		var loose envelope
		if err := jsonv1.Unmarshal(out, &loose); err != nil {
			t.Fatal(err)
		}
		if loose.Method != strict.Method || loose.JSONRPC != strict.JSONRPC {
			t.Fatalf("%s -> %s: v1 read %q/%q, the dialect %q/%q", in, out, loose.JSONRPC, loose.Method, strict.JSONRPC, strict.Method)
		}
		if (loose.Params == nil) != (strict.Params == nil) || loose.Params != nil && loose.Params.Title != strict.Params.Title {
			t.Fatalf("%s -> %s: params differ", in, out)
		}
	})
}
