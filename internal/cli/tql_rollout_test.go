package cli

import "testing"

func TestTQLShellFriendlyArgvAndLegacyStateCompatibility(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	assigned := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "assigned", "Shell-safe query target.", "--priority", "1", "--tag", "release")), "id")
	mustCLI(t, "claim", assigned, "--actor", "coder")

	// These argv slices represent the words emitted by POSIX shells,
	// PowerShell, and cmd.exe for the documented shell-friendly forms.
	for _, args := range [][]string{
		{"query", "state:open", "and", "assignee:coder"},
		{"query", "tag:release", "priority", "le", "P2"},
	} {
		items := queryJSONItems(t, mustCLI(t, args...))
		if len(items) != 1 || items[0].(map[string]any)["id"] != assigned {
			t.Fatalf("shell-safe argv %v selected %v", args, items)
		}
	}

	tail := queryJSONItems(t, mustCLIJSONBeforeTail(t, "list", "-q", "state:open", "and", "assignee:coder"))
	if len(tail) != 1 || tail[0].(map[string]any)["id"] != assigned {
		t.Fatalf("shell-safe query tail selected %v", tail)
	}

	legacy := stringValue(t, exactlyOneJSONObject(t, mustCLI(t, "create", "legacy", "Retain old lifecycle spelling.")), "id")
	mustCLI(t, "state", legacy, "completed")
	closed := queryJSONItems(t, mustCLI(t, "query", "state:closed"))
	completed := queryJSONItems(t, mustCLI(t, "query", "state:completed"))
	if len(closed) != 1 || len(completed) != 1 || closed[0].(map[string]any)["id"] != legacy || completed[0].(map[string]any)["id"] != legacy {
		t.Fatalf("legacy lifecycle query mismatch: closed=%v completed=%v", closed, completed)
	}
	if completed[0].(map[string]any)["state"] != "closed" {
		t.Fatalf("legacy lifecycle output was not canonical: %v", completed[0])
	}
}

func TestListQueryTailUsesGenericParserBoundary(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	mustCLI(t, "create", "tail", "Exercise list query parsing.")

	for _, value := range []string{"-q", "--query"} {
		items := queryJSONItems(t, mustCLIJSONBeforeTail(t, "list", "--assignee", value))
		if len(items) != 0 {
			t.Fatalf("option value %q was treated as a query boundary: %v", value, items)
		}
	}

	items := queryJSONItems(t, mustCLIJSONBeforeTail(t, "list", "-q", "state:open", "and", "not", "terminal"))
	if len(items) != 1 {
		t.Fatalf("multi-token list query tail: %v", items)
	}
}
