package domain

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/store"
	"github.com/toolsupply/ticket/internal/testutil"
)

// testEnv opens a repository with deterministic IDs.
type testEnv struct {
	st   *store.Store
	src  *testutil.DeterministicSource
	base string
	t    *testing.T
}

func newEnv(t *testing.T, seed uint64) *testEnv {
	base := t.TempDir()
	if _, err := store.InitRoot(filepath.Join(base, "tickets")); err != nil {
		t.Fatalf("init: %v", err)
	}
	src := testutil.NewDeterministicSource(seed)
	st, err := store.Open(base, store.OpenOptions{IDSource: src})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	return &testEnv{st: st, src: src, base: base, t: t}
}

func (e *testEnv) create(t *testing.T, title string, opts CreateOptions) string {
	opts.Title = title
	res, err := Create(e.st, opts)
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	return res.ID
}

func contractCode(t *testing.T, err error) contract.ErrorCode {
	t.Helper()
	var ce *contract.Error
	if !errors.As(err, &ce) {
		t.Fatalf("not a contract error: %v", err)
	}
	return ce.Code
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t, 11)
	cases := []struct {
		name string
		opts CreateOptions
		code contract.ErrorCode
	}{
		{"empty title", CreateOptions{Title: ""}, contract.ErrInvalidArgument},
		{"long title", CreateOptions{Title: strings.Repeat("x", 241)}, contract.ErrInvalidArgument},
		{"newline title", CreateOptions{Title: "a\nb"}, contract.ErrInvalidArgument},
		{"bad tag", CreateOptions{Title: "t", Tags: []string{"Bad Tag"}}, contract.ErrInvalidArgument},
		{"long tag", CreateOptions{Title: "t", Tags: []string{strings.Repeat("a", 65)}}, contract.ErrInvalidArgument},
		{"dup tag", CreateOptions{Title: "t", Tags: []string{"a", "a"}}, contract.ErrInvalidArgument},
		{"priority low", CreateOptions{Title: "t", Priority: -1}, contract.ErrInvalidArgument},
		{"priority high", CreateOptions{Title: "t", Priority: 5}, contract.ErrInvalidArgument},
		{"unknown section", CreateOptions{Title: "t", Sections: map[string]string{"nope": "x"}}, contract.ErrInvalidArgument},
		{"outcome section", CreateOptions{Title: "t", Sections: map[string]string{"outcome": "x"}}, contract.ErrInvalidArgument},
	}
	for _, c := range cases {
		if _, err := Create(e.st, c.opts); err == nil {
			t.Fatalf("%s: expected error", c.name)
		} else if got := contractCode(t, err); got != c.code {
			t.Fatalf("%s: code=%s want %s", c.name, got, c.code)
		}
	}
	// Dangling reference.
	if _, err := Create(e.st, CreateOptions{Title: "t", Parent: "tk_" + strings.Repeat("a", 26)}); err == nil {
		t.Fatal("expected dangling_reference")
	}
}

