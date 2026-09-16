package cli

import "testing"

func errCode(t *testing.T, out string) string {
	t.Helper()
	m := exactlyOneJSONObject(t, out)
	er, _ := m["error"].(map[string]any)
	if er == nil {
		t.Fatalf("no error object in %q", out)
	}
	code, _ := er["code"].(string)
	return code
}

// C12: update through the CLI with --input.
func TestUpdateCLI(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: %q", out)
	}
	out, code := runCLI(t, "create", "--title", "Update me")
	if code != 0 {
		t.Fatalf("create: %q", out)
	}
	m := exactlyOneJSONObject(t, out)
	id := m["id"].(string)
	// Missing --input.
	out, code = runCLI(t, "update", id)
	if code == 0 {
		t.Fatalf("expected missing input: %q", out)
	}
	if got := errCode(t, out); got != "invalid_argument" {
		t.Fatalf("missing input code=%s", got)
	}

	// Valid update: title + priority.
	out, code = runCLIStdin(t, `{"set":{"title":"Updated","priority":1}}`, "update", id, "--input", "-")
	if code != 0 {
		t.Fatalf("update: %q (exit %d)", out, code)
	}
	m = exactlyOneJSONObject(t, out)
	if m["id"] != id {
		t.Fatalf("update id: %v", m)
	}
	changedFields, _ := m["changed_fields"].([]any)
	if len(changedFields) != 2 {
		t.Fatalf("changed_fields: %v", m)
	}
	if m["changed"] != true {
		t.Fatalf("changed: %v", m)
	}
	// Semantic no-op leaves the file unchanged.
	out, code = runCLIStdin(t, `{"set":{"title":"Updated","priority":1}}`, "update", id, "--input", "-")
	if code != 0 {
		t.Fatalf("noop update: %q", out)
	}
	m = exactlyOneJSONObject(t, out)
	if m["changed"] != false {
		t.Fatalf("noop: %v", m)
	}
	// Unknown input key.
	out, code = runCLIStdin(t, `{"set":{},"bogus":1}`, "update", id, "--input", "-")
	if code == 0 {
		t.Fatalf("expected unknown key: %q", out)
	}
	if got := errCode(t, out); got != "invalid_argument" {
		t.Fatalf("unknown key code=%s", got)
	}
}
