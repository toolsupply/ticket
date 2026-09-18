package cli

import "testing"

func TestCreateTagDefaultsAreAdditiveAndDeduplicated(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TICKET_CREATE_TAGS", "windows, networking, windows")
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Tagged", "Has an objective.", "--tag", "networking", "--tag", "urgent"))
	id := created["id"].(string)
	shown := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	tags := shown["tags"].([]any)
	if len(tags) != 3 || tags[0] != "networking" || tags[1] != "urgent" || tags[2] != "windows" {
		t.Fatalf("created tags: %v", tags)
	}
}

func TestWorkTagDefaultsFilterNextAndWaitButNotList(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TICKET_CREATE_TAGS", "")
	t.Setenv("TICKET_WORK_TAGS", "windows, networking")
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	both := exactlyOneJSONObject(t, mustCLI(t, "create", "Both", "Ready.", "--tag", "windows", "--tag", "networking"))["id"].(string)
	mustCLI(t, "create", "Windows", "Ready.", "--tag", "windows")
	mustCLI(t, "create", "Networking", "Ready.", "--tag", "networking")

	listed := exactlyOneJSONObject(t, mustCLI(t, "list", "--tag", "networking", "--fields", "id,tags"))
	items := listed["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("list should ignore work defaults: %v", listed)
	}

	next := exactlyOneJSONObject(t, mustCLI(t, "next"))
	if got := next["item"].(map[string]any)["id"]; got != both {
		t.Fatalf("next with work defaults: got %v want %s", got, both)
	}
	wait := exactlyOneJSONObject(t, mustCLI(t, "wait"))
	if got := wait["item"].(map[string]any)["id"]; got != both {
		t.Fatalf("wait with work defaults: got %v want %s", got, both)
	}
}

func TestWorkTagDefaultsApplyToReviewQueue(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("TICKET_WORK_TAGS", "windows, networking")
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	matching := exactlyOneJSONObject(t, mustCLI(t, "create", "Matching", "Ready.", "--tag", "windows", "--tag", "networking"))["id"].(string)
	if out, code := runCLI(t, "claim", matching, "--actor", "coder"); code != 0 {
		t.Fatalf("claim: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "submit", matching, "--actor", "coder"); code != 0 {
		t.Fatalf("submit: exit=%d out=%q", code, out)
	}
	other := exactlyOneJSONObject(t, mustCLI(t, "create", "Other", "Ready.", "--tag", "windows"))["id"].(string)
	if out, code := runCLI(t, "claim", other, "--actor", "coder"); code != 0 {
		t.Fatalf("claim other: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "submit", other, "--actor", "coder"); code != 0 {
		t.Fatalf("submit other: exit=%d out=%q", code, out)
	}
	selected := exactlyOneJSONObject(t, mustCLI(t, "next", "review"))
	if got := selected["item"].(map[string]any)["id"]; got != matching {
		t.Fatalf("review next with work defaults: got %v want %s", got, matching)
	}
}
