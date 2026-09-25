package domain

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/store"
)

func TestArchiveRoundTripAndReadOnlyMutations(t *testing.T) {
	e := newEnv(t, 8800)
	id := e.create(t, "Archive me", CreateOptions{Sections: map[string]string{"objective": "Finish and archive."}})
	if _, err := Close(e.st, id, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	result, err := Archive(e.st, id)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Archived || result.FromPath != id || result.ToPath != store.ArchiveDirName+"/"+id {
		t.Fatalf("archive result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(e.base, "tickets", store.ArchiveDirName, id, "TASK.md")); err != nil {
		t.Fatal(err)
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil || !ticket.Archived || ticket.State != "closed" {
		t.Fatalf("archived read: ticket=%+v err=%v", ticket, err)
	}
	taskRel, err := e.st.TaskRelPath(id)
	if err != nil || taskRel != store.ArchiveDirName+"/"+id+"/TASK.md" {
		t.Fatalf("archived TaskRelPath=%q err=%v", taskRel, err)
	}
	dirRel, err := e.st.TicketDirRelPath(id)
	if err != nil || dirRel != store.ArchiveDirName+"/"+id {
		t.Fatalf("archived TicketDirRelPath=%q err=%v", dirRel, err)
	}
	if _, err := Update(e.st, id, UpdateOptions{Set: map[string]any{"priority": 1}}); contractCode(t, err) != contract.ErrArchived {
		t.Fatalf("archived update code: %v", err)
	}
	active, err := List(e.st, ListOptions{States: []string{"all"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Items) != 0 {
		t.Fatalf("archived ticket leaked into active list: %+v", active.Items)
	}
	archived, err := List(e.st, ListOptions{ArchivedOnly: true, Fields: []string{"path"}})
	if err != nil || len(archived.Items) != 1 || !archived.Items[0].Archived {
		t.Fatalf("archived list: result=%+v err=%v", archived, err)
	}
	if archived.Items[0].Path == nil || *archived.Items[0].Path != store.ArchiveDirName+"/"+id+"/TASK.md" {
		t.Fatalf("archived list path=%v", archived.Items[0].Path)
	}
	pathResult, err := Path(e.st, id, false)
	if err != nil || pathResult["path"] != store.ArchiveDirName+"/"+id+"/TASK.md" {
		t.Fatalf("archived path result=%v err=%v", pathResult, err)
	}
	if _, err := Unarchive(e.st, id); err != nil {
		t.Fatal(err)
	}
	ticket, err = ReadTicket(e.st, id)
	if err != nil || ticket.Archived {
		t.Fatalf("unarchived read: ticket=%+v err=%v", ticket, err)
	}
}

func TestArchiveReferencesParticipateInActiveReadiness(t *testing.T) {
	e := newEnv(t, 8810)
	dependency := e.create(t, "Dependency", CreateOptions{Sections: map[string]string{"objective": "Finish first."}})
	target := e.create(t, "Target", CreateOptions{DependsOn: []string{dependency}, Sections: map[string]string{"objective": "Then continue."}})
	if _, err := Close(e.st, dependency, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(e.st, dependency); err != nil {
		t.Fatal(err)
	}
	readiness, err := Readiness(e.st, target)
	if err != nil {
		t.Fatal(err)
	}
	if !readiness.Ready {
		t.Fatalf("active target remained blocked by archived dependency: %+v", readiness.Blockers)
	}
	if err := ValidateGraphs(e.st); err != nil {
		t.Fatalf("archive-aware graph validation: %v", err)
	}
	if err := ValidateActiveGraphs(e.st); err != nil {
		t.Fatalf("active-only archive-aware graph validation: %v", err)
	}
}

func TestValidateActiveGraphsIgnoresUnrelatedCorruptArchive(t *testing.T) {
	e := newEnv(t, 8812)
	active := e.create(t, "Active ticket", CreateOptions{})
	archived := e.create(t, "Archived ticket", CreateOptions{})
	if _, err := Close(e.st, archived, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(e.st, archived); err != nil {
		t.Fatal(err)
	}
	archiveTask := filepath.Join(e.base, "tickets", store.ArchiveDirName, archived, "TASK.md")
	if err := os.WriteFile(archiveTask, []byte("corrupt archived ticket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateActiveGraphs(e.st); err != nil {
		t.Fatalf("active-only check rejected unrelated archive corruption (active %s): %v", active, err)
	}
	if err := ValidateGraphs(e.st); err == nil || contractCode(t, err) != contract.ErrInvalidTicket {
		t.Fatalf("full check did not reject archive corruption: %v", err)
	}
}

func TestValidateActiveGraphsDetectsActiveStructuralErrors(t *testing.T) {
	e := newEnv(t, 8813)
	dependency := e.create(t, "Dependency", CreateOptions{})
	target := e.create(t, "Target", CreateOptions{DependsOn: []string{dependency}})
	insertTaskFMLine(t, e.base, target, "depends_on: ["+dependency+", "+dependency+"]\n")
	if err := ValidateActiveGraphs(e.st); err == nil || contractCode(t, err) != contract.ErrInvalidTicket {
		t.Fatalf("active-only check did not reject duplicate active dependencies: %v", err)
	}
}

func TestValidateActiveGraphsDetectsActiveRelationshipCycles(t *testing.T) {
	e := newEnv(t, 8814)
	first := e.create(t, "First", CreateOptions{})
	second := e.create(t, "Second", CreateOptions{DependsOn: []string{first}})
	insertTaskFMLine(t, e.base, first, "depends_on: ["+second+"]\n")
	if err := ValidateActiveGraphs(e.st); err == nil || contractCode(t, err) != contract.ErrDependencyCycle {
		t.Fatalf("active-only check did not reject a dependency cycle: %v", err)
	}
}

func TestCombinedReadinessGraphResolvesArchivedRelationships(t *testing.T) {
	e := newEnv(t, 8811)
	parent := e.create(t, "Parent", CreateOptions{})
	child := e.create(t, "Child", CreateOptions{Parent: parent})
	if _, err := Close(e.st, child, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(e.st, child); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(e.st, parent, UpdateOptions{Set: map[string]any{"depends_on": []any{child}}}); err != nil {
		t.Fatalf("archived relationship rejected: %v", err)
	}
	if err := ValidateGraphs(e.st); err != nil {
		t.Fatalf("archive-aware combined graph validation: %v", err)
	}
}

func TestArchiveManyPrevalidatesAndDeduplicates(t *testing.T) {
	e := newEnv(t, 8830)
	closed := e.create(t, "Closed batch", CreateOptions{Sections: map[string]string{"objective": "Archive this."}})
	open := e.create(t, "Open batch", CreateOptions{Sections: map[string]string{"objective": "Keep active."}})
	if _, err := Close(e.st, closed, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ArchiveMany(e.st, []string{closed, open, closed}); err == nil || contractCode(t, err) != contract.ErrInvalidTransition {
		t.Fatalf("invalid batch accepted: %v", err)
	}
	if ticket, err := ReadTicket(e.st, closed); err != nil || ticket.Archived {
		t.Fatalf("prevalidation partially archived closed ticket: ticket=%+v err=%v", ticket, err)
	}
	result, err := ArchiveMany(e.st, []string{closed, closed})
	if err != nil || len(result.Items) != 1 || !result.Items[0].Changed {
		t.Fatalf("deduplicated archive: result=%+v err=%v", result, err)
	}
	empty, err := ArchiveMany(e.st, nil)
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("empty archive batch: result=%+v err=%v", empty, err)
	}
}

func TestManualArchiveMoveIsResolved(t *testing.T) {
	e := newEnv(t, 8820)
	id := e.create(t, "Manual move", CreateOptions{Sections: map[string]string{"objective": "Move without rewriting."}})
	if _, err := Close(e.st, id, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	archiveRoot := filepath.Join(e.base, "tickets", store.ArchiveDirName)
	if err := os.MkdirAll(archiveRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(e.base, "tickets", id), filepath.Join(archiveRoot, id)); err != nil {
		t.Fatal(err)
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil || !ticket.Archived {
		t.Fatalf("manual archive lookup: ticket=%+v err=%v", ticket, err)
	}
	if _, err := Unarchive(e.st, id); err != nil {
		t.Fatal(err)
	}
}
