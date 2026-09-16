package cli

import (
	"strings"
	"testing"
)

func TestListAcceptsPositionalTicketIDs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	first := exactlyOneJSONObject(t, mustCLI(t, "create", "List by ID", "Select this ticket by its ID."))["id"].(string)
	short := first[:len(first)-1]
	listed := exactlyOneJSONObject(t, mustCLI(t, "list", short))
	items := listed["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != first {
		t.Fatalf("list by shorthand: %v", listed)
	}
	second := exactlyOneJSONObject(t, mustCLI(t, "create", "Other ticket", "Keep this ticket out of the result."))["id"].(string)
	if _, code := runCLI(t, "close", second); code != 0 {
		t.Fatalf("close second: exit=%d", code)
	}

	listed = exactlyOneJSONObject(t, mustCLI(t, "list", second))
	items = listed["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != second {
		t.Fatalf("list by full ID: %v", listed)
	}
	if items[0].(map[string]any)["state"] != "completed" {
		t.Fatalf("list by ID should include the selected terminal ticket: %v", items[0])
	}

	listed = exactlyOneJSONObject(t, mustCLI(t, "list", first[:8]))
	items = listed["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"] != first || items[1].(map[string]any)["id"] != second {
		t.Fatalf("list by date prefix: %v", listed)
	}

	listed = exactlyOneJSONObject(t, mustCLI(t, "list", "20260101-00000"))
	if items, ok := listed["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("missing list prefix: %v", listed)
	}
}

func TestListPrefixCanMatchMultipleTickets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	first := exactlyOneJSONObject(t, mustCLI(t, "create", "first"))["id"].(string)
	second := exactlyOneJSONObject(t, mustCLI(t, "create", "second"))["id"].(string)
	prefix := first[:len(first)-2]
	if !strings.HasPrefix(second, prefix) {
		t.Skip("deterministic fixture IDs do not share a prefix")
	}
	listed := exactlyOneJSONObject(t, mustCLI(t, "list", prefix))
	items := listed["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("list by shared prefix: %v", listed)
	}
}
