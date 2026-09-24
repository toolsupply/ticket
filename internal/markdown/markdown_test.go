package markdown

import (
	"strings"
	"testing"
)

// hasFMError reports whether an Metadata contains an error diagnostic.
func hasFMError(f *Metadata) bool {
	for _, d := range f.Diagnostics {
		if d.IsError() {
			return true
		}
	}
	return false
}

// hasError reports whether diags contains an error-severity diagnostic.
func hasError(b *Body) bool {
	for _, d := range b.Diagnostics {
		if d.IsError() {
			return true
		}
	}
	return false
}

func hasWarning(b *Body, code string) bool {
	for _, d := range b.Diagnostics {
		if d.Severity == "warning" && d.Code == code {
			return true
		}
	}
	return false
}

// S12: title extraction and ATX rules.
func TestParseBodyTitles(t *testing.T) {
	b := ParseBody([]byte("# Fix login\n\n## Objective\nDo it.\n"))
	if b.Title != "Fix login" {
		t.Fatalf("title=%q", b.Title)
	}
	if len(b.Diagnostics) != 0 {
		t.Fatalf("diags=%v", b.Diagnostics)
	}
	// Non-ASCII title preserved.
	b = ParseBody([]byte("# 修正ログインフロー\n\n## Objective\n\n"))
	if b.Title != "修正ログインフロー" {
		t.Fatalf("non-ascii title=%q", b.Title)
	}
	// C++ heading: closing hashes are optional; C++ is not a heading.
	b = ParseBody([]byte("# T\n\n## C++\n\n## Objective\nx\n"))
	found := false
	for _, s := range b.Sections {
		if s.Heading == "C++" {
			found = true
		}
	}
	if !found {
		t.Fatalf("C++ section missing: %v", b.Sections)
	}
	if hasError(b) {
		t.Fatalf("unexpected error: %v", b.Diagnostics)
	}
	// F4: a trailing # in ATX text is text, not a closing sequence.
	b = ParseBody([]byte("# Fix C#\n\n## Objective\nx\n"))
	if b.Title != "Fix C#" {
		t.Fatalf("title=%q", b.Title)
	}
	// "##no-space" is not a heading: content.
	b = ParseBody([]byte("# T\n\n##no-space\n"))
	if b.Title != "T" {
		t.Fatalf("title=%q", b.Title)
	}
	for _, s := range b.Sections {
		if s.Heading == "no-space" {
			t.Fatalf("##no-space treated as heading")
		}
	}
}

// S12: fence-aware heading scan.
func TestParseBodyFences(t *testing.T) {
	body := "# T\n\n```\n## Not a heading\n```\n\n## Objective\nreal\n"
	b := ParseBody([]byte(body))
	if b.Title != "T" {
		t.Fatalf("title=%q", b.Title)
	}
	var objective *Section
	for i := range b.Sections {
		if b.Sections[i].Heading == "Objective" {
			objective = &b.Sections[i]
		}
	}
	if objective == nil {
		t.Fatalf("sections=%v", b.Sections)
	}
	if got := objective.DisplayContent([]byte(body)); got != "real" {
		t.Fatalf("content=%q", got)
	}
	// Indented fence inside body: still a fence (up to 3 spaces).
	body2 := "# T\n\n   ```\n# nope\n   ```\n"
	b = ParseBody([]byte(body2))
	for _, d := range b.Diagnostics {
		if d.Severity == "error" {
			t.Fatalf("unexpected error: %v", d)
		}
	}
}

// Duplicate known sections are a warning; the first section remains usable.
func TestParseBodyDuplicateSection(t *testing.T) {
	b := ParseBody([]byte("# T\n\n## Objective\na\n\n## Objective\nb\n"))
	if hasError(b) || !hasWarning(b, "duplicate_section") {
		t.Fatalf("expected duplicate section warning: %v", b.Diagnostics)
	}
}

// S10: duplicate H1 is an error; missing title is an error.
func TestParseBodyH1Rules(t *testing.T) {
	b := ParseBody([]byte("# A\n\n# B\n"))
	if !hasError(b) {
		t.Fatal("expected second H1 error")
	}
	b = ParseBody([]byte("\n\nno heading here\n"))
	if !hasError(b) {
		t.Fatal("expected missing H1 error")
	}
}

