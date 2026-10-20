// Package strictjson is the one way acp-tui reads JSON that another
// process wrote: the dialect relay, agentloop and acp-tui share (relay#58).
//
// The dialect is RFC 8259 JSON in valid UTF-8, one value and only
// whitespace after it, no duplicate member names at any depth (compared
// after unescaping), member names matched exactly, no byte order mark, no
// unpaired surrogate escapes, nesting at most MaxDepth levels, a number
// literal at most MaxNumber characters, and a number's exponent (the integer
// after e or E, sign included) within ±MaxExponent by value.
// encoding/json/v2 enforces all of it except the three limits, which Valid
// adds.
//
// The ACP SDK decodes with encoding/json v1, which matches member names
// case-insensitively. ForV1 removes the members v1 would read under a name
// other than the one written, so the SDK reads a line as this dialect does.
package strictjson

import (
	"bytes"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

const (
	// MaxDepth is the deepest nesting allowed; the top value is level 1.
	MaxDepth = 128
	// MaxNumber is the longest number literal allowed, in characters.
	MaxNumber = 1000
	// MaxExponent bounds a number's exponent either way.
	MaxExponent = 9999
)

var (
	errDepth     = fmt.Errorf("nested deeper than %d levels", MaxDepth)
	errNumber    = fmt.Errorf("number literal longer than %d characters", MaxNumber)
	errExponent  = fmt.Errorf("exponent outside ±%d", MaxExponent)
	errTrailing  = errors.New("more than one value")
	errNotObject = errors.New("not a JSON object")

	// ErrUndecodable says encoding/json v1 cannot decode a value into the
	// type ForV1 was given, so a v1 reader would refuse it too.
	ErrUndecodable = errors.New("v1 cannot decode it")
)

// Valid reports why b is not one JSON value in the dialect, or nil.
func Valid(b []byte) error {
	dec := jsontext.NewDecoder(bytes.NewReader(b))
	for {
		switch dec.PeekKind() {
		case '0':
			v, err := dec.ReadValue()
			if err != nil {
				return err
			}
			if len(v) > MaxNumber {
				return errNumber
			}
			if !exponentInBounds(v) {
				return errExponent
			}
		default: // includes 0, the kind of an error or the end
			if _, err := dec.ReadToken(); err != nil {
				return err
			}
			if dec.StackDepth() > MaxDepth {
				return errDepth
			}
		}
		if dec.StackDepth() == 0 {
			break
		}
	}
	if _, err := dec.ReadToken(); err != io.EOF {
		if err == nil {
			return errTrailing
		}
		return err
	}
	return nil
}

// Message reports why b is not one message: a JSON object in the dialect.
func Message(b []byte) error {
	if err := Valid(b); err != nil {
		return err
	}
	if jsontext.Value(b).Kind() != '{' {
		return errNotObject
	}
	return nil
}

// Unmarshal checks b against the dialect and decodes it into v with
// encoding/json/v2, which matches member names exactly and ignores
// unknown ones.
func Unmarshal(b []byte, v any) error {
	if err := Valid(b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// maxPasses bounds ForV1's rewrites. Each pass removes at least one
// member, and real traffic needs none.
const maxPasses = 64

// ForV1 prepares b, which must be valid, for a reader that decodes it into
// v's type with encoding/json v1, such as the ACP SDK. It returns b with
// every member removed that v1 would read under a name other than the one
// written: "Method" or "paramſ" where the type has method or params. This
// dialect ignores such a member as unknown, so after the removal v1 reads
// what this dialect reads. b comes back unchanged when nothing needs
// removing, which is every line a JSON encoder wrote. v is set to the v1
// decoding of the result. Two members that fold to the same name and that
// v1 keeps under neither pass when v1 ignores both, as an unknown name;
// when v1 puts one of them in a field, they are an error, since which one
// it takes is not knowable here. Anything v1 cannot decode into v at all comes back
// unchanged with ErrUndecodable, for the caller to decide.
func ForV1(b []byte, v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, errors.New("strictjson: ForV1 needs a non-nil pointer")
	}
	for range maxPasses {
		fresh := reflect.New(rv.Elem().Type())
		if err := jsonv1.Unmarshal(b, fresh.Interface()); err != nil {
			return b, fmt.Errorf("%w: %w", ErrUndecodable, err)
		}
		back, err := jsonv1.Marshal(fresh.Interface())
		if err != nil {
			return b, err
		}
		written, err := tree(b)
		if err != nil {
			return b, err
		}
		read, err := tree(back)
		if err != nil {
			return b, err
		}
		drop := map[jsontext.Pointer]bool{}
		var groups [][]jsontext.Pointer
		misread(written, read, "", drop, &groups)
		for _, g := range groups {
			if err := ignored(b, g, rv.Elem().Type()); err != nil {
				return b, err
			}
		}
		if len(drop) == 0 {
			rv.Elem().Set(fresh.Elem())
			return b, nil
		}
		if b, err = without(b, drop); err != nil {
			return b, err
		}
	}
	return b, errors.New("strictjson: too many misread members")
}

// node is the shape of a JSON value: its members or elements, and nothing
// of its scalars.
type node struct {
	members  map[string]*node
	elements []*node
}

func tree(b []byte) (*node, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(b), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	return readNode(dec)
}

func readNode(dec *jsontext.Decoder) (*node, error) {
	switch dec.PeekKind() {
	case '{':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		n := &node{members: map[string]*node{}}
		for dec.PeekKind() != '}' {
			tok, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			name := tok.String()
			child, err := readNode(dec)
			if err != nil {
				return nil, err
			}
			n.members[name] = child
		}
		_, err := dec.ReadToken()
		return n, err
	case '[':
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		n := &node{}
		for dec.PeekKind() != ']' {
			child, err := readNode(dec)
			if err != nil {
				return nil, err
			}
			n.elements = append(n.elements, child)
		}
		_, err := dec.ReadToken()
		return n, err
	}
	return nil, dec.SkipValue()
}

var errAmbiguous = errors.New("member names that differ only in case")

// misread compares what was written with what v1 read, re-encoded, and
// records in drop each written member that v1 read under another name.
// A written name v1 did not keep, that folds to one it kept, is one.
// Written names that fold together where v1 kept none of them go to
// groups, one group of members per object, for ignored to settle.
func misread(written, read *node, at jsontext.Pointer, drop map[jsontext.Pointer]bool, groups *[][]jsontext.Pointer) {
	if written == nil || read == nil {
		return
	}
	if written.members != nil && read.members != nil {
		grouped := map[string]bool{}
		for name, child := range written.members {
			if sub, ok := read.members[name]; ok {
				misread(child, sub, at.AppendToken(name), drop, groups)
				continue
			}
			for other := range read.members {
				if other != name && strings.EqualFold(other, name) {
					drop[at.AppendToken(name)] = true
				}
			}
			if drop[at.AppendToken(name)] || grouped[name] {
				continue
			}
			g := []jsontext.Pointer{at.AppendToken(name)}
			for other := range written.members {
				if _, kept := read.members[other]; !kept && other != name && strings.EqualFold(other, name) {
					g = append(g, at.AppendToken(other))
					grouped[other] = true
				}
			}
			if len(g) > 1 {
				grouped[name] = true
				*groups = append(*groups, g)
			}
		}
	}
	for i := range min(len(written.elements), len(read.elements)) {
		misread(written.elements[i], read.elements[i], at.AppendToken(fmt.Sprint(i)), drop, groups)
	}
}

// ignored reports an error unless v1, decoding b into t, ignores every
// member in group: names that differ only in case, none of which v1 kept
// in what it read. v1 may still have put one in a field that the
// re-encoding hides, such as a zero value under omitempty, and which one
// it took is not knowable from what it read. So each member is tried
// alone, the others removed, against none of them: if no member changes
// what v1 decodes, v1 ignores them all as this dialect does, and b may
// pass as written.
func ignored(b []byte, group []jsontext.Pointer, t reflect.Type) error {
	decode := func(keep jsontext.Pointer) (any, error) {
		drop := map[jsontext.Pointer]bool{}
		for _, p := range group {
			if p != keep {
				drop[p] = true
			}
		}
		pruned, err := without(b, drop)
		if err != nil {
			return nil, err
		}
		v := reflect.New(t)
		if err := jsonv1.Unmarshal(pruned, v.Interface()); err != nil {
			return nil, err
		}
		return v.Interface(), nil
	}
	none, err := decode("")
	if err != nil {
		return fmt.Errorf("%w at %q", errAmbiguous, group[0].Parent())
	}
	for _, p := range group {
		one, err := decode(p)
		if err != nil || !reflect.DeepEqual(one, none) {
			return fmt.Errorf("%w at %q", errAmbiguous, p.Parent())
		}
	}
	return nil
}

// without is b with the members at drop removed, its other bytes as they
// were apart from whitespace.
func without(b []byte, drop map[jsontext.Pointer]bool) ([]byte, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(b))
	var out bytes.Buffer
	enc := jsontext.NewEncoder(&out, jsontext.PreserveRawStrings(true))
	for {
		kind := dec.PeekKind()
		if kind == 0 {
			break
		}
		if kind == '"' {
			if k, _ := dec.StackIndex(dec.StackDepth()); k == '{' {
				// A name at an even position in the object, before its value.
				if _, n := dec.StackIndex(dec.StackDepth()); n%2 == 0 {
					name, err := dec.ReadValue()
					if err != nil {
						return nil, err
					}
					if drop[dec.StackPointer()] {
						if err := dec.SkipValue(); err != nil {
							return nil, err
						}
						continue
					}
					if err := enc.WriteValue(name); err != nil {
						return nil, err
					}
					continue
				}
			}
		}
		if kind == '{' || kind == '[' || kind == '}' || kind == ']' {
			tok, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			if err := enc.WriteToken(tok); err != nil {
				return nil, err
			}
			continue
		}
		v, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		if err := enc.WriteValue(v); err != nil {
			return nil, err
		}
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

// exponentInBounds reports whether the exponent of the number literal v,
// if it has one, is within ±MaxExponent. Leading zeros do not count, so
// the bound is on the value.
func exponentInBounds(v []byte) bool {
	i := bytes.IndexAny(v, "eE")
	if i < 0 {
		return true
	}
	digits := bytes.TrimLeft(bytes.TrimLeft(v[i+1:], "+-"), "0")
	if len(digits) > 4 {
		return false
	}
	n := 0
	for _, c := range digits {
		n = n*10 + int(c-'0')
	}
	return n <= MaxExponent
}
