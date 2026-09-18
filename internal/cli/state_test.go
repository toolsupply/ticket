package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateOverrideClearsAssignmentAndPreservesContent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "State correction", "Preserve this objective."))["id"].(string)
	mustCLI(t, "claim", id, "--actor", "alice")
	path := filepath.Join("tickets", id, "TASK.md")

	result := exactlyOneJSONObject(t, mustCLI(t, "state", id, "review", "-m", "Corrected by supervisor"))
	if result["id"] != id || result["changed"] != true || result["state"] != "review" {
		t.Fatalf("state result: %v", result)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if view["state"] != "review" || view["assignee"] != nil {
		t.Fatalf("state correction: %v", view)
	}
	if !strings.Contains(view["body"].(string), "Preserve this objective.") ||
		!strings.Contains(view["body"].(string), "Corrected by supervisor") {
		t.Fatalf("state correction lost content: %v", view["body"])
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, "state", id[:len(id)-1], "open"); code == 0 {
		t.Fatalf("state accepted a ticket prefix: %q", out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("invalid state correction changed TASK.md")
	}
}

func TestStateOverrideRejectsInvalidState(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Invalid state", "Keep state unchanged."))["id"].(string)
	path := filepath.Join("tickets", id, "TASK.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, "state", id, "unknown"); code == 0 {
		t.Fatalf("invalid state accepted: %q", out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("invalid state changed TASK.md")
	}
}
