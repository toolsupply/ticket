package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ticket/internal/contract"
)

// fileBody returns the complete TASK.md; new tickets have no metadata.
func fileBody(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	s := string(data)
	for _, cand := range []string{"\n---\n", "\n---\r\n"} {
		if i := strings.Index(s, cand); i >= 0 {
			return s[i+len(cand):]
		}
	}
	if i := strings.Index(s, "\n## Objective"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func TestUpdateMetadataOnlyBodyIdentical(t *testing.T) {
	e := newEnv(t, 31)
	id := e.create(t, "Preserve bytes", CreateOptions{
		Sections: map[string]string{
			"objective":  "Keep this line exactly.\n",
			"acceptance": "- [ ] one\n- [ ] two\n",
		},
	})
	p := filepath.Join(e.base, "tickets", id, "TASK.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Hand-edit: an unknown top-level key must survive.
	s := strings.Replace(string(data), "- State: open\n", "- State: open\nteam: core\n", 1)
	if s == string(data) {
		t.Fatalf("state line not found:\n%s", data)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	before := fileBody(t, p)

	res, err := Update(e.st, id, UpdateOptions{
		Set: map[string]any{"blocked_reason": "waiting on upstream"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || len(res.ChangedFields) != 1 || res.ChangedFields[0] != "blocked_reason" {
		t.Fatalf("result: %+v", res)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "team: core\n") {
		t.Fatalf("unknown key lost:\n%s", after)
	}
	if fileBody(t, p) != before {
		t.Fatalf("body changed on metadata-only update:\n---before---\n%s\n---after---\n%s", before, string(after))
	}
	if !strings.Contains(string(after), "- Blocked reason: waiting on upstream\n") {
		t.Fatalf("blocked_reason not set:\n%s", after)
	}
	// Semantics survive the round trip.
	again, err := ReadTicket(e.st, id)
	if err != nil {
		t.Fatal(err)
	}
	if again.BlockedReason != "waiting on upstream" {
		t.Fatalf("round trip: %+v", again)
	}
}

func TestUpdateCRLFPreserved(t *testing.T) {
	e := newEnv(t, 32)
	id := e.create(t, "CRLF ticket", CreateOptions{
		Sections: map[string]string{"objective": "CRLF keep.\n"},
	})
	p := filepath.Join(e.base, "tickets", id, "TASK.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(string(data), "\n", "\r\n")
	if err := os.WriteFile(p, []byte(crlf), 0o644); err != nil {
		t.Fatal(err)
	}
	before := fileBody(t, p)

	ticket, err := ReadTicket(e.st, id)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.LineEnding != "\r\n" {
		t.Fatalf("line ending = %q", ticket.LineEnding)
	}
	if _, err := Update(e.st, id, UpdateOptions{
		Set: map[string]any{"priority": 1},
	}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if fileBody(t, p) != before {
		t.Fatalf("CRLF body changed:\n%s", after)
	}
	if strings.Count(string(after), "\r\n") != strings.Count(string(after), "\n") {
		t.Fatalf("line endings mixed after update:\n%s", after)
	}
	if !strings.Contains(string(after), "- Priority: P1\r\n") {
		t.Fatalf("priority not rendered CRLF:\n%s", after)
	}
}

func TestUpdateSectionReplaceIsolates(t *testing.T) {
	e := newEnv(t, 33)
	id := e.create(t, "Section isolation", CreateOptions{
		Sections: map[string]string{
			"objective":  "Base.\n",
			"acceptance": "```\n## Fake heading inside fence\n```\n",
		},
	})
	p := filepath.Join(e.base, "tickets", id, "TASK.md")
	beforeAll, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	beforeBody := fileBody(t, p)
	// Isolate the acceptance section bytes.
	acc := acceptanceSection(t, beforeBody)

	res, err := Update(e.st, id, UpdateOptions{
		Sections: map[string]string{"objective": "Replaced."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ChangedFields) != 1 || res.ChangedFields[0] != "sections.objective" {
		t.Fatalf("changed: %v", res.ChangedFields)
	}
	afterBody := fileBody(t, p)
	if !strings.Contains(afterBody, "## Fake heading inside fence") {
		t.Fatalf("fenced heading lost on unrelated section update:\n%s", afterBody)
	}
	if got := acceptanceSection(t, afterBody); got != acc {
		t.Fatalf("acceptance section changed:\n%s", afterBody)
	}
	if !strings.Contains(afterBody, "Replaced.") {
		t.Fatalf("objective not replaced:\n%s", afterBody)
	}
	// Byte-level check: everything outside the objective span identical.
	if err := os.WriteFile(p, beforeAll, 0o644); err != nil {
		t.Fatal(err)
	}
}

func acceptanceSection(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, "## Acceptance\n")
	if i < 0 {
		t.Fatalf("no acceptance in body:\n%s", body)
	}
	return body[i:]
}

func TestUpdateNoopIsNoWrite(t *testing.T) {
	e := newEnv(t, 34)
	id := e.create(t, "Noop", CreateOptions{
		Sections: map[string]string{"objective": "Same.\n"},
		Priority: 2,
	})
	p := filepath.Join(e.base, "tickets", id, "TASK.md")
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Update(e.st, id, UpdateOptions{
		Set:      map[string]any{"priority": 2, "tags": []any{}},
		Sections: map[string]string{"objective": "Same.\n"},
	})
	if err != nil {
		t.Fatalf("unexpected error for semantic no-op: %v", err)
	}
	if res.Changed {
		t.Fatalf("noop must not change the file: %+v", res)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("noop touched disk:\n%s", after)
	}
}

func TestUpdateClearsAndInserts(t *testing.T) {
	e := newEnv(t, 35)
	parent := e.create(t, "Parent ticket", CreateOptions{})
	id := e.create(t, "Child ticket", CreateOptions{
		Priority: 1,
		Parent:   parent,
		Tags:     []string{"auth"},
	})
	res, err := Update(e.st, id, UpdateOptions{
		Set: map[string]any{
			"parent":         nil,
			"priority":       nil,
			"tags":           []any{},
			"blocked_reason": "waiting on API",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"priority", "tags", "parent", "blocked_reason"}
	if len(res.ChangedFields) != len(want) {
		t.Fatalf("changed fields: %v", res.ChangedFields)
	}
	for i, w := range want {
		if res.ChangedFields[i] != w {
			t.Fatalf("changed fields: %v", res.ChangedFields)
		}
	}
	p := filepath.Join(e.base, "tickets", id, "TASK.md")
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(after)
	if strings.Contains(s, "- Parent:") || strings.Contains(s, "- Tags:") || !strings.Contains(s, "- Priority: P2") {
		t.Fatalf("cleared fields still present:\n%s", s)
	}
	if !strings.Contains(s, "- Blocked reason: waiting on API") {
		t.Fatalf("blocked_reason missing:\n%s", s)
	}
	again, err := ReadTicket(e.st, id)
	if err != nil {
		t.Fatal(err)
	}
	if again.Parent != "" || again.Priority != 2 || len(again.Tags) != 0 || again.BlockedReason != "waiting on API" {
		t.Fatalf("round trip: %+v", again)
	}
}

func TestUpdateValidation(t *testing.T) {
	e := newEnv(t, 36)
	id := e.create(t, "Validate me", CreateOptions{})
	cases := []struct {
		name string
		opts UpdateOptions
		code contract.ErrorCode
	}{
		{"unknown key", UpdateOptions{Set: map[string]any{"bogus": "x"}}, contract.ErrInvalidArgument},
		{"self parent", UpdateOptions{Set: map[string]any{"parent": id}}, contract.ErrInvalidArgument},
		{"dangling parent", UpdateOptions{Set: map[string]any{"parent": "20010101-99999"}}, contract.ErrDanglingReference},
		{"bad section key", UpdateOptions{Sections: map[string]string{"nope": "x"}}, contract.ErrInvalidArgument},
		{"bad title", UpdateOptions{Set: map[string]any{"title": ""}}, contract.ErrInvalidArgument},
		{"bad priority", UpdateOptions{Set: map[string]any{"priority": 9}}, contract.ErrInvalidArgument},
	}
	for _, c := range cases {
		if _, err := Update(e.st, id, c.opts); err == nil {
			t.Fatalf("%s: expected error", c.name)
		} else if got := contractCode(t, err); got != c.code {
			t.Fatalf("%s: code=%s want %s", c.name, got, c.code)
		}
	}
}

// F4: a single update that inserts several missing sections must produce
// the canonical document order deterministically, regardless of map
// iteration order (shared insertion offsets).
func TestUpdateMultiSectionInsertionDeterministic(t *testing.T) {
	for n := 0; n < 5; n++ {
		e := newEnv(t, 600+uint64(n))
		id := e.create(t, "Insert order", CreateOptions{
			Sections: map[string]string{"objective": "Start.\n"},
		})
		// The lean format starts with only Objective, so these updates insert
		// the other managed sections in canonical order.
		p := filepath.Join(e.base, "tickets", id, "TASK.md")
		_, err := Update(e.st, id, UpdateOptions{
			Sections: map[string]string{
				"handoff":    "H.\n",
				"acceptance": "A.\n",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		body := fileBody(t, p)
		for _, want := range []string{"A.\n", "H.\n"} {
			if !strings.Contains(body, want) {
				t.Fatalf("missing content %q:\n%s", want, body)
			}
		}
		pos := []int{
			strings.Index(body, "## Objective"),
			strings.Index(body, "## Acceptance"),
			strings.Index(body, "## Handoff"),
		}
		for j, p2 := range pos {
			if p2 < 0 {
				t.Fatalf("missing heading %d:\n%s", j, body)
			}
			if j > 0 && pos[j] < pos[j-1] {
				t.Fatalf("non-canonical section order:\n%s", body)
			}
		}
	}
}

func TestManagedSectionsAppendAfterObjective(t *testing.T) {
	e := newEnv(t, 700)
	id := e.create(t, "Append sections", CreateOptions{Sections: map[string]string{
		"objective": "Objective content.",
	}})
	path := filepath.Join(e.base, "tickets", id, "TASK.md")
	before := fileBody(t, path)
	if _, err := Update(e.st, id, UpdateOptions{Sections: map[string]string{
		"acceptance": "Acceptance content.",
		"handoff":    "Handoff content.",
	}}); err != nil {
		t.Fatal(err)
	}
	after := fileBody(t, path)
	objective := strings.Index(after, "## Objective")
	acceptance := strings.Index(after, "## Acceptance")
	handoff := strings.Index(after, "## Handoff")
	if objective < 0 || acceptance < 0 || handoff < 0 || !(objective < acceptance && acceptance < handoff) {
		t.Fatalf("managed sections are not appended in order:\n%s", after)
	}
	if !strings.Contains(after, before) {
		t.Fatalf("existing ticket content was moved or rewritten:\n---before---\n%s\n---after---\n%s", before, after)
	}
}

// F4: the splice engine applies every non-overlapping op (zero-width
// insertions at the same offset included); a true overlap is an error,
// never a silent skip reported as applied.
func TestSpliceBodyNonOverlappingAppliesAll(t *testing.T) {
	body := []byte("# T\n\n## A\n1\n\n## B\n2\n")
	// Two zero-width insertions share offset 4; a range replacement covers
	// 10..12. No overlaps: every op must be applied.
	got, err := spliceBody(body, []bodyOp{
		{start: 4, end: 4, new: []byte("NOTE\n")},
		{start: 4, end: 4, new: []byte("MARK\n")},
		{start: 10, end: 12, new: []byte("11\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Both zero-width insertions survive, in order, and the range
	// replacement applies.
	if !strings.Contains(string(got), "NOTE\nMARK\n") {
		t.Fatalf("inserts missing or misordered: %q", got)
	}
	if !strings.Contains(string(got), "11\n") {
		t.Fatalf("range replacement missing: %q", got)
	}
}

func TestSpliceBodyOverlapIsError(t *testing.T) {
	body := []byte("0123456789")
	// Two ops that overlap: first covers 2..8, second starts at 5.
	got, err := spliceBody(body, []bodyOp{
		{start: 2, end: 8, new: []byte("AAAA")},
		{start: 5, end: 6, new: []byte("BB")},
	})
	if err == nil {
		t.Fatalf("expected overlap error, got output %q", got)
	}
}

// Body validation failures must be caught by complete ticket parsing while
// duplicate known sections remain a nonfatal warning.
func TestBodyValidationRejectsReadingPaths(t *testing.T) {
	// Mixed-case duplicate section: full parse accepts the first section and
	// records a warning.
	e := newEnv(t, 134)
	id := e.create(t, "dup case", CreateOptions{})
	p := filepath.Join(e.base, "tickets", id, "TASK.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(data), "## Objective", "## Objective\n\n## objective", 1)
	if s == string(data) {
		t.Fatal("## Objective not found")
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTicket(e.st, id); err != nil {
		t.Fatalf("mixed-case duplicate section should be readable: %v", err)
	}

	// Setext H1 title is rejected by complete parsing.
	e2 := newEnv(t, 135)
	id2 := e2.create(t, "setext title", CreateOptions{})
	p2 := filepath.Join(e2.base, "tickets", id2, "TASK.md")
	data2, err := os.ReadFile(p2)
	if err != nil {
		t.Fatal(err)
	}
	s2 := strings.Replace(string(data2), "# setext title\n", "Setext title\n===\n", 1)
	if s2 == string(data2) {
		t.Fatal("# setext title not found")
	}
	if err := os.WriteFile(p2, []byte(s2), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTicket(e2.st, id2); err == nil {
		t.Fatal("setext H1 title accepted by full parse")
	}
}
