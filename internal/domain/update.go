package domain

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"ticket/internal/contract"
	"ticket/internal/markdown"
	"ticket/internal/store"
)

// setFieldOrder is the deterministic changed_fields order for set keys.
var setFieldOrder = []string{
	"title", "priority", "tags", "parent", "depends_on",
	"blocked_reason",
}

var setAllowlist = map[string]bool{}

func init() {
	for _, k := range setFieldOrder {
		setAllowlist[k] = true
	}
}

// canonicalFieldOrder is the metadata canonical order used when
// inserting fields that were not present in the document.
var canonicalFieldOrder = []string{
	"state", "priority", "tags", "parent", "depends_on", "assignee", "blocked_reason",
}

// UpdateOptions is one update of a ticket.
type UpdateOptions struct {
	// Set maps field names to new values; a null value clears an optional
	// field; absent keys are untouched.
	Set map[string]any
	// Sections maps known section keys to complete replacement content.
	Sections     map[string]string
	Actor        string
	allowWorkLog bool
}

// UpdateResult reports an update.
type UpdateResult struct {
	ID            string   `json:"id"`
	Changed       bool     `json:"changed"`
	ChangedFields []string `json:"changed_fields"`
}

// Update validates and commits set/sections changes as one TASK.md replacement.
func Update(st *store.Store, id string, opts UpdateOptions) (*UpdateResult, error) {
	full, err := mustResolve(st, id)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	if t.Assignee != "" {
		if err := validateActor(opts.Actor); err != nil {
			return nil, err
		}
		if t.Assignee != opts.Actor {
			return nil, contract.NewError(contract.ErrAlreadyClaimed, "The ticket is assigned to another actor.", map[string]any{"id": full})
		}
	}
	if err := validateUpdateSet(opts); err != nil {
		return nil, err
	}

	setChanged := map[string]bool{}
	if err := applySet(st, t, full, opts.Set, setChanged); err != nil {
		return nil, err
	}
	_, parentChanged := opts.Set["parent"]
	_, dependsChanged := opts.Set["depends_on"]
	if parentChanged || dependsChanged {
		graphTickets, err := loadGraph(st)
		if err != nil {
			return nil, err
		}
		if err := validateGraphCandidate(graphTickets, t); err != nil {
			return nil, err
		}
	}

	secChanged := map[string]bool{}
	newBody, err := applyBodyChanges(t, opts, secChanged)
	if err != nil {
		return nil, err
	}

	changedFields := changedFieldList(setChanged, secChanged)
	if len(changedFields) == 0 {
		return &UpdateResult{ID: full, Changed: false, ChangedFields: []string{}}, nil
	}

	data, err := renderUpdated(t, newBody, setChanged)
	if err != nil {
		return nil, err
	}
	if len(data) > TaskMaxBytes {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Updated TASK.md exceeds the 1 MiB managed-file limit.", nil)
	}
	if _, err := ParseTicketFile(full, data); err != nil {
		return nil, err
	}
	_, err = st.ReplaceTask(full, data, TaskMaxBytes)
	if err != nil {
		return nil, err
	}
	return &UpdateResult{ID: full, Changed: true, ChangedFields: changedFields}, nil
}

func changedFieldList(setChanged, secChanged map[string]bool) []string {
	out := []string{}
	for _, k := range setFieldOrder {
		if setChanged[k] {
			out = append(out, k)
		}
	}
	for _, ks := range markdown.KnownSections {
		if secChanged[ks.Key] {
			out = append(out, "sections."+ks.Key)
		}
	}
	return out
}

func validateUpdateSet(opts UpdateOptions) error {
	for key := range opts.Set {
		if !setAllowlist[key] {
			return contract.NewError(contract.ErrInvalidArgument,
				"Unknown set key "+key+". Allowed: "+strings.Join(setFieldOrder, ", ")+".", nil)
		}
	}
	return nil
}

