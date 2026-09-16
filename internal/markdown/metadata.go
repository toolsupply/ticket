// Metadata parsing for the small language owned by the ticket
// manager. It deliberately supports only scalar values and flat string lists.
package markdown

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Entry is one top-level metadata key with its decoded value and raw text
// span. Raw is retained so unrelated metadata survives updates byte-for-byte.
type Entry struct {
	Key   string
	Value any
	Raw   string
	Line  int
}

// Metadata is parsed metadata.
type Metadata struct {
	Entries     []Entry
	Diagnostics []Diagnostic
}

func (f *Metadata) Get(key string) (any, bool) {
	for _, e := range f.Entries {
		if e.Key == key {
			return e.Value, true
		}
	}
	return nil, false
}

const (
	metadataMaxEntries = 200
	metadataMaxBytes   = 64 << 10
	metadataMaxLine    = 16 << 10
)

type metaLine struct {
	start int
	text  string
}

// ParseMetadata parses the application-owned metadata format. A metadata
// line is a top-level key followed by a scalar or flat string list. General
// data-language constructs such as nesting, anchors, tags, and multiline
// values are not part of this format.
func ParseMetadata(data []byte) *Metadata {
	f := &Metadata{}
	if len(data) == 0 || len(data) > metadataMaxBytes {
		if len(data) == 0 {
			f.errAt("empty_metadata", 1, "The metadata block must not be empty.")
		} else {
			f.errAt("metadata_too_large", 1, "The metadata exceeds %d bytes.", metadataMaxBytes)
		}
		return f
	}

	lines := splitMetaLines(data)
	seen := map[string]bool{}
	entryLines := make([]int, 0)
	for i, line := range lines {
		lineText := strings.TrimSuffix(line.text, "\r")
		if strings.TrimSpace(lineText) == "" {
			continue
		}
		if len(lineText) > metadataMaxLine {
			f.errAt("metadata_too_large", i+1, "Metadata line exceeds %d bytes.", metadataMaxLine)
			continue
		}
		key, rawValue, ok := splitMetaEntry(lineText)
		if !ok {
			f.errAt("unsupported_metadata", i+1, "Metadata must use one top-level key and value per line.")
			continue
		}
		if !validMetaKey(key) {
			f.errAt("unsupported_metadata", i+1, "Metadata key %q is not supported.", key)
			continue
		}
		if key == "<<" {
			f.errAt("unsupported_metadata", i+1, "Merge keys are not supported.")
			continue
		}
		if seen[key] {
			f.errAt("duplicate_key", i+1, "Metadata key %q appears more than once.", key)
			continue
		}
		value, err := parseMetaValue(rawValue)
		if err != nil {
			f.errAt("unsupported_metadata", i+1, "Metadata value for %q is invalid: %s", key, err)
			continue
		}
		seen[key] = true
		entryLines = append(entryLines, i)
		f.Entries = append(f.Entries, Entry{Key: key, Value: value, Line: i + 1})
	}
	if len(f.Entries) > metadataMaxEntries {
		f.errAt("metadata_too_large", 1, "The metadata has more than %d entries.", metadataMaxEntries)
	}
	for i := range f.Entries {
		start := lines[entryLines[i]].start
		end := len(data)
		if i+1 < len(entryLines) {
			end = lines[entryLines[i+1]].start
		}
		f.Entries[i].Raw = string(data[start:end])
	}
	return f
}

func (f *Metadata) errAt(code string, line int, format string, args ...any) {
	f.Diagnostics = append(f.Diagnostics, Diagnostic{"error", code, line, fmt.Sprintf(format, args...)})
}

func splitMetaLines(data []byte) []metaLine {
	lines := make([]metaLine, 0, bytes.Count(data, []byte{'\n'})+1)
	start := 0
	for start < len(data) {
		rel := bytes.IndexByte(data[start:], '\n')
		if rel < 0 {
			lines = append(lines, metaLine{start: start, text: string(data[start:])})
			break
		}
		end := start + rel + 1
		lines = append(lines, metaLine{start: start, text: string(data[start : end-1])})
		start = end
	}
	return lines
}

func splitMetaEntry(line string) (key, value string, ok bool) {
	if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
		return "", "", false
	}
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return "", "", false
	}
	return line[:colon], strings.TrimSpace(line[colon+1:]), true
}

func validMetaKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func parseMetaValue(raw string) (any, error) {
	if raw == "" {
		return "", nil
	}
	if raw[0] == '[' {
		return parseStringList(raw)
	}
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "}") ||
		strings.HasPrefix(raw, "&") || strings.HasPrefix(raw, "*") ||
		strings.HasPrefix(raw, "!") || strings.HasPrefix(raw, "|") ||
		strings.HasPrefix(raw, ">") || strings.HasPrefix(raw, "%") {
		return nil, fmt.Errorf("general metadata constructs are not supported")
	}
	if raw[0] == '"' || raw[0] == '\'' {
		return parseQuotedMetaString(raw)
	}
	if raw == "true" {
		return true, nil
	}
	if raw == "false" {
		return false, nil
	}
	if raw == "null" {
		return nil, nil
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n, nil
	}
	return raw, nil
}

func parseQuotedMetaString(raw string) (string, error) {
	quote := raw[0]
	if len(raw) < 2 || raw[len(raw)-1] != quote {
		return "", fmt.Errorf("unterminated quoted string")
	}
	if quote == '"' {
		value, err := strconv.Unquote(raw)
		if err != nil {
			return "", fmt.Errorf("invalid string escape")
		}
		return value, nil
	}
	value := raw[1 : len(raw)-1]
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("multiline strings are not supported")
	}
	return strings.ReplaceAll(value, "''", "'"), nil
}

func parseStringList(raw string) ([]string, error) {
	if len(raw) < 2 || raw[len(raw)-1] != ']' {
		return nil, fmt.Errorf("list must end with ]")
	}
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return []string{}, nil
	}
	parts := make([]string, 0, 4)
	start := 0
	var quote byte
	escaped := false
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if quote != 0 {
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
		} else if c == ',' {
			parts = append(parts, inner[start:i])
			start = i + 1
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted list item")
	}
	parts = append(parts, inner[start:])
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("list contains an empty item")
		}
		value, err := parseMetaValue(part)
		if err != nil {
			return nil, err
		}
		str, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("lists may contain strings only")
		}
		out = append(out, str)
	}
	return out, nil
}
