// Package markdown parses the deliberately small structural Markdown subset
// used by ticket bodies. Section content is otherwise opaque user data.
package markdown

import (
	"bytes"
	"fmt"
	"strings"
)

// Diagnostic is a parse diagnostic with severity error|warning.
type Diagnostic struct {
	Severity string
	Code     string
	Line     int
	Message  string
}

func (d Diagnostic) IsError() bool { return d.Severity == "error" }

// KnownSection is the canonical known-section table.
type KnownSection struct {
	Key     string
	Heading string
}

// KnownSections is the fixed order of known sections.
var KnownSections = []KnownSection{
	{"objective", "Objective"},
	{"acceptance", "Acceptance"},
	{"handoff", "Handoff"},
	{"work_log", "Work log"},
	{"outcome", "Outcome"},
}

func KnownHeading(text string) (string, bool) {
	text = strings.TrimSpace(text)
	for _, ks := range KnownSections {
		if strings.EqualFold(text, ks.Heading) {
			return ks.Key, true
		}
	}
	return "", false
}

// Section is one parsed section with source ranges.
type Section struct {
	Heading      string
	Key          string
	HeadingLine  int
	ContentStart int
	ContentEnd   int
}

func (s Section) Content(body []byte) string {
	return string(body[s.ContentStart:s.ContentEnd])
}

// DisplayContent strips exactly one trailing LF. CRLF content retains its CR
// because it is part of the original user-authored bytes.
func (s Section) DisplayContent(body []byte) string {
	return strings.TrimSuffix(s.Content(body), "\n")
}

// Body is the result of parsing a task body.
type Body struct {
	Title       string
	TitleLine   int
	Metadata    *Metadata
	MetadataEnd int
	Sections    []Section
	Diagnostics []Diagnostic
}

func (b *Body) addError(code string, line int, msg string) {
	b.Diagnostics = append(b.Diagnostics, Diagnostic{"error", code, line, msg})
}

func (b *Body) addWarning(code string, line int, msg string) {
	b.Diagnostics = append(b.Diagnostics, Diagnostic{"warning", code, line, msg})
}

// SplitMetadata separates leading "---" delimiters. The first line must
// be exactly "---" and the next exact delimiter closes the metadata block.
func SplitMetadata(data []byte) (fm, body []byte, hadBOM bool, diags []Diagnostic) {
	b := data
	if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		b = b[3:]
		hadBOM = true
	}
	nl := bytes.IndexByte(b, '\n')
	if nl < 0 {
		diags = append(diags, Diagnostic{"error", "no_metadata", 1, "TASK.md must start with a metadata block."})
		return nil, nil, hadBOM, diags
	}
	first := string(b[:nl])
	if first != "---" && first != "---\r" {
		diags = append(diags, Diagnostic{"error", "no_metadata", 1, "TASK.md must start with a metadata block."})
		return nil, nil, hadBOM, diags
	}
	rest := b[nl+1:]
	offset := 0
	search := rest
	for {
		nnl := bytes.IndexByte(search, '\n')
		var line []byte
		if nnl < 0 {
			line = search
		} else {
			line = search[:nnl]
		}
		if string(line) == "---" || string(line) == "---\r" {
			fmEnd := offset + len(line)
			fm = rest[:fmEnd-len(line)]
			if nnl < 0 {
				return fm, nil, hadBOM, nil
			}
			return fm, rest[fmEnd+1:], hadBOM, nil
		}
		if nnl < 0 {
			diags = append(diags, Diagnostic{"error", "unterminated_metadata", 1, "The metadata block is not terminated by ---."})
			return nil, nil, hadBOM, diags
		}
		offset += nnl + 1
		search = search[nnl+1:]
	}
}

// Diag constructs a diagnostic with keyed fields.
func Diag(severity, code string, line int, message string) Diagnostic {
	return Diagnostic{Severity: severity, Code: code, Line: line, Message: message}
}

type bodyLine struct {
	start int
	end   int
	text  string
}

type heading struct {
	level        int
	text         string
	line         int
	start        int
	contentStart int
}

func splitBodyLines(body []byte) []bodyLine {
	lines := make([]bodyLine, 0, bytes.Count(body, []byte{'\n'})+1)
	start := 0
	for start < len(body) {
		rel := bytes.IndexByte(body[start:], '\n')
		if rel < 0 {
			lines = append(lines, bodyLine{start: start, end: len(body), text: string(body[start:])})
			break
		}
		end := start + rel + 1
		lines = append(lines, bodyLine{start: start, end: end, text: string(body[start : end-1])})
		start = end
	}
	return lines
}

