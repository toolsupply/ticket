package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONCommandsDoNotChangeHumanCurrentTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	a := exactlyOneJSONObject(t, mustCLI(t, "create", "Human current"))["id"].(string)
	b := exactlyOneJSONObject(t, mustCLI(t, "create", "Agent claim", "Claim this ticket."))["id"].(string)
	exactlyOneJSONObject(t, mustCLI(t, "create", "Another agent ticket", "Select this ticket."))

	if _, code := runCLIHuman(t, "show", a); code != 0 {
		t.Fatalf("select human current: exit=%d", code)
	}
	marker := filepath.Join(dir, "tickets", ".local", "current")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}

	if out, code := runCLI(t, "show", b); code != 0 {
		t.Fatalf("JSON show: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "claim", b, "--actor", "worker"); code != 0 {
		t.Fatalf("JSON claim: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "next", "--claim", "--actor", "worker-2"); code != 0 {
		t.Fatalf("JSON next --claim: exit=%d out=%q", code, out)
	}

	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("JSON commands changed human current: before=%q after=%q", before, after)
	}

	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, "show", b); code != 0 {
		t.Fatalf("JSON show without marker: exit=%d out=%q", code, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("JSON command created current marker: %v", err)
	}
}

func TestHumanExplicitTicketStillUpdatesCurrent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Human selection", "Keep human convenience."))["id"].(string)
	marker := filepath.Join(dir, "tickets", ".local", "current")
	if _, code := runCLIHuman(t, "show", id); code != 0 {
		t.Fatalf("human show: exit=%d", code)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != id+"\n" {
		t.Fatalf("human current marker=%q want %q", data, id+"\n")
	}
}

func TestJSONReviewWaitPreservesHumanCurrentAndImplicitSelection(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	a := exactlyOneJSONObject(t, mustCLI(t, "create", "Human ticket", "Keep this selected."))["id"].(string)
	b := exactlyOneJSONObject(t, mustCLI(t, "create", "Worker ticket", "Claim this as a worker."))["id"].(string)
	c := exactlyOneJSONObject(t, mustCLI(t, "create", "Review ticket", "Send this to review."))["id"].(string)

	if _, code := runCLIHuman(t, "show", a); code != 0 {
		t.Fatalf("select human current: exit=%d", code)
	}
	marker := filepath.Join(dir, "tickets", ".local", "current")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}

	mustCLI(t, "claim", b, "--actor", "worker")
	mustCLI(t, "claim", c, "--actor", "implementer")
	mustCLI(t, "submit", c, "--actor", "implementer")
	waited := exactlyOneJSONObject(t, mustCLI(t, "wait", "review", "--claim", "--actor", "reviewer"))
	item := waited["item"].(map[string]any)
	if item["id"] != c || item["assignee"] != "reviewer" {
		t.Fatalf("review wait selected wrong ticket: %v", waited)
	}

	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("JSON worker/reviewer operations changed human current: before=%q after=%q", before, after)
	}
	status, code := runCLIHuman(t, "status")
	if code != 0 || !strings.Contains(status, a) {
		t.Fatalf("implicit human status did not retain ticket %s: exit=%d out=%q", a, code, status)
	}
}

func TestJSONReviewWaitDoesNotCreateCurrentMarker(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Unselected review", "Keep current absent."))["id"].(string)
	mustCLI(t, "claim", id, "--actor", "implementer")
	mustCLI(t, "submit", id, "--actor", "implementer")
	marker := filepath.Join(dir, "tickets", ".local", "current")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("JSON setup unexpectedly created current marker: %v", err)
	}

	waited := exactlyOneJSONObject(t, mustCLI(t, "wait", "review", "--claim", "--actor", "reviewer"))
	if item := waited["item"].(map[string]any); item["id"] != id {
		t.Fatalf("review wait selected wrong ticket: %v", waited)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("JSON review wait created current marker: %v", err)
	}
}
