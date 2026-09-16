package cli

import (
	"strings"
	"testing"
)

func TestMarkdownOutputForQueryAndTicketCommands(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Markdown output", "Exercise Markdown output."))
	id := created["id"].(string)
	if out, code := runCLI(t, "list", "-m"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("-m was still accepted as a Markdown alias: exit=%d out=%q", code, out)
	}

	list, code := runCLIHuman(t, "list", "--markdown")
	if code != 0 || !strings.Contains(list, "| State | Pri | ID | Title | Assignee |\n") || !strings.Contains(list, "| open | P2 | "+id+" | Markdown output |  |") {
		t.Fatalf("markdown list: exit=%d out=%q", code, list)
	}
	ready, code := runCLIHuman(t, "ready", "--markdown")
	if code != 0 || !strings.Contains(ready, "| open | P2 | "+id+" | Markdown output |  |") {
		t.Fatalf("markdown ready: exit=%d out=%q", code, ready)
	}
	next, code := runCLIHuman(t, "next", "--markdown")
	if code != 0 || !strings.Contains(next, "| Pri | ID | Title | Assignee |\n") || !strings.Contains(next, "| P2 | "+id+" | Markdown output |  |") {
		t.Fatalf("markdown next: exit=%d out=%q", code, next)
	}
	status, code := runCLIHuman(t, "status", id, "--markdown")
	if code != 0 || !strings.Contains(status, "## "+id+"\n") || !strings.Contains(status, "- State: open\n- Priority: P2\n") {
		t.Fatalf("markdown status: exit=%d out=%q", code, status)
	}
	show, code := runCLIHuman(t, "show", id, "--markdown")
	if code != 0 || !strings.Contains(show, "### Objective\n\nExercise Markdown output.") {
		t.Fatalf("markdown show: exit=%d out=%q", code, show)
	}
	jsonOutput, code := runCLI(t, "list", "--markdown")
	if code != 0 {
		t.Fatalf("markdown JSON list: exit=%d out=%q", code, jsonOutput)
	}
	result := exactlyOneJSONObject(t, jsonOutput)
	if _, ok := result["items"]; !ok {
		t.Fatalf("markdown flag changed JSON contract: %v", result)
	}
}
