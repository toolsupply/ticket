package cli

import "testing"

func TestArchiveSupportsTQLBatchTargetsAndArchiveBoundary(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	first := exactlyOneJSONObject(t, mustCLI(t, "create", "first archive", "Archive this.", "--tag", "batch"))["id"].(string)
	second := exactlyOneJSONObject(t, mustCLI(t, "create", "second archive", "Archive this too.", "--tag", "batch"))["id"].(string)
	for _, id := range []string{first, second} {
		mustCLI(t, "close", id)
	}

	result := exactlyOneJSONObject(t, mustCLI(t, "archive", "terminal"))
	items := result["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("terminal archive: %v", result)
	}
	secondRun := exactlyOneJSONObject(t, mustCLI(t, "archive", "terminal"))
	if items := secondRun["items"].([]any); len(items) != 0 {
		t.Fatalf("archived tickets leaked into active archive query: %v", secondRun)
	}

	empty := exactlyOneJSONObject(t, mustCLI(t, "archive", "state:closed", "unclaimed"))
	if items := empty["items"].([]any); len(items) != 0 {
		t.Fatalf("empty archive selection: %v", empty)
	}

	third := exactlyOneJSONObject(t, mustCLI(t, "create", "query-tail archive", "Archive from a tail.", "--tag", "tail"))["id"].(string)
	mustCLI(t, "close", third)
	tail := exactlyOneJSONObject(t, mustCLIJSONBeforeTail(t, "archive", "-q", "state:closed", "unclaimed"))
	if items := tail["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != third {
		t.Fatalf("query-tail archive: %v", tail)
	}

	valid := exactlyOneJSONObject(t, mustCLI(t, "create", "valid archive batch", "Close this first.", "--tag", "mixed"))["id"].(string)
	mustCLI(t, "close", valid)
	bad := exactlyOneJSONObject(t, mustCLI(t, "create", "invalid archive batch", "Keep active.", "--tag", "mixed"))["id"].(string)
	if out, code := runCLI(t, "archive", valid, "tag:mixed"); code == 0 || errCode(t, out) != "invalid_transition" {
		t.Fatalf("invalid archive batch accepted: %q", out)
	}
	if listed := queryJSONItems(t, mustCLI(t, "query", "state:open")); len(listed) != 1 || listed[0].(map[string]any)["id"] != bad {
		t.Fatalf("invalid archive batch changed active ticket: %v", listed)
	}
	if listed := queryJSONItems(t, mustCLI(t, "query", "state:closed")); len(listed) != 1 || listed[0].(map[string]any)["id"] != valid {
		t.Fatalf("invalid archive batch moved valid ticket: %v", listed)
	}
}
