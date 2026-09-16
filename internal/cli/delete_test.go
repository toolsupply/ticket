package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteRemovesOneOrMoreTicketDirectories(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	first := exactlyOneJSONObject(t, mustCLI(t, "create", "first", "Do first."))["id"].(string)
	second := exactlyOneJSONObject(t, mustCLI(t, "create", "second", "Do second."))["id"].(string)

	result := exactlyOneJSONObject(t, mustCLI(t, "delete", second+","+first))
	items, ok := result["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("delete result: %v", result)
	}
	for i, item := range items {
		row := item.(map[string]any)
		if row["changed"] != true {
			t.Fatalf("delete item %d: %v", i, row)
		}
	}
	if items[0].(map[string]any)["id"] != first || items[1].(map[string]any)["id"] != second {
		t.Fatalf("delete order: %v", items)
	}
	for _, id := range []string{first, second} {
		if _, err := os.Stat(filepath.Join(dir, "tickets", id)); !os.IsNotExist(err) {
			t.Fatalf("ticket %s still exists, err=%v", id, err)
		}
	}

	third := exactlyOneJSONObject(t, mustCLI(t, "create", "third", "Do third."))["id"].(string)
	human, code := runCLIHuman(t, "delete", third)
	if code != 0 || human != "deleted "+third+"\n" {
		t.Fatalf("human delete: exit=%d output=%q", code, human)
	}
}

func TestDeletePreflightsAllTargets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "keep", "Keep this."))["id"].(string)

	result, code := runCLI(t, "delete", id, "20260915-99999")
	if code == 0 {
		t.Fatalf("expected missing target failure: %q", result)
	}
	errorObject := exactlyOneJSONObject(t, result)["error"].(map[string]any)
	if errorObject["code"] != "not_found" {
		t.Fatalf("unexpected error: %v", errorObject)
	}
	if _, err := os.Stat(filepath.Join(dir, "tickets", id, "TASK.md")); err != nil {
		t.Fatalf("existing target was deleted after failed preflight: %v", err)
	}
}
