// Package domain implements ticket semantics: TASK.md parsing and
// validation, create, list, and show operations.
package domain

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"ticket/internal/contract"
	"ticket/internal/markdown"
	"ticket/internal/store"
)

// TaskMaxBytes is the managed TASK.md ceiling.
const TaskMaxBytes = 1 << 20 // 1 MiB

var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var assigneePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// mustResolve resolves the subject ticket of an operation. A missing
// subject is not_found; dangling_reference is reserved for the
// relationship references (parent, depends_on, ...).
func mustResolve(st *store.Store, ref string) (string, error) {
	full, err := st.ResolveID(ref, true)
	if err != nil {
		if cErr, ok := err.(*contract.Error); ok && cErr.Code == contract.ErrDanglingReference {
			return "", contract.NewError(contract.ErrNotFound, "Ticket not found.", map[string]any{"id": ref})
		}
		return "", err
	}
	return full, nil
}

// Ticket is one parsed ticket (TASK.md plus its body).
type Ticket struct {
	ID            string
	TaskRelPath   string
	Title         string
	State         string
	Assignee      string
	BlockedReason string
	Priority      int
	Tags          []string
	Parent        string
	DependsOn     []string

	HadBOM         bool
	FileBytes      []byte
	Metadata       *markdown.Metadata
	MetadataRaw    []byte
	LineEnding     string
	Body           []byte
	Sections       map[string]markdown.Section // known keys in document order
	CustomSections []markdown.Section
	Diagnostics    []markdown.Diagnostic
}

// Section returns the section content for a known key, when present.
func (t *Ticket) Section(key string) (string, bool) {
	s, ok := t.Sections[key]
	if !ok {
		return "", false
	}
	return s.DisplayContent(t.Body), true
}

// IsTerminal reports whether the ticket is in a terminal state.
func (t *Ticket) IsTerminal() bool { return t.State == "completed" || t.State == "rejected" }

// ParseTicketFile parses TASK.md bytes into a Ticket. Malformed
// structure is a contract error carrying up to 20 diagnostics.
func ParseTicketFile(id string, data []byte) (*Ticket, error) {
	if len(data) > TaskMaxBytes {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"TASK.md exceeds the 1 MiB managed-file limit.", map[string]any{"path": id + "/TASK.md", "size": len(data)})
	}
	var metadataRaw, body []byte
	var hadBOM bool
	var diags []markdown.Diagnostic
	if hasMetadataBlock(data) {
		metadataRaw, body, hadBOM, diags = markdown.SplitMetadata(data)
		if len(diags) > 0 {
			return nil, diagError(id, diags)
		}
	} else {
		body = data
	}
	var f *markdown.Metadata
	if metadataRaw != nil {
		f = markdown.ParseMetadata(metadataRaw)
	} else {
		f = &markdown.Metadata{}
	}
	t := &Ticket{
		ID:          id,
		TaskRelPath: id + "/TASK.md",
		HadBOM:      hadBOM, Body: body,
		FileBytes: data, Metadata: f, MetadataRaw: metadataRaw,
		LineEnding: lineEndingOf(data),
		Priority:   2, Tags: []string{}, DependsOn: []string{},
	}
	bodyParse := markdown.ParseBody(body)
	if metadataRaw == nil {
		if bodyParse.Metadata != nil {
			f = bodyParse.Metadata
		}
	}
	t.Metadata = f
	applyMetadata(t, f)
	diags = append(diags, f.Diagnostics...)
	diags = append(diags, bodyParse.Diagnostics...)
	if hasErrors(diags) {
		return nil, diagError(id, diags)
	}
	t.Title = bodyParse.Title
	t.Sections = map[string]markdown.Section{}
	for _, s := range bodyParse.Sections {
		if s.Key != "" {
			previous, dup := t.Sections[s.Key]
			if !dup || (s.Key == "objective" &&
				strings.TrimSpace(previous.DisplayContent(t.Body)) == "" &&
				strings.TrimSpace(s.DisplayContent(t.Body)) != "") {
				t.Sections[s.Key] = s
			}
		} else {
			t.CustomSections = append(t.CustomSections, s)
		}
	}
	present := map[string]bool{}
	for _, s := range bodyParse.Sections {
		if s.Key != "" {
			present[s.Key] = true
		}
	}
	for _, ks := range markdown.KnownSections {
		if !present[ks.Key] {
			diags = append(diags, markdown.Diag("warning", "missing_section", 0, fmt.Sprintf("Known section %q is missing.", ks.Heading)))
		}
	}
	t.Diagnostics = diags
	return t, nil
}

