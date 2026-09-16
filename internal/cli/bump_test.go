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
