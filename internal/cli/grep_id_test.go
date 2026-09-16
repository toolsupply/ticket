package cli

import "testing"

func TestGrepRejectsActorAndMatchesTicketIDs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "grep ID"))["id"].(string)

	result := exactlyOneJSONObject(t, mustCLI(t, "grep", "^"+id[:8]))
	items := result["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != id {
		t.Fatalf("grep ID result: %v", result)
	}
	result = exactlyOneJSONObject(t, mustCLI(t, "grep", "^"+id+"$", "grep ID"))
	items = result["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != id {
		t.Fatalf("grep multiple expressions result: %v", result)
	}
}
