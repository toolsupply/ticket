package cli

import (
	"strings"
	"testing"
)

func TestLeanOwnership(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	created := runCLIStdinMust(t, `{"title":"lean ownership","sections":{"objective":"work","acceptance":"done"}}`, "create", "--input", "-")
	id := exactlyOneJSONObject(t, created)["id"].(string)
	before := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))

	// The lean contract permits an unguarded mutation.
	claim := exactlyOneJSONObject(t, mustCLI(t, "claim", id, "--actor", "alice"))
	if claim["assignee"] != "alice" || claim["changed"] != true {
		t.Fatalf("claim: %v", claim)
	}
	if out, code := runCLI(t, "claim", id, "--actor", "bob"); code == 0 || !strings.Contains(out, "already_claimed") {
		t.Fatalf("other actor claim: exit=%d %q", code, out)
	}
	if out, code := runCLIStdin(t, `{"set":{"title":"wrong owner"}}`, "update", id, "--input", "-", "--actor", "bob"); code == 0 || !strings.Contains(out, "already_claimed") {
		t.Fatalf("other actor update: exit=%d %q", code, out)
	}
	if out, code := runCLI(t, "release", id, "--actor", "bob"); code == 0 || !strings.Contains(out, "already_claimed") {
		t.Fatalf("other actor release: exit=%d %q", code, out)
	}
	claimed := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if claimed["assignee"] != "alice" {
		t.Fatalf("claim did not persist exact ownership state: before=%v after=%v", before, claimed)
	}
	release := exactlyOneJSONObject(t, mustCLI(t, "release", id, "--actor", "alice"))
	if release["changed"] != true {
		t.Fatalf("release: %v", release)
	}
	released := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if released["assignee"] != nil {
		t.Fatalf("release did not restore exact state: before=%v after=%v", before, released)
	}

	if before["id"] != id {
		t.Fatal("show result lost id")
	}
}

func TestLeanReleaseHandoffInputPresence(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	created := runCLIStdinMust(t, `{"title":"handoff","sections":{"objective":"work","acceptance":"done"}}`, "create", "--input", "-")
	id := exactlyOneJSONObject(t, created)["id"].(string)
	claim := exactlyOneJSONObject(t, mustCLI(t, "claim", id, "--actor", "alice"))
	runCLIStdinMust(t, `{"handoff":"leave this"}`, "release", id, "--input", "-", "--actor", "alice")
	body := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))["body"].(string)
	if !strings.Contains(body, "leave this") {
		t.Fatalf("handoff missing: %q", body)
	}
	claim = exactlyOneJSONObject(t, mustCLI(t, "claim", id, "--actor", "alice"))
	if _, code := runCLIStdin(t, `{"handoff":""}`, "release", id, "--input", "-", "--actor", "alice"); code != 0 {
		t.Fatalf("clear handoff failed")
	}
	body = exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))["body"].(string)
	if strings.Contains(body, "leave this") {
		t.Fatalf("handoff was not cleared: %q", body)
	}
	_ = claim
}

func mustCLI(t *testing.T, args ...string) string {
	t.Helper()
	out, code := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("cli %v: exit=%d out=%q", args, code, out)
	}
	return out
}

func runCLIStdinMust(t *testing.T, input string, args ...string) string {
	t.Helper()
	out, code := runCLIStdin(t, input, args...)
	if code != 0 {
		t.Fatalf("cli %v: exit=%d out=%q", args, code, out)
	}
	return out
}
