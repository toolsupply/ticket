package domain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
)

// F1: IDs are validated at domain entry points. A traversal-shaped ID
// (full length, correct prefix, non-canonical body) must be rejected at
// every read/mutate entry point and must not read or modify anything
// outside the repository root.

const escapeFixture = "xxxxxxxxxxxxxxxxxxx" // exactly 19 chars

func TestIDEntryPointsRejectTraversal(t *testing.T) {
	e := newEnv(t, 121)
	realID := e.create(t, "real", CreateOptions{})

	// The traversal ID resolves, from the ticket root (e.base/tickets),
	// through e.base/20260913/../../fixture. Make that external target a valid
	// ticket so an ordinary missing-file error cannot mask missing validation.
	fixture := filepath.Join(e.base, escapeFixture)
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	fixtureBytes, err := os.ReadFile(filepath.Join(e.base, "tickets", realID, "TASK.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "TASK.md"), fixtureBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := append([]byte(nil), fixtureBytes...)
	trav := "20260913/../../" + escapeFixture
	cleanTarget := filepath.Clean(filepath.Join(e.st.Root, trav, "TASK.md"))
	if cleanTarget != filepath.Join(fixture, "TASK.md") {
		t.Fatalf("traversal fixture resolves to %q, want %q", cleanTarget, filepath.Join(fixture, "TASK.md"))
	}

	ops := map[string]func() error{
		"read ticket": func() error { _, err := ReadTicket(e.st, trav); return err },
		"show":        func() error { _, err := Show(e.st, trav, ShowOptions{}); return err },
		"path":        func() error { _, err := Path(e.st, trav, false); return err },
		"update": func() error {
			_, err := Update(e.st, trav, UpdateOptions{Set: map[string]any{"title": "x"}})
			return err
		},
		"list parent": func() error { _, err := List(e.st, ListOptions{Parent: trav}); return err },
		"create parent": func() error {
			_, err := Create(e.st, CreateOptions{Title: "t", Parent: trav})
			return err
		},
		"create depends_on": func() error {
			_, err := Create(e.st, CreateOptions{Title: "t", DependsOn: []string{trav}})
			return err
		},
	}
	for name, op := range ops {
		if err := op(); err == nil {
			t.Fatalf("%s: traversal-shaped ID accepted", name)
		} else if name == "read ticket" || name == "show" {
			if got := contractCode(t, err); got != contract.ErrInvalidArgument {
				t.Fatalf("%s: code=%s want invalid_argument", name, got)
			}
		}
		// Relationship fields of an existing ticket.
		for field, ref := range map[string]any{
			"parent":     trav,
			"depends_on": []any{trav},
		} {
			if _, err := Update(e.st, realID, UpdateOptions{Set: map[string]any{field: ref}}); err == nil {
				t.Fatalf("update %s: traversal-shaped ID accepted", field)
			}
		}
	}
	// The external sentinel is byte-identical.
	data, err := os.ReadFile(filepath.Join(fixture, "TASK.md"))
	if err != nil || string(data) != string(sentinel) {
		t.Fatalf("external fixture modified: %q %v", data, err)
	}
	// Nothing was created at the resolved escape path.
	if _, err := os.Stat(filepath.Join(fixture, "TASK.md")); os.IsNotExist(err) {
		t.Fatal("sentinel file disappeared")
	}
	if st, err := os.Stat(filepath.Join(fixture, "TASK.md")); err == nil && !st.Mode().IsRegular() {
		t.Fatalf("fixture path is no longer a regular file: %v", st.Mode())
	}
}

// F1: validation is canonicality, not existence. A canonical ID for a
// ticket that does not exist resolves (dangling), while non-canonical
// forms - wrong case, malformed suffix, wrong entity prefix -
// are rejected as invalid IDs.
func TestIDValidationCanonicalForms(t *testing.T) {
	e := newEnv(t, 122)

	canonical := "20260101-00000"

	// Canonical but non-existent: a lookup miss, not an invalid ID.
	if _, err := ReadTicket(e.st, canonical); err == nil {
		t.Fatal("expected error for non-existent canonical ID")
	}

	badLast := canonical[:len(canonical)-1] + "x"

	badPrefix := "tk_" + canonical

	for _, id := range []string{strings.ToUpper(canonical), badLast, badPrefix} {
		if _, err := ReadTicket(e.st, id); err == nil {
			t.Fatalf("non-canonical ID accepted: %q", id)
		} else if got := contractCode(t, err); got == contract.ErrDanglingReference {
			// must be invalid_id, not a lookup miss
			t.Fatalf("non-canonical ID %q treated as lookup miss", id)
		}
	}

	// Valid canonical IDs still resolve directly (no collection
	// enumeration): a created ticket is readable by full ID.
	realID := e.create(t, "valid", CreateOptions{})
	if _, err := ReadTicket(e.st, realID); err != nil {
		t.Fatalf("valid canonical ID rejected: %v", err)
	}
}