func parseHeading(line string) (level int, text string, ok bool) {
	line = strings.TrimSuffix(line, "\r")
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	start := i
	for i < len(line) && line[i] == '#' {
		i++
	}
	level = i - start
	if level < 1 || level > 2 || (i < len(line) && line[i] != ' ' && line[i] != '\t') {
		return 0, "", false
	}
	if i < len(line) {
		text = strings.TrimSpace(line[i:])
	}
	return level, text, true
}

func parseFence(line string) (char byte, length int, ok bool) {
	line = strings.TrimSuffix(line, "\r")
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return 0, 0, false
	}
	char = line[i]
	for i < len(line) && line[i] == char {
		length++
		i++
	}
	if length < 3 {
		return 0, 0, false
	}
	// Backtick fences cannot have a backtick in their info string.
	if char == '`' && strings.Contains(line[i:], "`") {
		return 0, 0, false
	}
	return char, length, true
}

func closesFence(line string, char byte, length int) bool {
	line = strings.TrimSuffix(line, "\r")
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || line[i] != char {
		return false
	}
	n := 0
	for i < len(line) && line[i] == char {
		n++
		i++
	}
	return n >= length && strings.TrimSpace(line[i:]) == ""
}

func scanHeadings(body []byte) ([]bodyLine, []heading) {
	lines := splitBodyLines(body)
	heads := make([]heading, 0)
	var fenceChar byte
	fenceLength := 0
	for i, line := range lines {
		if fenceChar != 0 {
			if closesFence(line.text, fenceChar, fenceLength) {
				fenceChar = 0
				fenceLength = 0
			}
			continue
		}
		if char, length, ok := parseFence(line.text); ok {
			fenceChar, fenceLength = char, length
			continue
		}
		level, text, ok := parseHeading(line.text)
		if ok {
			contentStart := line.end
			if i+1 < len(lines) && strings.TrimSpace(strings.TrimSuffix(lines[i+1].text, "\r")) == "" {
				contentStart = lines[i+1].end
			}
			heads = append(heads, heading{level, text, i + 1, line.start, contentStart})
		}
	}
	return lines, heads
}

func firstNonBlankLine(lines []bodyLine) int {
	for i, line := range lines {
		if strings.TrimSpace(strings.TrimSuffix(line.text, "\r")) != "" {
			return i + 1
		}
	}
	return 0
}

// ParseBody parses a task body using only ATX H1/H2 headings and fence-aware
// line scanning. Setext headings and deeper headings are ordinary content.
func ParseBody(body []byte) *Body {
	b := &Body{}
	lines, heads := scanHeadings(body)
	first := firstNonBlankLine(lines)
	if len(heads) == 0 || heads[0].line != first || heads[0].level != 1 {
		b.addError("first_block_not_h1", first, "The first non-blank body line must be an H1 title using ATX syntax (\"# Title\").")
		return b
	}
	title := strings.TrimSpace(heads[0].text)
	b.Title = title
	b.TitleLine = heads[0].line
	b.Metadata, b.MetadataEnd = parseVisibleMetadata(body, lines, heads[0].contentStart)
	if title == "" {
		b.addError("empty_title", heads[0].line, "The H1 title must not be empty.")
	}
	if len([]rune(title)) > 240 {
		b.addError("title_too_long", heads[0].line, "The title exceeds 240 characters.")
	}
	for i := 1; i < len(heads); i++ {
		if heads[i].level == 1 {
			b.addError("multiple_h1", heads[i].line, "Only one H1 heading is allowed; it is the title.")
		}
	}
	seen := map[string]bool{}
	for i, h := range heads {
		if h.level != 2 {
			continue
		}
		end := len(body)
		for j := i + 1; j < len(heads); j++ {
			if heads[j].level <= 2 {
				end = heads[j].start
				break
			}
		}
		key, known := KnownHeading(h.text)
		if known && seen[key] {
			b.addWarning("duplicate_section", h.line, fmt.Sprintf("Section %q appears more than once; the first section is used.", h.text))
		}
		if known {
			seen[key] = true
		}
		b.Sections = append(b.Sections, Section{Heading: h.text, Key: key, HeadingLine: h.line, ContentStart: h.contentStart, ContentEnd: end})
	}
	return b
}

var visibleMetadataKeys = map[string]string{
	"State":          "state",
	"Priority":       "priority",
	"Assignee":       "assignee",
	"Parent":         "parent",
	"Depends on":     "depends_on",
	"Tags":           "tags",
	"Blocked reason": "blocked_reason",
}