func hasMetadataBlock(data []byte) bool {
	b := data
	if bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}) {
		b = b[3:]
	}
	return bytes.HasPrefix(b, []byte("---\n")) || bytes.HasPrefix(b, []byte("---\r\n"))
}

func objectivePreview(t *Ticket) string {
	if t == nil {
		return ""
	}
	text := strings.TrimSpace(t.SectionText("objective"))
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > 80 {
			return string(runes[:79]) + "…"
		}
		return line
	}
	return ""
}

// lineEndingOf reports the dominant line ending of the file.
func lineEndingOf(data []byte) string {
	if bytes.Contains(data, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

func hasErrors(diags []markdown.Diagnostic) bool {
	for _, d := range diags {
		if d.IsError() {
			return true
		}
	}
	return false
}

// diagError builds the invalid_ticket error with bounded diagnostics.
func diagError(id string, diags []markdown.Diagnostic) *contract.Error {
	sums := make([]map[string]any, 0, len(diags))
	for _, d := range diags {
		if len(sums) >= 20 {
			break
		}
		sums = append(sums, map[string]any{
			"severity": d.Severity, "code": d.Code, "message": d.Message,
			"line": d.Line, "path": id + "/TASK.md",
		})
	}
	details := map[string]any{"diagnostics": sums}
	if len(diags) > 20 {
		details["diagnostics_truncated"] = true
	}
	return contract.NewError(contract.ErrInvalidTicket,
		"TASK.md failed validation.", details)
}

// applyMetadata extracts known fields with exact types; unknown
// keys are retained as warnings and hard type errors are
// recorded.
func applyMetadata(t *Ticket, f *markdown.Metadata) {
	seen := map[string]bool{}
	for _, e := range f.Entries {
		seen[e.Key] = true
		switch e.Key {
		case "state":
			if v, ok := e.Value.(string); ok {
				if v != "open" && v != "hold" && v != "review" && v != "signoff" && v != "completed" && v != "rejected" {
					f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_value", e.Line, "state must be open, hold, review, signoff, completed, or rejected."))
				} else {
					t.State = v
				}
			} else {
				f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_type", e.Line, "state must be a string."))
			}
		case "priority":
			if v, ok := toInt(e.Value); ok {
				if v < 0 || v > 4 {
					f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_value", e.Line, "priority must be an integer 0-4."))
				} else {
					t.Priority = int(v)
				}
			} else {
				f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_type", e.Line, "priority must be an integer."))
			}
		case "tags":
			if v, ok := stringSlice(e.Value); ok {
				t.Tags = v
			} else {
				f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_type", e.Line, "tags must be a list of strings."))
			}
		case "parent":
			if v, ok := e.Value.(string); ok {
				t.Parent = v
			} else {
				f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_type", e.Line, "parent must be a string."))
			}
		case "depends_on":
			if v, ok := stringSlice(e.Value); ok {
				t.DependsOn = v
			} else {
				f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_type", e.Line, "depends_on must be a list of strings."))
			}
		case "assignee":
			if v, ok := e.Value.(string); ok {
				t.Assignee = v
			} else {
				f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_type", e.Line, "assignee must be a string."))
			}
		case "blocked_reason":
			if v, ok := e.Value.(string); ok {
				t.BlockedReason = v
			} else {
				f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_type", e.Line, "blocked_reason must be a string."))
			}
		default:
			// Unknown top-level key: retained (warning only).
			f.Diagnostics = append(f.Diagnostics, markdown.Diag("warning", "unknown_field", e.Line, fmt.Sprintf("Unknown top-level metadata key %q is retained.", e.Key)))
		}
	}
	// Required fields.
	if !seen["state"] {
		f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "missing_field", 0, "Required field state is missing."))
	}
	t.crossField(f)
}

