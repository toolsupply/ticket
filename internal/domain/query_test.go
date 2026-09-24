package domain

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEvaluateQueryPredicatesScopesAndCanonicalState(t *testing.T) {
	e := newEnv(t, 12000)
	parent := e.create(t, "Parent", CreateOptions{Priority: 1, Tags: []string{"release"}, Sections: map[string]string{"objective": "Parent work."}})
	ready := e.create(t, "Ready", CreateOptions{Priority: 0, Tags: []string{"release"}, Sections: map[string]string{"objective": "Ready work."}})
	dependency := e.create(t, "Dependency", CreateOptions{Sections: map[string]string{"objective": "Dependency work."}})
	blocked := e.create(t, "Blocked", CreateOptions{DependsOn: []string{dependency}, Sections: map[string]string{"objective": "Blocked work."}})
	child := e.create(t, "Child", CreateOptions{Parent: parent, Sections: map[string]string{"objective": "Child work."}})
	archived := e.create(t, "Archived", CreateOptions{Priority: 3, Sections: map[string]string{"objective": "Archived work."}})
	if _, err := Close(e.st, archived, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(e.st, archived); err != nil {
		t.Fatal(err)
	}

	query, err := EvaluateQuery(e.st, QuerySpec{Expr: And(Field("state", "completed"), Field("tag", "release"))})
	if err != nil {
		t.Fatal(err)
	}
	if len(query.Tickets) != 0 {
		t.Fatalf("closed alias unexpectedly selected open tickets: %v", query.IDs())
	}
	if _, err := Close(e.st, parent, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	query, err = EvaluateQuery(e.st, QuerySpec{Expr: Field("state", "completed")})
	if err != nil || len(query.Tickets) != 1 || query.Tickets[0].ID != parent || query.Tickets[0].State != StateClosed {
		t.Fatalf("canonical closed query: ids=%v err=%v", query.IDs(), err)
	}

	byParent, err := EvaluateQuery(e.st, QuerySpec{Expr: Field("parent", parent)})
	if err != nil || len(byParent.Tickets) != 1 || byParent.Tickets[0].ID != child {
		t.Fatalf("parent query: ids=%v err=%v", byParent.IDs(), err)
	}
	readySet, err := EvaluateQuery(e.st, QuerySpec{Expr: And(Field("state", "open"), Bare("ready"))})
	if err != nil || len(readySet.Tickets) != 3 || readySet.Tickets[0].ID != ready || readySet.Tickets[1].ID != dependency || readySet.Tickets[2].ID != child {
		t.Fatalf("ready query: ids=%v err=%v", readySet.IDs(), err)
	}
	blockedSet, err := EvaluateQuery(e.st, QuerySpec{Expr: Bare("blocked")})
	if err != nil || len(blockedSet.Tickets) != 1 || blockedSet.Tickets[0].ID != blocked {
		t.Fatalf("blocked query: ids=%v err=%v", blockedSet.IDs(), err)
	}

	archivedSet, err := EvaluateQuery(e.st, QuerySpec{Scope: ScopeArchived, Expr: Bare("archived")})
	if err != nil || len(archivedSet.Tickets) != 1 || archivedSet.Tickets[0].ID != archived {
		t.Fatalf("archived query: ids=%v err=%v", archivedSet.IDs(), err)
	}
	allSet, err := EvaluateQuery(e.st, QuerySpec{Scope: ScopeAll, Expr: Bare("archived")})
	if err != nil || len(allSet.Tickets) != 1 || allSet.Tickets[0].ID != archived {
		t.Fatalf("all archived query: ids=%v err=%v", allSet.IDs(), err)
	}
	if allSet.Tickets[0].State != StateClosed {
		t.Fatalf("archived state=%q, want %q", allSet.Tickets[0].State, StateClosed)
	}
}

func TestEvaluateQueryOrderingLimitsAndTargetUnion(t *testing.T) {
	e := newEnv(t, 12010)
	low := e.create(t, "Low", CreateOptions{Priority: 0, Sections: map[string]string{"objective": "Low."}})
	high := e.create(t, "High", CreateOptions{Priority: 2, Sections: map[string]string{"objective": "High."}})
	middle := e.create(t, "Middle", CreateOptions{Priority: 1, Sections: map[string]string{"objective": "Middle."}})

	set, err := EvaluateQuery(e.st, QuerySpec{Sort: SortPriorityID, Offset: 1, Limit: 1})
	if err != nil || len(set.Tickets) != 1 || set.Tickets[0].ID != middle {
		t.Fatalf("priority pagination: ids=%v err=%v", set.IDs(), err)
	}
	byID, err := EvaluateQuery(e.st, QuerySpec{Sort: SortIDDesc})
	if err != nil || len(byID.Tickets) != 3 || byID.Tickets[0].ID != middle && byID.Tickets[0].ID != high {
		t.Fatalf("id-desc query: ids=%v err=%v", byID.IDs(), err)
	}

	union, err := EvaluateTarget(e.st, TargetSpec{
		ExplicitRefs: []string{high},
		Query:        &QuerySpec{Expr: Field("state", "open")},
	})
	if err != nil || len(union.Tickets) != 3 || union.Tickets[0].ID != high {
		t.Fatalf("target union: ids=%v err=%v", union.IDs(), err)
	}
	seen := map[string]bool{}
	for _, ticket := range union.Tickets {
		if seen[ticket.ID] {
			t.Fatalf("duplicate target %s: %v", ticket.ID, union.IDs())
		}
		seen[ticket.ID] = true
	}
	limited, err := EvaluateTarget(e.st, TargetSpec{
		ExplicitRefs: []string{high},
		Query:        &QuerySpec{Limit: 1},
	})
	if err != nil || !limited.More {
		t.Fatalf("target union pagination: more=%v err=%v", limited.More, err)
	}
	if low == "" {
		t.Fatal("unreachable test fixture")
	}
}

func TestEvaluateQueryIsReadOnlyAndReportsCollectionDiagnostics(t *testing.T) {
	e := newEnv(t, 12020)
	id := e.create(t, "Stable", CreateOptions{Sections: map[string]string{"objective": "Do not mutate."}})
	path := filepath.Join(e.base, "tickets", id, "TASK.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	set, err := EvaluateQuery(e.st, QuerySpec{Expr: Field("priority", "P0")})
	if err != nil || len(set.Tickets) != 1 {
		t.Fatalf("read-only query: ids=%v err=%v", set.IDs(), err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("query changed TASK.md: err=%v", err)
	}

	badID := "20260923-99999"
	badPath := filepath.Join(e.base, "tickets", badID)
	if err := os.MkdirAll(badPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badPath, "TASK.md"), []byte("# malformed\n\n- State: ???\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluateQuery(e.st, QuerySpec{}); err == nil || contractCode(t, err) != "invalid_ticket" {
		t.Fatalf("malformed collection result: %v", err)
	}
}

func TestArchiveReadyQueriesDoNotScanActiveStorage(t *testing.T) {
	e := newEnv(t, 12030)
	archived := e.create(t, "Archived", CreateOptions{Sections: map[string]string{"objective": "Archive this."}})
	if _, err := Close(e.st, archived, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(e.st, archived); err != nil {
		t.Fatal(err)
	}
	badID := "20260923-99998"
	badPath := filepath.Join(e.base, "tickets", badID)
	if err := os.MkdirAll(badPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badPath, "TASK.md"), []byte("# malformed\n\n- State: ???\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ready", "blocked"} {
		set, err := EvaluateQuery(e.st, QuerySpec{Scope: ScopeArchived, Expr: Bare(name)})
		if err != nil || len(set.Tickets) != 0 {
			t.Fatalf("archive-only %s query: ids=%v err=%v", name, set.IDs(), err)
		}
	}
}

func TestActiveTicketSetProjectionKeepsArchiveLocality(t *testing.T) {
	e := newEnv(t, 12040)
	dependency := e.create(t, "Archived dependency", CreateOptions{Sections: map[string]string{"objective": "Dependency."}})
	target := e.create(t, "Active target", CreateOptions{
		Tags:      []string{"projection-target"},
		DependsOn: []string{dependency},
		Sections:  map[string]string{"objective": "Target."},
	})
	if _, err := Close(e.st, dependency, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(e.st, dependency); err != nil {
		t.Fatal(err)
	}
	unrelated := e.create(t, "Malformed archive", CreateOptions{Sections: map[string]string{"objective": "Corrupt this after archiving."}})
	if _, err := Close(e.st, unrelated, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(e.st, unrelated); err != nil {
		t.Fatal(err)
	}
	malformedPath := filepath.Join(e.base, "tickets", "archive", unrelated, "TASK.md")
	if err := os.WriteFile(malformedPath, []byte("# malformed\n\n- State: ???\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	set, err := EvaluateQuery(e.st, QuerySpec{Expr: Field("tag", "projection-target")})
	if err != nil || len(set.Tickets) != 1 || set.Tickets[0].ID != target {
		t.Fatalf("active query crossed archive boundary: ids=%v err=%v", set.IDs(), err)
	}
	for _, view := range []string{"", "blocking", "all"} {
		result, err := ProjectTicketSet(e.st, set, nil, view)
		if err != nil || len(result.Items) != 1 {
			t.Fatalf("projection view %q: result=%+v err=%v", view, result, err)
		}
		if view == "all" {
			dependencies := result.Items[0].Dependencies
			if len(dependencies) != 1 || dependencies[0].ID != dependency || !dependencies[0].Archived || !dependencies[0].Satisfied {
				t.Fatalf("archived dependency projection: %+v", dependencies)
			}
		} else if len(result.Items[0].Dependencies) != 0 {
			t.Fatalf("unexpected dependencies for view %q: %+v", view, result.Items[0].Dependencies)
		}
	}
}
