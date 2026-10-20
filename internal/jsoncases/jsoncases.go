// Package jsoncases reads the shared table of JSON lines that relay,
// agentloop and acp-tui must read alike (relay#58), for tests.
package jsoncases

import (
	"bufio"
	"encoding/base64"
	jsonv1 "encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Case is one line of the shared table, testdata/strict-json/cases.jsonl.
type Case struct {
	Name   string
	Line   []byte
	Reject bool
	Method *string // when accepted: the exact member method, or nil
}

// Read reads the shared table. It is a file in this repo, not output
// from another process, so v1 is fine for it.
func Read(t testing.TB) []Case {
	t.Helper()
	f, err := os.Open(filepath.Join(root(t), "testdata", "strict-json", "cases.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []Case
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var raw struct {
			Name   string            `json:"name"`
			B64    string            `json:"b64"`
			Expect jsonv1.RawMessage `json:"expect"`
		}
		if err := jsonv1.Unmarshal(sc.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		line, err := base64.StdEncoding.DecodeString(raw.B64)
		if err != nil {
			t.Fatal(err)
		}
		c := Case{Name: raw.Name, Line: line}
		if string(raw.Expect) == `"reject"` {
			c.Reject = true
		} else {
			var e struct {
				Method *string `json:"method"`
			}
			if err := jsonv1.Unmarshal(raw.Expect, &e); err != nil {
				t.Fatalf("%s: %v", raw.Name, err)
			}
			c.Method = e.Method
		}
		out = append(out, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no cases")
	}
	return out
}

// root is the repo's root, found from this file.
func root(t testing.TB) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}