func applySet(st *store.Store, t *Ticket, full string, set map[string]any, changed map[string]bool) error {
	// title
	if v, ok := set["title"]; ok && v != nil {
		s, ok := v.(string)
		if !ok {
			return contract.NewError(contract.ErrInvalidArgument, "set.title must be a string.", nil)
		}
		if err := validateTitle(s); err != nil {
			return err
		}
		if s != t.Title {
			t.Title = s
			changed["title"] = true
		}
	}
	// priority
	if v, ok := set["priority"]; ok {
		if v == nil {
			if t.Priority != 2 {
				t.Priority = 2
				changed["priority"] = true
			}
		} else {
			n, ok := toInt(v)
			if !ok || n < 0 || n > 4 {
				return contract.NewError(contract.ErrInvalidArgument,
					"set.priority must be an integer 0-4.", nil)
			}
			if int(n) != t.Priority {
				t.Priority = int(n)
				changed["priority"] = true
			}
		}
	}
	// array reference fields
	for _, spec := range []struct {
		key string
		get func() *[]string
	}{
		{"tags", func() *[]string { return &t.Tags }},
		{"depends_on", func() *[]string { return &t.DependsOn }},
	} {
		v, ok := set[spec.key]
		if !ok {
			continue
		}
		if v == nil {
			if len(*spec.get()) != 0 {
				*spec.get() = []string{}
				changed[spec.key] = true
			}
			continue
		}
		values, err := toStrings(v, "set."+spec.key)
		if err != nil {
			return err
		}
		if spec.key == "tags" {
			values, err = normalizeTags(values)
			if err != nil {
				return err
			}
		} else {
			for _, ref := range values {
				if _, _, ok := store.ParseID(ref); !ok {
					return contract.NewError(contract.ErrInvalidArgument,
						"Reference "+ref+" is not a valid ticket ID.", nil)
				}
				if ref == full {
					return contract.NewError(contract.ErrInvalidArgument,
						"set."+spec.key+" cannot reference the ticket itself.", nil)
				}
			}
			for _, ref := range values {
				if _, err := st.ResolveID(ref, true); err != nil {
					return err
				}
			}
		}
		values, err = validateUnique(values, spec.key)
		if err != nil {
			return err
		}
		sort.Strings(values)
		cur := *spec.get()
		if !sameStringSlice(cur, values) {
			*spec.get() = values
			changed[spec.key] = true
		}
	}
	// scalar optional fields
	for _, spec := range []struct {
		key string
		get func() *string
	}{
		{"parent", func() *string { return &t.Parent }},
		{"blocked_reason", func() *string { return &t.BlockedReason }},
	} {
		v, ok := set[spec.key]
		if !ok {
			continue
		}
		if v == nil {
			if *spec.get() != "" {
				*spec.get() = ""
				changed[spec.key] = true
			}
			continue
		}
		s, ok := v.(string)
		if !ok {
			return contract.NewError(contract.ErrInvalidArgument,
				"set."+spec.key+" must be a string or null.", nil)
		}
		if spec.key == "parent" && s != "" {
			if _, _, ok := store.ParseID(s); !ok {
				return contract.NewError(contract.ErrInvalidArgument,
					"Reference "+s+" is not a valid ticket ID.", nil)
			}
			if s == full {
				return contract.NewError(contract.ErrInvalidArgument,
					"set.parent cannot reference the ticket itself.", nil)
			}
			if _, err := st.ResolveID(s, true); err != nil {
				return err
			}
		}
		if s != *spec.get() {
			*spec.get() = s
			changed[spec.key] = true
		}
	}
	return nil
}

// ---- body splicing -------------------------------------------------------

type bodyOp struct {
	start, end int
	new        []byte
}

