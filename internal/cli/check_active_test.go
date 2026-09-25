package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/toolsupply/ticket/internal/store"
)

func TestCheckActiveCLILeavesUnrelatedArchivesOutOfScope(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TICKET_CURRENT", "")
	t.Setenv("TICKET_REPOSITORY", "")
	t.Setenv("TICKET_ROOT", "")
	mustCLI(t, "init")
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Archived check target", "Archive corruption is unrelated."))
	id := created["id"].(string)
	mustCLI(t, "close", id)
	mustCLI(t, "archive", id)
	archiveTask := filepath.Join("tickets", store.ArchiveDirName, id, "TASK.md")
	if err := os.WriteFile(archiveTask, []byte("malformed archived ticket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, "check", "--active"); code != 0 || exactlyOneJSONObject(t, out)["ok"] != true {
		t.Fatalf("active JSON check: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "check"); code == 0 || errCode(t, out) != "invalid_ticket" {
		t.Fatalf("full check did not report archive corruption: exit=%d out=%q", code, out)
	}
}