func (t *Ticket) crossField(f *markdown.Metadata) {
	if t.State == "" {
		return
	}
	// ID-shape validation for relationship fields (existence is checked
	// by operations, not the parser).
	checkIDs(f, "parent", t.Parent)
	checkIDList(f, "depends_on", t.DependsOn)
	seenTags := map[string]bool{}
	for i, tag := range t.Tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_value", 0, fmt.Sprintf("tags[%d] must not be empty.", i)))
		} else if trimmed != tag {
			f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_value", 0, fmt.Sprintf("tags[%d] must not have surrounding whitespace.", i)))
		} else if !tagPattern.MatchString(tag) {
			f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_value", 0, fmt.Sprintf("tags[%d] must match %s.", i, tagPattern.String())))
		}
		if seenTags[trimmed] {
			f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "duplicate_key", 0, fmt.Sprintf("tags must be unique; duplicate %q.", tag)))
		}
		seenTags[trimmed] = true
	}
	if t.Assignee != "" && !assigneePattern.MatchString(t.Assignee) {
		f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_value", 0, fmt.Sprintf("assignee must match %s.", assigneePattern.String())))
	}
}

func checkIDs(f *markdown.Metadata, key, id string) {
	if id == "" {
		return
	}
	if _, _, ok := store.ParseID(id); !ok {
		f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_value", 0, fmt.Sprintf("%s must be a full ticket ID.", key)))
	}
}

func checkIDList(f *markdown.Metadata, key string, ids []string) {
	for _, id := range ids {
		if _, _, ok := store.ParseID(id); !ok {
			f.Diagnostics = append(f.Diagnostics, markdown.Diag("error", "field_value", 0, fmt.Sprintf("%s entries must be full ticket IDs.", key)))
			return
		}
	}
}

func normalizeTags(values []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			return nil, contract.NewError(contract.ErrInvalidArgument, "Tags must not contain empty values.", nil)
		}
		if !tagPattern.MatchString(tag) {
			return nil, contract.NewError(contract.ErrInvalidArgument,
				"Tag "+tag+" must match [a-z0-9][a-z0-9._-]{0,63}.", nil)
		}
		if seen[tag] {
			return nil, contract.NewError(contract.ErrInvalidArgument, "Tags must be unique.", nil)
		}
		seen[tag] = true
		out = append(out, tag)
	}
	sort.Strings(out)
	return out, nil
}

// NormalizeTags applies the ticket tag syntax and canonical ordering to
// configuration-provided or command-provided tag values.
func NormalizeTags(values []string) ([]string, error) {
	return normalizeTags(values)
}

func stringSlice(v any) ([]string, bool) {
	if s, ok := v.([]string); ok {
		return append([]string(nil), s...), true
	}
	s, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(s))
	for _, item := range s {
		str, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, str)
	}
	return out, true
}

// RenderNew builds the canonical TASK.md bytes for a new ticket.
func RenderNew(title string, priority int, tags []string, parent string, dependsOn []string, sections map[string]string) []byte {
	state := "hold"
	if strings.TrimSpace(sections["objective"]) != "" {
		state = "open"
	}
	return renderNew(title, priority, tags, parent, dependsOn, sections, state)
}

// RenderNewForEditor builds the starter shown by create -e. The editor
// presents a new ticket as open so the normal case needs no metadata edit;
// publication normalizes an empty Objective back to hold.
func RenderNewForEditor(title string, priority int, tags []string, parent string, dependsOn []string, sections map[string]string) []byte {
	return renderNew(title, priority, tags, parent, dependsOn, sections, "open")
}

