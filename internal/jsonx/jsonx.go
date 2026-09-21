// Package jsonx provides strict JSON decoding shared by config loading
// and CLI --input handling: one document, no trailing values, no duplicate
// object keys, no unknown keys on typed targets, no accidental type
// coercion beyond the target's declared types.
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type frame struct {
	isObject bool
	wantKey  bool
	keys     map[string]struct{}
}

// Validate scans one JSON document and rejects duplicate object keys at
// any nesting depth, trailing content after the document, and
// unterminated structures.
func Validate(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var stack []frame
	done := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if !done {
				return fmt.Errorf("unterminated JSON document")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if done {
			return fmt.Errorf("trailing content after JSON document")
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, frame{isObject: true, wantKey: true, keys: make(map[string]struct{})})
			case '[':
				stack = append(stack, frame{isObject: false})
			case '}', ']':
				if len(stack) == 0 {
					return fmt.Errorf("unmatched closing delimiter")
				}
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					done = true
				} else if stack[len(stack)-1].isObject {
					stack[len(stack)-1].wantKey = true
				}
			default:
				return fmt.Errorf("unexpected delimiter %q", t)
			}
		case string:
			if len(stack) == 0 {
				done = true
				continue
			}
			f := &stack[len(stack)-1]
			if f.isObject && f.wantKey {
				if _, exists := f.keys[t]; exists {
					return fmt.Errorf("duplicate key %q", t)
				}
				f.keys[t] = struct{}{}
				f.wantKey = false
			} else if f.isObject {
				f.wantKey = true // a scalar value completed in an object
			}
		default:
			if len(stack) == 0 {
				done = true
			} else if stack[len(stack)-1].isObject {
				stack[len(stack)-1].wantKey = true
			}
		}
	}
}

// Decode decodes one strict JSON document into out (struct with json
// tags). It rejects: multiple documents, duplicate keys, unknown struct
// keys, and type mismatches.
// ErrUnknownField marks a rejected unknown field in structured input.
var ErrUnknownField = errors.New("unknown field")

func Decode(data []byte, out any) error {
	if err := Validate(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		if strings.Contains(err.Error(), "json: unknown field") {
			return fmt.Errorf("%w: %s", ErrUnknownField, err.Error())
		}
		return err
	}
	return nil
}