// applyBodyChanges splices title/section changes into the original body
// bytes; all other bytes survive verbatim.
func applyBodyChanges(t *Ticket, opts UpdateOptions, secChanged map[string]bool) ([]byte, error) {
	body := t.Body
	eol := t.LineEnding
	ops := []bodyOp{}

	if v, ok := opts.Set["title"]; ok && v != nil {
		s, _ := v.(string)
		start, end := titleSpan(body)
		cur := strings.TrimPrefix(strings.TrimRight(string(body[start:end]), "\r\n"), "# ")
		if cur != s {
			ops = append(ops, bodyOp{start: start, end: end, new: []byte("# " + s + eol)})
		}
	}

	type secEdit struct {
		key   string
		start int
		end   int
		norm  string
		isNew bool
	}
	var edits []secEdit
	// Process sections in canonical order so insertion positions (which
	// share offsets) produce a deterministic document order.
	secIdx := map[string]int{}
	for i, ks := range markdown.KnownSections {
		secIdx[ks.Key] = i
	}
	keys := make([]string, 0, len(opts.Sections))
	for key := range opts.Sections {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return secIdx[keys[i]] < secIdx[keys[j]] })
	for _, key := range keys {
		content := opts.Sections[key]
		if !knownSectionKey(key) {
			return nil, contract.NewError(contract.ErrInvalidArgument,
				"Unknown section key "+key+" in update sections.", nil)
		}
		if key == "work_log" && !opts.allowWorkLog {
			return nil, contract.NewError(contract.ErrInvalidArgument,
				"Work log is append-only; use --message on a workflow command.", nil)
		}
		norm, err := ValidateSectionContent(key, content)
		if err != nil {
			return nil, err
		}
		norm = toEOL(norm, eol)
		if s, ok := t.Sections[key]; ok {
			if key == "work_log" && s.ContentEnd == len(body) {
				norm = withOneTrailingBlankLine(norm, eol)
			}
			if strings.TrimRight(string(s.Content(body)), "\r\n") != strings.TrimRight(norm, "\r\n") {
				if sectionHeadingEnd(body, s) == s.ContentStart {
					norm = eol + norm
				}
				edits = append(edits, secEdit{key: key, start: s.ContentStart, end: s.ContentEnd, norm: norm})
			}
		} else {
			edits = append(edits, secEdit{key: key, norm: norm, isNew: true})
		}
	}
	// Insertion points for missing sections: before the first known
	// section whose canonical order is greater, else end of body.
	secs := docOrderedSections(t)
	for i := range edits {
		e := &edits[i]
		if !e.isNew {
			insert := e.norm
			if e.end < len(body) {
				insert += eol // blank line before the next heading
			}
			ops = append(ops, bodyOp{start: e.start, end: e.end, new: []byte(insert)})
			secChanged[e.key] = true
			continue
		}
		at := len(body)
		for _, s := range secs {
			if s.Key == "" || s.Key == e.key {
				continue
			}
			if secIdx[s.Key] > secIdx[e.key] && headingOffset(body, s.HeadingLine) < at {
				at = headingOffset(body, s.HeadingLine)
			}
		}
		heading := "## " + knownHeading(e.key) + eol + eol
		if e.key == "work_log" && at == len(body) {
			e.norm = withOneTrailingBlankLine(e.norm, eol)
		}
		insert := heading + e.norm
		if at > 0 {
			before := body[:at]
			switch {
			case bytes.HasSuffix(before, []byte(eol+eol)):
			case bytes.HasSuffix(before, []byte(eol)):
				insert = eol + insert
			default:
				insert = eol + eol + insert
			}
		}
		if at < len(body) {
			insert += eol // blank line before the next heading
		} else {
			if n := len(body); n == 0 || body[n-1] != '\n' {
				insert = eol + insert
			}
		}
		ops = append(ops, bodyOp{start: at, end: at, new: []byte(insert)})
		secChanged[e.key] = true
	}
	return spliceBody(body, ops)
}

