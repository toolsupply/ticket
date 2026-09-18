package cli

import (
	"strings"
	"testing"
)

func TestFullIDObjectFirstDispatch(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Object first", "Exercise object-first dispatch."))["id"].(string)

	shown := exactlyOneJSONObject(t, mustCLI(t, id))
	if shown["id"] != id || shown["title"] != "Object first" {
		t.Fatalf("object-first show: %v", shown)
	}
	shown = exactlyOneJSONObject(t, mustCLI(t, id, "show", "--full"))
	if shown["id"] != id || !strings.Contains(shown["body"].(string), "## Objective") {
		t.Fatalf("object-first explicit show: %v", shown)
	}

	held := exactlyOneJSONObject(t, mustCLI(t, id, "hold"))
	if held["id"] != id || held["state"] != "hold" {
		t.Fatalf("object-first hold: %v", held)
	}

	prefix := id[:len(id)-1]
	if out, code := runCLI(t, prefix, "open"); code == 0 {
		t.Fatalf("ticket prefix activated object-first dispatch: %q", out)
	}
	current := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if current["state"] != "hold" {
		t.Fatalf("prefix changed ticket state: %v", current)
	}
	if out, code := runCLI(t, id, "list"); code == 0 {
		t.Fatalf("collection command accepted after object-first ID: %q", out)
	}
}

func TestRejectHelpIsNotARejectOutcome(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Reject help", "Help must not mutate."))["id"].(string)

	out, code := runCLIHuman(t, "reject", "help")
	if code != 0 || !strings.Contains(out, "reject -") {
		t.Fatalf("reject help: exit=%d output=%q", code, out)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if view["state"] != "open" {
		t.Fatalf("reject help changed ticket: %v", view)
	}
}
