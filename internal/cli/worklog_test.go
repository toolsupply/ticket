package cli

import (
	"strings"
	"testing"
)

func TestWorkflowMessageCLIAppendsWorkLog(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TICKET_ACTOR", "human")
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Message workflow", "Exercise workflow messages."))
	id := created["id"].(string)

	if out, code := runCLI(t, "submit", id, "-m", "Implemented the first pass."); code != 0 {
		t.Fatalf("submit message: exit=%d out=%q", code, out)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	body := view["body"].(string)
	if !strings.Contains(body, "## Work log") || !strings.Contains(body, "human: Implemented the first pass.") {
		t.Fatalf("submit message missing from Work log: %q", body)
	}

	t.Setenv("TICKET_ACTOR", "reviewer")
	if out, code := runCLI(t, "claim", id); code != 0 {
		t.Fatalf("claim review: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "approve", id, "--message", "Reviewed the first pass."); code != 0 {
		t.Fatalf("approve message: exit=%d out=%q", code, out)
	}
	view = exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	body = view["body"].(string)
	if !strings.Contains(body, "reviewer: Reviewed the first pass.") || strings.Count(body, "- 20") < 2 {
		t.Fatalf("approve message was not appended: %q", body)
	}
}
