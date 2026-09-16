package cli

import "testing"

func TestReviewQueueSelectsClaimsAndResumesReviewWork(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Review this", "Review the implementation.", "--tag", "security", "--tag", " linux "))
	id := created["id"].(string)
	if out, code := runCLI(t, "claim", id, "--actor", "coder"); code != 0 {
		t.Fatalf("claim: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "submit", id, "--actor", "coder"); code != 0 {
		t.Fatalf("submit: exit=%d out=%q", code, out)
	}

	selected := exactlyOneJSONObject(t, mustCLI(t, "next", "review", "--tag", "security"))
	item := selected["item"].(map[string]any)
	if item["id"] != id || item["state"] != "review" {
		t.Fatalf("review selection: %v", selected)
	}
	if got := item["tags"].([]any); len(got) != 2 || got[0] != "linux" || got[1] != "security" {
		t.Fatalf("review tags: %v", got)
	}

	claimed := exactlyOneJSONObject(t, mustCLI(t, "next", "review", "--claim", "--actor", "reviewer", "--tag", "security"))
	claimedItem := claimed["item"].(map[string]any)
	if claimedItem["id"] != id || claimedItem["assignee"] != "reviewer" || claimedItem["state"] != "review" {
		t.Fatalf("review claim: %v", claimed)
	}
	resumed := exactlyOneJSONObject(t, mustCLI(t, "next", "review", "--claim", "--actor", "reviewer", "--tag", "linux"))
	resumedItem := resumed["item"].(map[string]any)
	if resumedItem["id"] != id || resumedItem["assignee"] != "reviewer" || resumedItem["state"] != "review" {
		t.Fatalf("review resume: %v", resumed)
	}
}

func TestNextTagsUseANDAndRejectOwnedMismatch(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	first := exactlyOneJSONObject(t, mustCLI(t, "create", "Both tags", "Do both.", "--priority", "1", "--tag", "windows", "--tag", "networking"))["id"].(string)
	second := exactlyOneJSONObject(t, mustCLI(t, "create", "One tag", "Do one.", "--tag", "windows"))["id"].(string)
	ready := exactlyOneJSONObject(t, mustCLI(t, "ready", "--tag", "networking"))
	readyItems := ready["items"].([]any)
	if len(readyItems) != 1 || readyItems[0].(map[string]any)["id"] != first {
		t.Fatalf("ready tag filter: %v", ready)
	}
	selected := exactlyOneJSONObject(t, mustCLI(t, "next", "--tag", "windows", "--tag", "networking"))
	if got := selected["item"].(map[string]any)["id"]; got != first {
		t.Fatalf("AND tag selection: got %v want %s", got, first)
	}
	if out, code := runCLI(t, "next", "--claim", "--actor", "worker", "--tag", "windows", "--tag", "networking"); code != 0 {
		t.Fatalf("claim filtered work: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "update", second, "--tag", "windows", "--tag", "networking"); code != 0 {
		t.Fatalf("update tags: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "next", "--claim", "--actor", "worker", "--tag", "linux"); code != 4 || errCode(t, out) != "conflict" {
		t.Fatalf("owned filter mismatch: exit=%d out=%q", code, out)
	}
	// The one-tag ticket remains unclaimed and is not silently consumed by the
	// actor whose existing ticket failed its requested filter.
	listed := exactlyOneJSONObject(t, mustCLI(t, "list", second, "--fields", "id,assignee"))
	if len(listed["items"].([]any)) != 1 {
		t.Fatalf("unexpected one-tag result: %v", listed)
	}
}

func TestReviewQueueIgnoresImplementationReadiness(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Needs review", "Has an objective."))["id"].(string)
	if out, code := runCLI(t, "claim", id, "--actor", "coder"); code != 0 {
		t.Fatalf("claim: exit=%d out=%q", code, out)
	}
	// Clear the objective after claiming, then submit. Review selection is
	// governed by the explicit review state, not implementation readiness.
	if out, code := runCLIStdin(t, `{"sections":{"objective":""}}`, "update", id, "--input", "-", "--actor", "coder"); code != 0 {
		t.Fatalf("clear objective: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "submit", id, "--actor", "coder"); code != 0 {
		t.Fatalf("submit: exit=%d out=%q", code, out)
	}
	selected := exactlyOneJSONObject(t, mustCLI(t, "wait", "review", "--claim", "--actor", "reviewer"))
	selectedItem := selected["item"].(map[string]any)
	if selectedItem["id"] != id || selectedItem["assignee"] != "reviewer" {
		t.Fatalf("review wait selection: %v", selected)
	}
}

func TestWorkQueueRejectsUnknownQueue(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "next", "completed"); code != 2 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("unknown queue: exit=%d out=%q", code, out)
	}
}
