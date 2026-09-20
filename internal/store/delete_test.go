package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
)

func seedDeleteTicket(t *testing.T) (*Store, string, []byte) {
	t.Helper()
	base := t.TempDir()
	root := initTicketRoot(t, base)
	id := "20260917-12345"
	ticketDir := filepath.Join(root, id)
	if err := os.Mkdir(ticketDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("---\nstate: open\n---\n# Delete me\n")
	if err := os.WriteFile(filepath.Join(ticketDir, "TASK.md"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return &Store{Root: root}, id, data
}

func TestDeleteRenameFailurePreservesCanonicalTicket(t *testing.T) {
	st, id, before := seedDeleteTicket(t)
	originalRename := deleteRename
	deleteRename = func(_, _ string) error { return errors.New("rename blocked") }
	t.Cleanup(func() { deleteRename = originalRename })

	err := st.DeleteTicket(id)
	if err == nil || contractCode(t, err) != contract.ErrIOError {
		t.Fatalf("rename failure: %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(st.Root, id, "TASK.md"))
	if readErr != nil || string(data) != string(before) {
		t.Fatalf("canonical ticket changed after rename failure: %q (%v)", data, readErr)
	}
}

func TestDeleteCleanupFailureLeavesCanonicalNamespaceConsistent(t *testing.T) {
	st, id, before := seedDeleteTicket(t)
	originalCleanup := deleteRemoveAll
	var staged string
	deleteRemoveAll = func(path string) error {
		staged = path
		return errors.New("cleanup blocked")
	}
	t.Cleanup(func() { deleteRemoveAll = originalCleanup })

	err := st.DeleteTicket(id)
	if err == nil || contractCode(t, err) != contract.ErrIOError {
		t.Fatalf("cleanup failure: %v", err)
	}
	if !strings.Contains(err.Error(), "removed from the repository") {
		t.Fatalf("cleanup failure is not explicit: %v", err)
	}
	var ce *contract.Error
	if !errors.As(err, &ce) || ce.Details["removal_applied"] != true {
		t.Fatalf("cleanup failure details: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(st.Root, id)); !os.IsNotExist(statErr) {
		t.Fatalf("canonical ticket remains after staged deletion: %v", statErr)
	}
	data, readErr := os.ReadFile(filepath.Join(staged, "TASK.md"))
	if readErr != nil || string(data) != string(before) {
		t.Fatalf("staged ticket was not preserved after cleanup failure: %q (%v)", data, readErr)
	}
}

func contractCode(t *testing.T, err error) contract.ErrorCode {
	t.Helper()
	var ce *contract.Error
	if !errors.As(err, &ce) {
		t.Fatalf("not a contract error: %v", err)
	}
	return ce.Code
}