// S11: metadata restrictions.
func TestMetadataRestrictions(t *testing.T) {
	if _, _, _, diags := SplitMetadata([]byte("not metadata\n")); len(diags) != 1 || diags[0].Message != "TASK.md must start with a metadata block." {
		t.Fatalf("metadata start diagnostic=%v", diags)
	}
	if _, _, _, diags := SplitMetadata([]byte("---\npriority: 1\n")); len(diags) != 1 || diags[0].Message != "The metadata block is not terminated by ---." {
		t.Fatalf("metadata termination diagnostic=%v", diags)
	}

	// Duplicate key.
	fm, _, _, sd := SplitMetadata([]byte("---\na: 1\na: 2\n---\n"))
	if len(sd) != 0 {
		t.Fatalf("split diags: %v", sd)
	}
	f := ParseMetadata(fm)
	if !hasFMError(f) {
		t.Fatalf("expected duplicate key diagnostic, got %v", f.Diagnostics)
	}

	// Quoted strings are valid and preserve their decoded value.
	fm, _, _, _ = SplitMetadata([]byte("---\ncustom_at: '2026-01-01T00:00:00Z'\n---\n"))
	f = ParseMetadata(fm)
	if got, ok := f.Get("custom_at"); !ok || got != "2026-01-01T00:00:00Z" {
		t.Fatal("quoted value should parse")
	}
	if hasFMError(f) {
		t.Fatalf("quoted timestamp rejected: %v", f.Diagnostics)
	}
	// Unquoted date-looking values remain strings; there is no implicit date
	// coercion in the application-owned metadata format.
	fm, _, _, _ = SplitMetadata([]byte("---\ncustom_at: 2026-01-01T00:00:00Z\n---\n"))
	f = ParseMetadata(fm)
	if got, ok := f.Get("custom_at"); !ok || got != "2026-01-01T00:00:00Z" || hasFMError(f) {
		t.Fatalf("unquoted timestamp should remain a string: value=%v diagnostics=%v", got, f.Diagnostics)
	}

	// Aliases/anchors rejected.
	fm, _, _, _ = SplitMetadata([]byte("---\na: &x 1\nb: *x\n---\n"))
	f = ParseMetadata(fm)
	ok := false
	for _, d := range f.Diagnostics {
		if d.Severity == "error" {
			ok = true
		}
	}
	if !ok {
		t.Fatal("anchor/alias must be rejected")
	}

	// Merge key rejected.
	fm, _, _, _ = SplitMetadata([]byte("---\na: 1\n<<: {b: 2}\n---\n"))
	f = ParseMetadata(fm)
	ok = false
	for _, d := range f.Diagnostics {
		if d.Severity == "error" {
			ok = true
		}
	}
	if !ok {
		t.Fatal("merge key must be rejected")
	}

	// The parser owns only integer scalars and flat string lists.
	fm, _, _, _ = SplitMetadata([]byte("---\npriority: 1.5\ntags: [bug, parser]\n---\n"))
	f = ParseMetadata(fm)
	if got, ok := f.Get("tags"); !ok || len(got.([]string)) != 2 {
		t.Fatalf("flat string list did not parse: %v", f)
	}
	if _, ok := f.Get("priority"); !ok {
		t.Fatal("fractional scalar should remain available for field validation")
	}
	fm, _, _, _ = SplitMetadata([]byte("---\nnested: {key: value}\n---\n"))
	f = ParseMetadata(fm)
	if !hasFMError(f) {
		t.Fatalf("nested mapping should be rejected: %v", f.Diagnostics)
	}
}

// Section content span: exactly one trailing newline stripped.
func TestSectionContentSpan(t *testing.T) {
	body := "# T\n\n## Objective\nline1\nline2\n\n## Acceptance\n- [ ] one\n"
	b := ParseBody([]byte(body))
	if b.Title != "T" {
		t.Fatalf("title=%q", b.Title)
	}
	want := map[string]string{
		// DisplayContent strips exactly one trailing newline.
		"Objective":  "line1\nline2\n",
		"Acceptance": "- [ ] one",
	}
	for _, sec := range b.Sections {
		got := sec.DisplayContent([]byte(body))
		if got != want[sec.Heading] {
			t.Fatalf("section %s content=%q want %q", sec.Heading, got, want[sec.Heading])
		}
	}
	// Raw content keeps the trailing newline between sections.
	for _, sec := range b.Sections {
		if sec.Heading == "Objective" {
			if raw := sec.Content([]byte(body)); raw != "line1\nline2\n\n" {
				t.Fatalf("raw content=%q", raw)
			}
		}
	}
}