// parseVisibleMetadata reads only the recognized list items immediately
// following the title. Everything else remains ordinary Markdown content.
func parseVisibleMetadata(body []byte, lines []bodyLine, afterTitle int) (*Metadata, int) {
	f := &Metadata{}
	end := afterTitle
	started := false
	seen := map[string]bool{}
	for _, line := range lines {
		if line.start < afterTitle {
			continue
		}
		text := strings.TrimSuffix(line.text, "\r")
		if strings.TrimSpace(text) == "" {
			if started {
				end = line.end
			}
			continue
		}
		label, raw, ok := parseVisibleMetadataLine(text)
		key, recognized := visibleMetadataKeys[label]
		if !ok || !recognized {
			break
		}
		if seen[key] {
			f.errAt("duplicate_key", lineNumber(lines, line.start), "Metadata key %q appears more than once.", label)
			end = line.end
			continue
		}
		valueRaw := raw
		if key == "priority" {
			if len(valueRaw) < 2 || (valueRaw[0] != 'P' && valueRaw[0] != 'p') {
				f.errAt("unsupported_metadata", lineNumber(lines, line.start), "Priority must use the form P0 through P4.")
				end = line.end
				continue
			}
			valueRaw = valueRaw[1:]
		}
		if key == "tags" || key == "depends_on" {
			valueRaw = "[" + valueRaw + "]"
		}
		parsed := ParseMetadata([]byte(key + ": " + valueRaw + "\n"))
		if len(parsed.Diagnostics) > 0 || len(parsed.Entries) != 1 {
			for _, d := range parsed.Diagnostics {
				f.Diagnostics = append(f.Diagnostics, Diagnostic{d.Severity, d.Code, lineNumber(lines, line.start), d.Message})
			}
			if len(parsed.Diagnostics) == 0 {
				f.errAt("unsupported_metadata", lineNumber(lines, line.start), "Metadata value for %q is invalid.", label)
			}
			end = line.end
			continue
		}
		seen[key] = true
		f.Entries = append(f.Entries, Entry{Key: key, Value: parsed.Entries[0].Value, Raw: line.text + lineEnding(line.text), Line: lineNumber(lines, line.start)})
		started = true
		end = line.end
	}
	return f, end
}

func parseVisibleMetadataLine(line string) (label, value string, ok bool) {
	if !strings.HasPrefix(line, "- ") {
		return "", "", false
	}
	colon := strings.IndexByte(line[2:], ':')
	if colon < 0 {
		return "", "", false
	}
	colon += 2
	label = strings.TrimSpace(line[2:colon])
	if label == "" {
		return "", "", false
	}
	return label, strings.TrimSpace(line[colon+1:]), true
}

func lineNumber(lines []bodyLine, start int) int {
	for i, line := range lines {
		if line.start == start {
			return i + 1
		}
	}
	return 0
}

func lineEnding(line string) string {
	if strings.HasSuffix(line, "\r") {
		return "\r\n"
	}
	return "\n"
}

// ContainsTopLevelHeading reports whether text contains an H1 or H2 outside
// fenced code blocks. It uses the same scanner as ParseBody.
func ContainsTopLevelHeading(text string) bool {
	_, heads := scanHeadings([]byte(text))
	return len(heads) > 0
}

// PrepareObjective sanitizes forgiving human Markdown for storage as section
// content. H1 and H2 headings outside fenced code become H3 headings. When
// extractTitle is true, only a first meaningful H1 may supply the title; the
// heading and its following separator are removed from the objective.
func PrepareObjective(text string, extractTitle bool) (title, objective string) {
	body := []byte(text)
	if extractTitle {
		lines, heads := scanHeadings(body)
		first := firstNonBlankLine(lines)
		for _, head := range heads {
			if head.line != first || head.level != 1 || strings.TrimSpace(head.text) == "" {
				continue
			}
			title = strings.TrimSpace(head.text)
			body = append([]byte(nil), body[head.contentStart:]...)
			break
		}
	}
	return title, string(demoteHeadings(body))
}

func demoteHeadings(body []byte) []byte {
	lines, heads := scanHeadings(body)
	if len(heads) == 0 {
		return append([]byte(nil), body...)
	}
	byStart := make(map[int]heading, len(heads))
	for _, head := range heads {
		byStart[head.start] = head
	}
	var out bytes.Buffer
	pos := 0
	for _, line := range lines {
		head, ok := byStart[line.start]
		if !ok {
			continue
		}
		lineEnd := line.end
		if lineEnd > line.start && body[lineEnd-1] == '\n' {
			lineEnd--
		}
		if lineEnd > line.start && body[lineEnd-1] == '\r' {
			lineEnd--
		}
		indent := 0
		for indent < lineEnd-line.start && indent < 3 && body[line.start+indent] == ' ' {
			indent++
		}
		out.Write(body[pos : line.start+indent])
		out.WriteString("###")
		out.Write(body[line.start+indent+head.level : lineEnd])
		pos = lineEnd
	}
	out.Write(body[pos:])
	return out.Bytes()
}