func sectionHeadingEnd(body []byte, section markdown.Section) int {
	start := headingOffset(body, section.HeadingLine)
	if start < 0 || start >= len(body) {
		return start
	}
	if rel := bytes.IndexByte(body[start:], '\n'); rel >= 0 {
		return start + rel + 1
	}
	return len(body)
}

func spliceBody(body []byte, ops []bodyOp) ([]byte, error) {
	// Apply non-overlapping ops from the head of the body.
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].start < ops[j].start })
	var out []byte
	pos := 0
	for _, op := range ops {
		if op.start < pos {
			// Overlapping edits cannot be applied without corrupting the
			// document; never claim success for an edit that was not
			// applied.
			return nil, contract.NewError(contract.ErrInternalError,
				"internal error: overlapping body edits", nil)
		}
		out = append(out, body[pos:op.start]...)
		out = append(out, op.new...)
		pos = op.end
	}
	return append(out, body[pos:]...), nil
}

// titleSpan returns the byte span of the first nonblank body line, which
// must be the H1 title line.
func titleSpan(body []byte) (int, int) {
	offset := 0
	rest := body
	for len(rest) > 0 {
		nl := bytes.IndexByte(rest, '\n')
		var line []byte
		if nl < 0 {
			line = rest
		} else {
			line = rest[:nl+1]
		}
		if strings.TrimSpace(string(line)) != "" {
			return offset, offset + len(line)
		}
		if nl < 0 {
			break
		}
		rest = rest[nl+1:]
		offset = offset + nl + 1
	}
	return 0, 0
}

func docOrderedSections(t *Ticket) []*markdown.Section {
	out := []*markdown.Section{}
	for _, s := range t.Sections {
		out = append(out, &s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].HeadingLine < out[j].HeadingLine })
	return out
}

// headingOffset returns the byte offset of the first byte of the given
// 1-based line in the body.
func headingOffset(body []byte, line int) int {
	rest := body
	for i := 1; i < line; i++ {
		idx := bytes.IndexByte(rest, '\n')
		if idx < 0 {
			return len(body)
		}
		rest = rest[idx+1:]
	}
	return len(body) - len(rest)
}

func toEOL(s, eol string) string {
	if eol == "\n" {
		return s
	}
	return strings.ReplaceAll(s, "\n", eol)
}

// withOneTrailingBlankLine canonicalizes the end of a section at EOF. The
// final populated line is followed by exactly one empty line, regardless of
// how the user-authored document was terminated before the append.
func withOneTrailingBlankLine(s, eol string) string {
	return strings.TrimRight(s, "\r\n") + eol + eol
}

func knownSectionKey(key string) bool {
	for _, ks := range markdown.KnownSections {
		if ks.Key == key {
			return true
		}
	}
	return false
}

func knownHeading(key string) string {
	for _, ks := range markdown.KnownSections {
		if ks.Key == key {
			return ks.Heading
		}
	}
	return ""
}

// ---- metadata rendering ---------------------------------------------------

// renderUpdated emits the visible metadata block and keeps the rest of the
// parsed Markdown body byte-for-byte where possible.
func renderUpdated(t *Ticket, newBody []byte, setChanged map[string]bool) ([]byte, error) {
	if t.Metadata == nil || t.Body == nil || t.FileBytes == nil {
		return nil, contract.NewError(contract.ErrInternalError,
			"Ticket bytes are missing; cannot render update.", nil)
	}
	eol := t.LineEnding
	parsed := markdown.ParseBody(newBody)
	titleStart, titleEnd := titleSpan(newBody)
	if titleEnd <= titleStart {
		return nil, contract.NewError(contract.ErrInvalidTicket,
			"TASK.md title cannot be located; refusing to rewrite.", nil)
	}
	metadataEnd := titleEnd
	if parsed.MetadataEnd > metadataEnd {
		metadataEnd = parsed.MetadataEnd
	}
	metadata := renderVisibleMetadata(t, eol)
	var out []byte
	out = append(out, newBody[titleStart:titleEnd]...)
	out = append(out, []byte(eol)...)
	out = append(out, []byte(metadata)...)
	out = append(out, []byte(eol)...)
	out = append(out, newBody[metadataEnd:]...)
	return out, nil
}

