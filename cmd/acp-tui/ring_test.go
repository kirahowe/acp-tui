package main

import (
	"bytes"
	"testing"

	"pgregory.net/rapid"
)

// The stderr tail holds exactly the last max bytes written, however the
// writes are split.
func TestRingKeepsTheTail(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		r := &ring{max: rapid.IntRange(1, 64).Draw(t, "max")}
		var all []byte
		for _, w := range rapid.SliceOf(rapid.SliceOfN(rapid.Byte(), 0, 100)).Draw(t, "writes") {
			if n, err := r.Write(w); n != len(w) || err != nil {
				t.Fatalf("Write gave %d, %v", n, err)
			}
			all = append(all, w...)
		}
		want := all[max(0, len(all)-r.max):]
		if got := r.bytes(); !bytes.Equal(got, want) {
			t.Fatalf("tail %q, want %q", got, want)
		}
	})
}
