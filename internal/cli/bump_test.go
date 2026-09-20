package cli

import "testing"

func TestBumpLowersPriorityToZeroAndThenNoOps(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Bump priority", "Lower the numeric priority."))
	id := created["id"].(string)
	if out, code := runCLIStdin(t, `{"set":{"priority":3}}`, "update", id, "--input", "-"); code != 0 {
		t.Fatalf("set priority: exit=%d out=%q", code, out)
	}
	for want := 2; want >= 0; want-- {
		result := exactlyOneJSONObject(t, mustCLI(t, "bump", id))
		if result["changed"] != true {
			t.Fatalf("bump %d result: %v", want, result)
		}
		fields := result["changed_fields"].([]any)
		if len(fields) != 1 || fields[0] != "priority" {
			t.Fatalf("bump fields: %v", result)
		}
		view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
		if view["priority"] != float64(want) {
			t.Fatalf("priority=%v want %d", view["priority"], want)
		}
	}
	result := exactlyOneJSONObject(t, mustCLI(t, "bump", id))
	if result["changed"] != false || len(result["changed_fields"].([]any)) != 0 {
		t.Fatalf("priority zero should be a no-op: %v", result)
	}
}

func TestBumpPriorityFlagSetsExactPriority(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Set priority", "Use an exact bump priority."))["id"].(string)
	result := exactlyOneJSONObject(t, mustCLI(t, "bump", id, "-p", "4"))
	if result["changed"] != true {
		t.Fatalf("exact bump result: %v", result)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if view["priority"] != float64(4) {
		t.Fatalf("priority=%v want 4", view["priority"])
	}
	if out, code := runCLI(t, "bump", id, "--priority", "5"); code != 2 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("invalid exact priority: exit=%d out=%q", code, out)
	}
}

func TestUpdatePriorityFlagSetsPriority(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Update priority", "Set priority directly."))["id"].(string)
	if out, code := runCLI(t, "update", id, "--priority", "1"); code != 0 {
		t.Fatalf("update priority: exit=%d out=%q", code, out)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
	if view["priority"] != float64(1) {
		t.Fatalf("priority=%v want 1", view["priority"])
	}
	if out, code := runCLI(t, "update", id, "--input", "-", "--priority", "2"); code != 2 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("mixed update sources: exit=%d out=%q", code, out)
	}
}
