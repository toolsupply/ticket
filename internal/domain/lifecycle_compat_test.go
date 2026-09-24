package domain

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyCompletedStateNormalizesWithoutEagerRewrite(t *testing.T) {
	data := []byte("# Legacy\n\n- State: completed\n- Priority: P2\n\n## Objective\n\nKeep this ticket usable.\n")
	ticket, err := ParseTicketFile("20260922-legacy", data)
	if err != nil || ticket.State != StateClosed {
		t.Fatalf("legacy parse: ticket=%+v err=%v", ticket, err)
	}
	for _, diagnostic := range ticket.Diagnostics {
		if diagnostic.Code == "field_value" {
			t.Fatalf("legacy state produced validation diagnostic: %+v", diagnostic)
		}
	}
}

func TestLegacyCloseNoOpAndRealMutationCanonicalize(t *testing.T) {
	e := newEnv(t, 9920)
	id := e.create(t, "Legacy close", CreateOptions{Sections: map[string]string{"objective": "Keep this ticket usable."}})
	path := filepath.Join(e.base, "tickets", id, "TASK.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Replace(original, []byte("- State: open"), []byte("- State: completed"), 1)
	if bytes.Equal(original, legacy) {
		t.Fatal("legacy fixture was not changed")
	}
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil || ticket.State != StateClosed {
		t.Fatalf("legacy active read: ticket=%+v err=%v", ticket, err)
	}
	stateResult, err := SetState(e.st, id, "completed", StateOptions{})
	if err != nil || stateResult.Changed || stateResult.FromState != StateClosed || stateResult.State != StateClosed {
		t.Fatalf("legacy state no-op: result=%+v err=%v", stateResult, err)
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(unchanged, legacy) {
		t.Fatal("legacy state no-op rewrote TASK.md")
	}
	result, err := Close(e.st, id, CloseOptions{Outcome: "must not be written"})
	if err != nil || result.Changed || result.FromState != StateClosed || result.State != StateClosed {
		t.Fatalf("legacy close no-op: result=%+v err=%v", result, err)
	}
	unchanged, _ = os.ReadFile(path)
	if !bytes.Equal(unchanged, legacy) {
		t.Fatal("legacy no-op close rewrote TASK.md")
	}
	if _, err := Update(e.st, id, UpdateOptions{Set: map[string]any{"priority": 1}}); err != nil {
		t.Fatal(err)
	}
	canonical, _ := os.ReadFile(path)
	if !bytes.Contains(canonical, []byte("- State: closed")) || bytes.Contains(canonical, []byte("- State: completed")) {
		t.Fatalf("real mutation did not canonicalize state: %s", canonical)
	}
}

func TestLegacyArchivePreservesBytesAndReportsClosed(t *testing.T) {
	e := newEnv(t, 9930)
	id := e.create(t, "Legacy archive", CreateOptions{Sections: map[string]string{"objective": "Archive this ticket."}})
	path := filepath.Join(e.base, "tickets", id, "TASK.md")
	if _, err := Close(e.st, id, CloseOptions{}); err != nil {
		t.Fatal(err)
	}
	closedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Replace(closedBytes, []byte("- State: closed"), []byte("- State: completed"), 1)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(e.st, id); err != nil {
		t.Fatal(err)
	}
	archivedPath := filepath.Join(e.base, "tickets", "archive", id, "TASK.md")
	archivedBytes, _ := os.ReadFile(archivedPath)
	if !bytes.Equal(archivedBytes, legacy) {
		t.Fatal("archive rewrote legacy TASK.md")
	}
	ticket, err := ReadTicket(e.st, id)
	if err != nil || ticket.State != StateClosed {
		t.Fatalf("archived legacy read: ticket=%+v err=%v", ticket, err)
	}
	if _, err := Unarchive(e.st, id); err != nil {
		t.Fatal(err)
	}
	unarchivedBytes, _ := os.ReadFile(path)
	if !bytes.Equal(unarchivedBytes, legacy) {
		t.Fatal("unarchive rewrote legacy TASK.md")
	}
}

func TestLifecycleStateNormalizer(t *testing.T) {
	for _, value := range []string{"open", "hold", "review", "signoff", "closed", "rejected"} {
		if got, ok := NormalizeLifecycleState(value); !ok || got != value {
			t.Fatalf("normalize %q: got %q ok=%v", value, got, ok)
		}
	}
	if got, ok := NormalizeLifecycleState("completed"); !ok || got != StateClosed {
		t.Fatalf("normalize legacy: got %q ok=%v", got, ok)
	}
	if _, ok := NormalizeLifecycleState("all"); ok {
		t.Fatal("selector syntax accepted as lifecycle state")
	}
	if !strings.Contains("closed", StateClosed) {
		t.Fatal("canonical state constant is unexpected")
	}
}
