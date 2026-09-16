package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWaitUsesNextSelectionAndResumesActor(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Wait work", "Work for the waiting supervisor."))
	id := created["id"].(string)

	waiting := exactlyOneJSONObject(t, mustCLI(t, "wait"))
	item := waiting["item"].(map[string]any)
	if item["id"] != id || item["title"] != "Wait work" || item["state"] != "open" {
		t.Fatalf("wait selected wrong item: %v", waiting)
	}

	claimed := exactlyOneJSONObject(t, mustCLI(t, "wait", "--claim", "--actor", "worker"))
	claimedItem := claimed["item"].(map[string]any)
	if claimedItem["id"] != id || claimedItem["assignee"] != "worker" {
		t.Fatalf("wait claim: %v", claimed)
	}
	resumed := exactlyOneJSONObject(t, mustCLI(t, "next", "--claim", "--actor", "worker"))
	resumedItem := resumed["item"].(map[string]any)
	if resumedItem["id"] != id || resumedItem["assignee"] != "worker" {
		t.Fatalf("next did not resume actor ticket: %v", resumed)
	}

	marker := filepath.Join(dir, "tickets", ".local", "change")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read change marker: %v", err)
	}
	if _, code := runCLI(t, "show", id); code != 0 {
		t.Fatal("show failed")
	}
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read change marker after show: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("read-only show changed marker: before=%q after=%q", before, after)
	}
}

func TestNextClaimRejectsMultipleActiveActorTickets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	create := func(title string) string {
		created := exactlyOneJSONObject(t, mustCLI(t, "create", title, "Do this work."))
		return created["id"].(string)
	}
	first, second := create("First"), create("Second")
	if out, code := runCLI(t, "claim", first, "--actor", "worker"); code != 0 {
		t.Fatalf("claim first: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "claim", second, "--actor", "worker"); code != 0 {
		t.Fatalf("claim second: exit=%d out=%q", code, out)
	}
	out, code := runCLI(t, "next", "--claim", "--actor", "worker")
	if code != 4 || errCode(t, out) != "conflict" {
		t.Fatalf("multiple active tickets: exit=%d out=%q", code, out)
	}
}
