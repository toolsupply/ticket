package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateAcceptsExplicitInitialStateAndAssignee(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}

	out, code := runCLIStdin(t, `{"title":"Complete held draft","state":"hold","assignee":"reviewer","sections":{"objective":"Do the work.","acceptance":"The result is complete."}}`, "create", "--input", "-")
	if code != 0 {
		t.Fatalf("held create: exit=%d out=%q", code, out)
	}
	heldID := exactlyOneJSONObject(t, out)["id"].(string)
	held := exactlyOneJSONObject(t, mustCLI(t, "status", heldID))
	if held["state"] != "hold" || held["assignee"] != "reviewer" {
		t.Fatalf("held ticket state/assignee: %v", held)
	}
	heldDetails := exactlyOneJSONObject(t, mustCLI(t, "show", heldID))
	sections := heldDetails["sections"].(map[string]any)
	if strings.TrimSpace(sections["objective"].(map[string]any)["text"].(string)) == "" || strings.TrimSpace(sections["acceptance"].(map[string]any)["text"].(string)) == "" {
		t.Fatalf("held ticket sections missing: %v", sections)
	}
	selected := exactlyOneJSONObject(t, mustCLI(t, "next"))
	if selected["item"] != nil {
		t.Fatalf("held ticket became implementation work: %v", selected)
	}
	reviewSelection := exactlyOneJSONObject(t, mustCLI(t, "next", "review"))
	if reviewSelection["item"] != nil {
		t.Fatalf("held ticket became review work: %v", reviewSelection)
	}

	openOut, openCode := runCLI(t, "create", "Open ticket", "Do open work.", "--state", "open")
	if openCode != 0 {
		t.Fatalf("explicit open create: exit=%d out=%q", openCode, openOut)
	}
	openID := exactlyOneJSONObject(t, openOut)["id"].(string)
	open := exactlyOneJSONObject(t, mustCLI(t, "status", openID))
	if open["state"] != "open" || open["assignee"] != nil {
		t.Fatalf("explicit open ticket: %v", open)
	}

	reviewOut, reviewCode := runCLI(t, "create", "Review ticket", "Review this work.", "--state", "review", "--assignee", "reviewer-2")
	if reviewCode != 0 {
		t.Fatalf("explicit review create: exit=%d out=%q", reviewCode, reviewOut)
	}
	reviewID := exactlyOneJSONObject(t, reviewOut)["id"].(string)
	review := exactlyOneJSONObject(t, mustCLI(t, "status", reviewID))
	if review["state"] != "review" || review["assignee"] != "reviewer-2" {
		t.Fatalf("explicit review ticket: %v", review)
	}
}

func TestCreateInvalidInitialMetadataDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}

	for _, body := range []string{
		`{"title":"Bad state","state":"unknown","sections":{"objective":"Do work."}}`,
		`{"title":"Bad assignee","assignee":"bad actor","sections":{"objective":"Do work."}}`,
	} {
		out, code := runCLIStdin(t, body, "create", "--input", "-")
		if code == 0 || errCode(t, out) != "invalid_argument" {
			t.Fatalf("invalid create accepted: exit=%d out=%q", code, out)
		}
		items := exactlyOneJSONObject(t, mustCLI(t, "list", "--all"))["items"].([]any)
		if len(items) != 0 {
			t.Fatalf("invalid create published a ticket: %v", items)
		}
	}
}

func TestCreateEditorPreservesExplicitOpenState(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	editor := installTestHelper(t, filepath.Join(dir, "editor-helper"), "editor-append")
	t.Setenv("EDITOR", editor)
	t.Setenv("TEST_EDITOR_BODY", "")

	created, code := runCLIHuman(t, "create", "Explicit open", "--state", "open", "--edit")
	if code != 0 {
		t.Fatalf("editor create: exit=%d out=%q", code, created)
	}
	fields := strings.Fields(created)
	if len(fields) < 2 || fields[0] != "created" {
		t.Fatalf("editor create output: %q", created)
	}
	status := exactlyOneJSONObject(t, mustCLI(t, "status", fields[1]))
	if status["state"] != "open" {
		t.Fatalf("explicit open state was normalized during editor create: %v", status)
	}
}