func TestManagedHeadingsHaveBlankLines(t *testing.T) {
	e := newEnv(t, 19)
	id := e.create(t, "Spacing", CreateOptions{Sections: map[string]string{
		"objective": "Do the work.", "acceptance": "It works.", "handoff": "Continue here.",
	}})
	path := filepath.Join(e.base, "tickets", id, "TASK.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, heading := range []string{"# Spacing", "## Objective", "## Acceptance", "## Handoff"} {
		if !bytes.Contains(data, []byte(heading+"\n\n")) {
			t.Fatalf("%q is not followed by a blank line:\n%s", heading, data)
		}
	}

	// A ticket without the separator is repaired when its managed
	// section is edited; unrelated bytes remain outside the splice.
	data = bytes.Replace(data, []byte("## Objective\n\nDo the work.\n"), []byte("## Objective\nDo the work.\n"), 1)
	marker := []byte("custom: keep this text\n")
	data = append(data, marker...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(e.st, id, UpdateOptions{Sections: map[string]string{"objective": "Updated."}}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("## Objective\n\nUpdated.\n")) {
		t.Fatalf("edited heading is not followed by a blank line:\n%s", data)
	}
	if !bytes.Contains(data, marker) {
		t.Fatalf("unrelated content was not preserved:\n%s", data)
	}

	// Adding a missing section also separates it from the preceding content.
	id = e.create(t, "Inserted section", CreateOptions{Sections: map[string]string{
		"objective": "Do this first.",
	}})
	if _, err := Update(e.st, id, UpdateOptions{Sections: map[string]string{
		"acceptance": "It works.",
	}}); err != nil {
		t.Fatalf("add section: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(e.base, "tickets", id, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("Do this first.\n\n## Acceptance\n\nIt works.\n")) {
		t.Fatalf("inserted heading is not preceded by a blank line:\n%s", data)
	}
}

func TestToIntAcceptsOnlyFiniteWholeValues(t *testing.T) {
	tests := []struct {
		name  string
		value any
		ok    bool
	}{
		{"integer", float64(1), true},
		{"whole float", 1.0, true},
		{"fraction", 1.9, false},
		{"nan", math.NaN(), false},
		{"positive infinity", math.Inf(1), false},
		{"negative infinity", math.Inf(-1), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toInt(tt.value)
			if ok != tt.ok || (ok && got != 1) {
				t.Fatalf("toInt(%v) = %d, %v; want ok=%v", tt.value, got, ok, tt.ok)
			}
		})
	}
}

func TestCreateSuccessAndFields(t *testing.T) {
	e := newEnv(t, 21)
	id := e.create(t, "Fix login", CreateOptions{
		Priority: 1,
		Tags:     []string{"auth", "p1"},
		Sections: map[string]string{
			"objective":  "Log in reliably.\n",
			"acceptance": "- [ ] login works\n",
		},
	})
	// Parent/depends on the new ticket.
	// Default priority is rendered explicitly in visible metadata.
	id2 := e.create(t, "Child", CreateOptions{Priority: 2, Parent: id, DependsOn: []string{id}})
	data, err := os.ReadFile(filepath.Join(e.base, "tickets", id2, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"# Child", "- State: hold", "- Priority: P2", "## Objective"} {
		if !strings.Contains(s, want) {
			t.Fatalf("TASK.md missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "priority:") {
		t.Fatalf("old priority syntax emitted:\n%s", s)
	}
	// Parent and depends rendered.
	if !strings.Contains(s, "- Parent: "+id) || !strings.Contains(s, "- Depends on: "+id) {
		t.Fatalf("relationships missing:\n%s", s)
	}
	// Parse back.
	tk, err := ReadTicket(e.st, id2)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Parent != id || len(tk.DependsOn) != 1 || tk.DependsOn[0] != id {
		t.Fatalf("round trip: parent=%q depends=%v", tk.Parent, tk.DependsOn)
	}
	if tk.State != "hold" {
		t.Fatalf("title-only ticket state=%q want hold", tk.State)
	}
	if created, err := Create(e.st, CreateOptions{Title: "Objective ticket", Sections: map[string]string{"objective": "Start work."}}); err != nil {
		t.Fatal(err)
	} else if ticket, err := ReadTicket(e.st, created.ID); err != nil || ticket.State != "open" {
		t.Fatalf("objective ticket state=%q err=%v", ticket.State, err)
	}
}

func TestStatusUsesDatesAndRetainsStructuralBlockers(t *testing.T) {
	e := newEnv(t, 9400)
	dependency := e.create(t, "Dependency", CreateOptions{Sections: map[string]string{"objective": "Complete dependency."}})
	target := e.create(t, "Target", CreateOptions{DependsOn: []string{dependency}, Sections: map[string]string{"objective": "Complete target."}})
	status, err := Status(e.st, target)
	if err != nil {
		t.Fatal(err)
	}
	if status.Created != "2000-01-01" || status.Modified == "" {
		t.Fatalf("status dates: %+v", status)
	}
	if len(status.Blockers) != 1 || status.Blockers[0].Code != "dependency_open" || status.Blockers[0].ID != dependency {
		t.Fatalf("status blockers: %+v", status.Blockers)
	}
	before := status.Modified
	stamp := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(e.base, "tickets", target, "TASK.md"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	status, err = Status(e.st, target)
	if err != nil {
		t.Fatal(err)
	}
	if status.Modified == before {
		t.Fatalf("status mtime did not change: before=%q after=%q", before, status.Modified)
	}
}

func TestGrepMatchesTaskContentAndSortsLikeList(t *testing.T) {
	e := newEnv(t, 23)
	first := e.create(t, "Parser recovery", CreateOptions{Priority: 1, Sections: map[string]string{
		"objective": "Repair parser recovery.",
	}})
	second := e.create(t, "Documentation", CreateOptions{Priority: 2, Sections: map[string]string{
		"objective": "Explain the parser.",
	}})
	e.create(t, "Unrelated", CreateOptions{Priority: 0, Sections: map[string]string{
		"objective": "Improve unrelated behavior.",
	}})
	result, err := Grep(e.st, `parser recovery|Explain`)
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if len(result.Items) != 2 || result.Items[0].ID != first || result.Items[1].ID != second || result.More {
		t.Fatalf("grep result=%+v", result)
	}
	if _, err := Grep(e.st, "["); err == nil || contractCode(t, err) != contract.ErrInvalidArgument {
		t.Fatalf("invalid expression: %v", err)
	}
	idResult, err := Grep(e.st, "^"+first+"$")
	if err != nil {
		t.Fatalf("grep ticket ID: %v", err)
	}
	if len(idResult.Items) != 1 || idResult.Items[0].ID != first {
		t.Fatalf("grep ticket ID result=%+v", idResult.Items)
	}
	andResult, err := Grep(e.st, "parser", "recovery")
	if err != nil || len(andResult.Items) != 1 || andResult.Items[0].ID != first {
		t.Fatalf("grep AND result=%+v err=%v", andResult, err)
	}
}

// S05: an ID collision retries with a fresh random ID; an entropy
// failure surfaces randomness_unavailable.
func TestCreateCollisionRetry(t *testing.T) {
	e := newEnv(t, 31)
	// Pre-create a valid ticket at the ID the deterministic source will pick
	// first. Graph scans must reject damaged ticket directories, so a bare
	// collision directory is tested at the store layer instead.
	first, err := testutil.NewDeterministicSource(31).ID("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.base, "tickets", first), 0o755); err != nil {
		t.Fatal(err)
	}
	data := RenderNew("occupied", 2, nil, "", nil, map[string]string{})
	if err := os.WriteFile(filepath.Join(e.base, "tickets", first, "TASK.md"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), data...)
	res, err := Create(e.st, CreateOptions{Title: "retry me", DependsOn: []string{first}})
	if err != nil {
		t.Fatalf("create with collision: %v", err)
	}
	if res.ID == first {
		t.Fatal("create published over existing directory")
	}
	after, err := os.ReadFile(filepath.Join(e.base, "tickets", first, "TASK.md"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("collision retry changed the occupied ticket")
	}
	if err := os.RemoveAll(filepath.Join(e.base, "tickets", first)); err != nil {
		t.Fatal(err)
	}
	// Entropy failure (separate repository; e.st still holds the first lock).
	base2 := t.TempDir()
	if _, ierr := store.InitRoot(filepath.Join(base2, "tickets")); ierr != nil {
		t.Fatal(ierr)
	}
	st2, err := store.Open(base2, store.OpenOptions{IDSource: testutil.FailingSource{}})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if _, err := Create(st2, CreateOptions{Title: "no entropy"}); err == nil {
		t.Fatal("expected randomness_unavailable")
	}
}

// S13: symlinked TASK.md is rejected; missing TASK.md is invalid.
func TestSymlinkedTaskRejected(t *testing.T) {
	e := newEnv(t, 41)
	id := e.create(t, "real", CreateOptions{})
	dir := filepath.Join(e.base, "tickets", id)
	if err := os.Rename(filepath.Join(dir, "TASK.md"), filepath.Join(dir, "TASK.md.real")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "TASK.md")
	if err := os.Symlink("TARGET_MISSING", link); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(link)
	if _, err := ReadTicket(e.st, id); err == nil {
		t.Fatal("expected symlink rejection")
	}
	// Missing TASK.md.
	if err := os.RemoveAll(filepath.Join(dir, "TASK.md.real")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTicket(e.st, id); err == nil {
		t.Fatal("expected missing TASK.md error")
	}
}

// C03: list contract: default state, sort, limit, and bounded result.
func TestListContract(t *testing.T) {
	e := newEnv(t, 51)
	e.create(t, "low", CreateOptions{Priority: 3, Tags: []string{"a"}, Sections: map[string]string{"objective": "low"}})
	e.create(t, "high", CreateOptions{Priority: 1, Tags: []string{"b"}, Sections: map[string]string{"objective": "high"}})
	e.create(t, "mid", CreateOptions{Priority: 2, Sections: map[string]string{"objective": "mid"}})
	// Sort: priority ascending, then ID ascending.
	res, err := List(e.st, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 || res.More {
		t.Fatalf("items=%v more=%v", res.Items, res.More)
	}
	if *res.Items[0].Title != "high" || *res.Items[1].Title != "mid" || *res.Items[2].Title != "low" {
		t.Fatalf("wrong sort: %v", res.Items)
	}
	limited, err := List(e.st, ListOptions{Limit: 2})
	if err != nil || !limited.More || len(limited.Items) != 2 {
		t.Fatalf("limit: result=%v err=%v", limited, err)
	}
	for _, limit := range []int{0, -1} {
		if _, err := List(e.st, ListOptions{Limit: limit, LimitSet: true}); err == nil || contractCode(t, err) != contract.ErrInvalidArgument {
			t.Fatalf("list limit %d: %v", limit, err)
		}
		if _, err := Ready(e.st, ListOptions{Limit: limit, LimitSet: true}); err == nil || contractCode(t, err) != contract.ErrInvalidArgument {
			t.Fatalf("ready limit %d: %v", limit, err)
		}
	}
	if _, err := List(e.st, ListOptions{Offset: -1}); err == nil || contractCode(t, err) != contract.ErrInvalidArgument {
		t.Fatalf("negative offset: %v", err)
	}
	// Tag filter.
	res, err = List(e.st, ListOptions{Tags: []string{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || *res.Items[0].Title != "high" {
		t.Fatalf("tag filter: %v", res.Items)
	}
	// Without-tags filter.
	res, err = List(e.st, ListOptions{WithoutTags: []string{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("without-tags: %v", res.Items)
	}
	// Priority filter.
	p3 := 3
	res, err = List(e.st, ListOptions{Priority: &p3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || *res.Items[0].Title != "low" {
		t.Fatalf("priority filter: %v", res.Items)
	}
	// Unassigned: every M1 ticket is unassigned.
	res, err = List(e.st, ListOptions{Unassigned: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("unassigned: %v", res.Items)
	}
	// Assignee + unassigned conflict is a CLI concern; here assignee
	// filter returns nothing.
	res, err = List(e.st, ListOptions{Assignee: "someone"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 0 {
		t.Fatalf("assignee filter: %v", res.Items)
	}
	// Alias sorting is internal to the CLI, but the domain owns the stable
	// ordering used by it. Equal-priority ID order can be reversed explicitly.
	byID, err := List(e.st, ListOptions{State: "all", Sort: "id_desc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byID.Items) != 3 || byID.Items[0].ID <= byID.Items[1].ID || byID.Items[1].ID <= byID.Items[2].ID {
		t.Fatalf("id descending sort: %v", byID.Items)
	}
	window, err := List(e.st, ListOptions{State: "all", Sort: "id_desc", Offset: 1, Limit: 1, LimitSet: true})
	if err != nil || !window.More || len(window.Items) != 1 || window.Items[0].ID != byID.Items[1].ID {
		t.Fatalf("offset window: result=%v err=%v", window, err)
	}
	// Modification ordering uses the complete TASK.md file timestamp and
	// falls back to descending ID for ties.
	mtimes := []time.Time{time.Unix(100, 0), time.Unix(300, 0), time.Unix(200, 0)}
	for i, item := range byID.Items {
		path := filepath.Join(e.st.Root, item.ID, "TASK.md")
		if err := os.Chtimes(path, mtimes[i], mtimes[i]); err != nil {
			t.Fatal(err)
		}
	}
	byModified, err := List(e.st, ListOptions{State: "all", Sort: "modified_desc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byModified.Items) != 3 || byModified.Items[0].ID != byID.Items[1].ID || byModified.Items[1].ID != byID.Items[2].ID || byModified.Items[2].ID != byID.Items[0].ID {
		t.Fatalf("modified descending sort: %v", byModified.Items)
	}
}

// C05/C06: show default sections vs --full; --full preserves the body
// literally.
func TestShowContract(t *testing.T) {
	e := newEnv(t, 71)
	content := "line with special chars: <html> & \"quotes\" 日本語\n```\ncode line## inline, not a heading\n```\n"
	id := e.create(t, "Show me", CreateOptions{
		Sections: map[string]string{
			"objective":  content,
			"acceptance": "- [ ] done\n",
			"handoff":    "nothing\n",
		},
	})
	v, err := Show(e.st, id, ShowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v.AttachmentPath != "" {
		t.Fatalf("unexpected attachment path without directory: %q", v.AttachmentPath)
	}
	if err := os.Mkdir(filepath.Join(e.base, "tickets", id, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	withAttachments, err := Show(e.st, id, ShowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if withAttachments.AttachmentPath != filepath.Join(e.st.Root, id, "attachments") {
		t.Fatalf("attachment path=%q want %q", withAttachments.AttachmentPath, filepath.Join(e.st.Root, id, "attachments"))
	}
	for _, key := range []string{"objective", "acceptance", "handoff"} {
		if _, ok := v.Sections[key]; !ok {
			t.Fatalf("default sections missing %s: %v", key, v.Sections)
		}
	}
	// The blank-line section separator adds a second trailing newline in
	// the raw span; DisplayContent strips exactly one.
	if v.Sections["objective"].Text != content {
		t.Fatalf("objective text=%q want %q", v.Sections["objective"].Text, content)
	}
	// Hand-edited ticket with a fenced heading: create would reject it,
	// but show must return the body literally (fence-aware parsing).
	fm := "---\nstate: open\n---\n# Show me\n## Objective\n```\n## fenced heading stays content\n```\n"
	idh, err := e.st.NewID("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.base, "tickets", idh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.base, "tickets", idh, "TASK.md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	fullReady, err := Show(e.st, idh, ShowOptions{Full: true, Readiness: true})
	if err != nil || fullReady.Readiness == nil {
		t.Fatalf("full show lost readiness: %+v (%v)", fullReady, err)
	}
	vh, err := Show(e.st, idh, ShowOptions{Full: true})
	if err != nil {
		t.Fatalf("show hand-edited: %v", err)
	}
	wantHand := fm[strings.Index(fm, "\n# ")+1:]
	if vh.Body != wantHand {
		t.Fatalf("hand-edited full body mismatch:\ngot %q\nwant %q", vh.Body, wantHand)
	}
	// An existing Findings heading is ordinary Markdown. An unrelated title
	// update preserves it byte-for-byte.
	path := filepath.Join(e.base, "tickets", id, "TASK.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	withFindings := strings.Replace(string(data), "## Handoff\n", "## Findings\nobservations\n\n## Handoff\n", 1)
	if withFindings == string(data) {
		t.Fatal("handoff section not found")
	}
	if err := os.WriteFile(path, []byte(withFindings), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(e.st, id, UpdateOptions{Set: map[string]any{"title": "Show me updated"}}); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(updated), "## Findings\nobservations\n") {
		t.Fatalf("existing Findings was not preserved: %v\n%s", err, updated)
	}
	// Full: literal body preservation.
	v, err = Show(e.st, id, ShowOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(e.base, "tickets", id, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	fileBody := string(data)
	wantBody := fileBody
	if v.Body != wantBody {
		t.Fatalf("full body mismatch:\ngot %q\nwant %q", v.Body, wantBody)
	}
}

// C07: byte budget truncation on UTF-8 boundaries.
func TestShowBudgetTruncation(t *testing.T) {
	e := newEnv(t, 81)
	content := "ab日本語abcd"
	id := e.create(t, "Budget", CreateOptions{Sections: map[string]string{"objective": content + "\n"}})
	v, err := Show(e.st, id, ShowOptions{MaxBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Truncated || !v.Sections["objective"].Truncated {
		t.Fatalf("expected truncation: %+v", v.Sections)
	}
	text := v.Sections["objective"].Text
	if len(text) > 10 {
		t.Fatalf("not truncated: %q", text)
	}
	// No split multi-byte: text must be a valid UTF-8 prefix.
	if text != "ab日本" {
		t.Fatalf("bad UTF-8 cut: %q", text)
	}
}

// Prerequisites: direct depends_on summaries; missing deps flagged.
func TestShowPrerequisites(t *testing.T) {
	e := newEnv(t, 91)
	dep := e.create(t, "Dep", CreateOptions{})
	id := e.create(t, "Main", CreateOptions{DependsOn: []string{dep}})
	// Delete the dependency: the prerequisite must be flagged missing.
	if err := os.RemoveAll(filepath.Join(e.base, "tickets", dep)); err != nil {
		t.Fatal(err)
	}
	v, err := Show(e.st, id, ShowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Prerequisites) != 1 {
		t.Fatalf("prereqs=%v", v.Prerequisites)
	}
	if v.Prerequisites[0].ID != dep || !v.Prerequisites[0].Missing {
		t.Fatalf("missing flag not set: %v", v.Prerequisites)
	}
}

// Path: relative and absolute.
func TestPath(t *testing.T) {
	e := newEnv(t, 101)
	id := e.create(t, "P", CreateOptions{})
	rel, err := Path(e.st, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if rel["id"] != id || rel["path"] != id+"/TASK.md" {
		t.Fatalf("rel=%v", rel)
	}
	abs, err := Path(e.st, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(abs["path"]) {
		t.Fatalf("not absolute: %v", abs)
	}
	if filepath.Base(abs["path"]) != "TASK.md" {
		t.Fatalf("path=%v", abs)
	}
	// Shorthand.
	short, err := Path(e.st, id[:6], false)
	if err != nil {
		t.Fatal(err)
	}
	if short["id"] != id {
		t.Fatalf("shorthand: %v", short)
	}
}

func setStateLine(t *testing.T, base, id, oldLine, newLine string) error {
	t.Helper()
	path := filepath.Join(base, "tickets", id, "TASK.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	replaced := strings.Replace(string(data), oldLine, newLine, 1)
	if replaced == string(data) {
		return errors.New("state line not found")
	}
	return os.WriteFile(path, []byte(replaced), 0o644)
}