func renderNew(title string, priority int, tags []string, parent string, dependsOn []string, sections map[string]string, state string) []byte {
	var body strings.Builder
	body.WriteString("# " + title + "\n\n")
	renderMetadata(&body, state, priority, tags, parent, dependsOn, "", "")
	body.WriteString("\n## Objective\n")
	body.WriteString("\n")
	if content := sections["objective"]; content != "" {
		body.WriteString(normalizeSection(content))
	} else {
		// Keep a real content line after the separator. The editor opens on
		// that line; without it, Vim clamps the requested line to the
		// separator and typing consumes the blank line.
		body.WriteString("\n")
	}
	for _, ks := range markdown.KnownSections[1:3] {
		content, present := sections[ks.Key]
		if !present {
			continue
		}
		body.WriteString("\n## " + ks.Heading + "\n\n")
		if content != "" {
			body.WriteString(normalizeSection(content))
		}
	}
	return []byte(body.String())
}

// RenderNewWithBody builds a canonical ticket while keeping the supplied
// Markdown after the manager-owned H1 title.
func RenderNewWithBody(title string, priority int, tags []string, parent string, dependsOn []string, body string) []byte {
	state := "hold"
	fullBody := []byte("# " + title + "\n\n" + body)
	parsed := markdown.ParseBody(fullBody)
	for _, section := range parsed.Sections {
		if section.Key == "objective" && strings.TrimSpace(section.DisplayContent(fullBody)) != "" {
			state = "open"
			break
		}
	}
	var out strings.Builder
	out.WriteString("# " + title + "\n\n")
	renderMetadata(&out, state, priority, tags, parent, dependsOn, "", "")
	out.WriteString("\n")
	out.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		out.WriteString("\n")
	}
	return []byte(out.String())
}

func renderMetadata(body *strings.Builder, state string, priority int, tags []string, parent string, dependsOn []string, assignee, blockedReason string) {
	fmt.Fprintf(body, "- State: %s\n- Priority: P%d\n", state, priority)
	if assignee != "" {
		fmt.Fprintf(body, "- Assignee: %s\n", assignee)
	}
	if parent != "" {
		fmt.Fprintf(body, "- Parent: %s\n", parent)
	}
	if len(tags) > 0 {
		body.WriteString("- Tags: ")
		for i, t := range tags {
			if i > 0 {
				body.WriteString(", ")
			}
			body.WriteString(t)
		}
		body.WriteString("\n")
	}
	if len(dependsOn) > 0 {
		body.WriteString("- Depends on: ")
		for i, id := range dependsOn {
			if i > 0 {
				body.WriteString(", ")
			}
			body.WriteString(id)
		}
		body.WriteString("\n")
	}
	if blockedReason != "" {
		fmt.Fprintf(body, "- Blocked reason: %s\n", blockedReason)
	}
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		if strconv.IntSize == 32 && (x < -1<<31 || x > 1<<31-1) {
			return 0, false
		}
		return int(x), true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Trunc(x) != x {
			return 0, false
		}
		if strconv.IntSize == 32 {
			if x < float64(-1<<31) || x > float64(1<<31-1) {
				return 0, false
			}
		} else if x < -float64(1<<63) || x >= float64(1<<63) {
			return 0, false
		}
		return int(x), true
	}
	return 0, false
}

// normalizeSection converts line endings to LF and ensures exactly one
// trailing newline.
func normalizeSection(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	return s + "\n"
}

// ValidateSectionContent rejects top-level H1/H2 headings (outside code
// fences) in section input and normalizes line endings.
func ValidateSectionContent(key, content string) (string, error) {
	if markdown.ContainsTopLevelHeading(content) {
		return "", contract.NewError(contract.ErrInvalidArgument,
			"Section content for "+key+" must not contain top-level headings.", nil)
	}
	return normalizeSection(content), nil
}