func renderVisibleMetadata(t *Ticket, eol string) string {
	var b strings.Builder
	renderMetadata(&b, t.State, t.Priority, t.Tags, t.Parent, t.DependsOn, t.Assignee, t.BlockedReason)
	if t.Metadata != nil {
		known := map[string]bool{"state": true, "priority": true, "tags": true, "parent": true, "depends_on": true, "assignee": true, "blocked_reason": true}
		for _, entry := range t.Metadata.Entries {
			if !known[entry.Key] {
				raw := strings.TrimRight(strings.ReplaceAll(entry.Raw, "\r\n", "\n"), "\n")
				if raw != "" {
					b.WriteString(raw)
					b.WriteString("\n")
				}
			}
		}
	}
	return strings.ReplaceAll(b.String(), "\n", eol)
}

func canonicalLine(t *Ticket, key string, value any, eol string) (line string, removed bool) {
	switch key {
	case "priority":
		n, _ := toInt(value)
		if n == 2 {
			return "", true
		}
		return "priority: " + strconv.Itoa(int(n)), false
	case "tags":
		s, _ := value.([]string)
		if len(s) == 0 {
			return "", true
		}
		return "tags: [" + joinQuoted(s) + "]", false
	case "depends_on":
		s, _ := value.([]string)
		if len(s) == 0 {
			return "", true
		}
		return "depends_on: [" + joinQuoted(s) + "]", false
	case "parent":
		s, _ := value.(string)
		if s == "" {
			return "", true
		}
		return "parent: " + quoteMetaString(s), false
	case "blocked_reason":
		s, _ := value.(string)
		if s == "" {
			return "", true
		}
		return "blocked_reason: " + quoteMetaString(s), false
	case "assignee":
		s, _ := value.(string)
		if s == "" {
			return "", true
		}
		return "assignee: " + quoteMetaString(s), false
	case "state":
		s, _ := value.(string)
		if s == "" {
			return "", true
		}
		return "state: " + s, false
	}
	return "", true
}

func quoteMetaString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func joinQuoted(s []string) string {
	q := make([]string, len(s))
	for i, v := range s {
		q[i] = quoteMetaString(v)
	}
	return strings.Join(q, ", ")
}

func fieldValue(t *Ticket, key string) (any, bool) {
	switch key {
	case "priority":
		return t.Priority, true
	case "tags":
		return t.Tags, true
	case "depends_on":
		return t.DependsOn, true
	case "parent":
		return t.Parent, true
	case "blocked_reason":
		return t.BlockedReason, true
	case "assignee":
		return t.Assignee, true
	case "state":
		return t.State, true
	}
	return nil, false
}

func canonicalRank(key string) int {
	for i, k := range canonicalFieldOrder {
		if k == key {
			return i
		}
	}
	return len(canonicalFieldOrder)
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func toStrings(v any, field string) ([]string, error) {
	if raw, ok := v.([]string); ok {
		return append([]string(nil), raw...), nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			field+" must be an array of strings.", nil)
	}
	out := []string{}
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, contract.NewError(contract.ErrInvalidArgument,
				field+" values must be strings.", nil)
		}
		out = append(out, s)
	}
	return out, nil
}

func validateTitle(title string) error {
	if strings.TrimSpace(title) == "" {
		return contract.NewError(contract.ErrInvalidArgument, "A nonempty title is required.", nil)
	}
	if len([]rune(title)) > 240 {
		return contract.NewError(contract.ErrInvalidArgument, "The title exceeds 240 characters.", nil)
	}
	if strings.ContainsAny(title, "\n\r") {
		return contract.NewError(contract.ErrInvalidArgument, "The title must not contain line breaks.", nil)
	}
	return nil
}