// Fence with backtick run inside: fence char run rules.
func TestFenceRunRules(t *testing.T) {
	body := "# T\n\n~~~\n## x\n```\n~~~\n\n## Objective\nok\n"
	b := ParseBody([]byte(body))
	found := false
	for _, s := range b.Sections {
		if s.Heading == "Objective" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sections=%v", b.Sections)
	}
	if strings.Contains(strings.Join([]string{b.Title}, ""), "x") {
		t.Fatal("sanity")
	}
}

// Setext headings are outside the supported structural syntax.
func TestParseBodySetextHeadings(t *testing.T) {
	body := "# T\n\n" +
		"Intro.\n\n" +
		"Objective\n---\n\nDo this.\n\n" +
		"Notes\n--\n\nMore.\n"
	b := ParseBody([]byte("Title\n=====\n"))
	if !hasError(b) {
		t.Fatalf("setext title should fail: %v", b.Diagnostics)
	}
	b = ParseBody([]byte(body))
	if hasError(b) {
		t.Fatalf("setext lines in a body should remain ordinary content: %v", b.Diagnostics)
	}
	if len(b.Sections) != 0 {
		t.Fatalf("setext lines should remain ordinary content: %v", b.Sections)
	}
}

// F4: CRLF bodies keep original-source coordinates for section spans.
func TestParseBodyCRLF(t *testing.T) {
	body := "# T\r\n\r\n## Objective\r\nline1\r\nline2\r\n\r\n## Acceptance\r\n- [ ] one\r\n"
	b := ParseBody([]byte(body))
	if len(b.Diagnostics) != 0 {
		t.Fatalf("diags=%v", b.Diagnostics)
	}
	if b.Title != "T" {
		t.Fatalf("title=%q", b.Title)
	}
	if len(b.Sections) != 2 {
		t.Fatalf("sections=%v", b.Sections)
	}
	raw := map[string]string{}
	for _, s := range b.Sections {
		raw[s.Heading] = s.Content([]byte(body))
	}
	if raw["Objective"] != "line1\r\nline2\r\n\r\n" {
		t.Fatalf("objective raw=%q", raw["Objective"])
	}
	if raw["Acceptance"] != "- [ ] one\r\n" {
		t.Fatalf("acceptance raw=%q", raw["Acceptance"])
	}
}

// ContainsTopLevelHeading uses the same ATX/fence scanner as ParseBody.
func TestContainsTopLevelHeading(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"atx h1", "# T\n", true},
		{"atx h2", "## X\n", true},
		{"trailing atx hashes", "## X ##\n", true},
		{"setext h1", "T\n===\n", false},
		{"setext h2", "X\n--\n", false},
		{"h3 not counted", "### X\n", false},
		{"plain text", "just text\n", false},
		{"fenced backtick", "```\n# X\n```\n", false},
		{"fenced tilde", "~~~\n## X\n~~~\n", false},
		{"fenced long run", "````\n# X\n````\n", false},
		{"fence mismatched closer", "```\n# X\n~~~\n", false},
	}
	for _, c := range cases {
		if got := ContainsTopLevelHeading(c.text); got != c.want {
			t.Fatalf("%s: got=%v want=%v", c.name, got, c.want)
		}
	}
}

// F4: duplicate known sections are detected on the canonical key, not
// on raw heading text, so mixed-case duplicates are warnings.
func TestParseBodyDuplicateSectionMixedCase(t *testing.T) {
	b := ParseBody([]byte("# T\n\n## Acceptance\na\n\n## acceptance\nb\n"))
	if hasError(b) || !hasWarning(b, "duplicate_section") {
		t.Fatalf("expected nonfatal duplicate section warning: %v", b.Diagnostics)
	}
	dup := false
	for _, d := range b.Diagnostics {
		if d.Code == "duplicate_section" {
			dup = true
		}
	}
	if !dup {
		t.Fatalf("no duplicate_section diagnostic: %v", b.Diagnostics)
	}
	// Same-case duplicates are also nonfatal and retain the first section.
	b = ParseBody([]byte("# T\n\n## Objective\na\n\n## Objective\nb\n"))
	if hasError(b) || !hasWarning(b, "duplicate_section") {
		t.Fatalf("expected duplicate section warning: %v", b.Diagnostics)
	}
}
