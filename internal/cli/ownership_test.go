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

func TestReassignPreservesStateAndOwnershipAtomically(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")

	created := runCLIStdinMust(t, `{"title":"reassign open","sections":{"objective":"work","acceptance":"done"}}`, "create", "--input", "-")
	id := exactlyOneJSONObject(t, created)["id"].(string)
	mustCLI(t, "claim", id, "--actor", "coder")
	if out, code := runCLI(t, "reassign", id, "user", "--actor", "other"); code == 0 || !strings.Contains(out, "already_claimed") {
		t.Fatalf("other actor reassign: exit=%d %q", code, out)
	}
	reassigned := exactlyOneJSONObject(t, mustCLI(t, "reassign", id, "user", "--handoff", "continue", "-m", "transferred", "--actor", "coder"))
	if reassigned["changed"] != true || reassigned["state"] != "open" || reassigned["from_assignee"] != "coder" || reassigned["assignee"] != "user" {
		t.Fatalf("open reassignment: %v", reassigned)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if view["state"] != "open" || view["assignee"] != "user" || !strings.Contains(view["body"].(string), "continue") || !strings.Contains(view["body"].(string), "transferred") {
		t.Fatalf("open reassignment lost state or context: %v", view)
	}
	next := exactlyOneJSONObject(t, mustCLI(t, "next", "--claim", "--actor", "coder"))
	if next["item"] != nil {
		t.Fatalf("assigned ticket became selectable for another actor: %v", next)
	}

	created = runCLIStdinMust(t, `{"title":"reassign review","sections":{"objective":"work","acceptance":"done"}}`, "create", "--input", "-")
	reviewID := exactlyOneJSONObject(t, created)["id"].(string)
	mustCLI(t, "claim", reviewID, "--actor", "coder")
	mustCLI(t, "submit", reviewID, "--actor", "coder")
	mustCLI(t, "claim", reviewID, "--actor", "reviewer")
	reviewed := exactlyOneJSONObject(t, mustCLI(t, "reassign", reviewID, "user", "--actor", "reviewer"))
	if reviewed["state"] != "review" || reviewed["assignee"] != "user" {
		t.Fatalf("review reassignment changed lifecycle: %v", reviewed)
	}
}

func TestReassignJSONInput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	created := runCLIStdinMust(t, `{"title":"reassign json","sections":{"objective":"work","acceptance":"done"}}`, "create", "--input", "-")
	id := exactlyOneJSONObject(t, created)["id"].(string)
	mustCLI(t, "claim", id, "--actor", "coder")
	result := exactlyOneJSONObject(t, runCLIStdinMust(t, `{"assignee":"reviewer","handoff":"json handoff","message":"json transfer"}`, "reassign", id, "--input", "-", "--actor", "coder"))
	if result["assignee"] != "reviewer" || result["handoff"] != "json handoff" || result["message"] != "json transfer" {
		t.Fatalf("JSON reassignment result: %v", result)
	}
	view := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if view["assignee"] != "reviewer" || !strings.Contains(view["body"].(string), "json handoff") || !strings.Contains(view["body"].(string), "json transfer") {
		t.Fatalf("JSON reassignment did not persist: %v", view)
	}
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

func TestReassignToOption(t *testing.T) {
	t.Chdir(t.TempDir())
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "assign", "work"))["id"].(string)
	for _, args := range [][]string{
		{"reassign", id, "user", "--to", "user", "--actor", "coder"},
		{"reassign", id, "--to", "", "--actor", "coder"},
	} {
		if out, code := runCLI(t, args...); code == 0 {
			t.Fatalf("accepted invalid target: %v %s", args, out)
		}
	}
	if out, code := runCLIStdin(t, `{"assignee":"user"}`, "reassign", id, "--to", "user", "--input", "-", "--actor", "coder"); code == 0 {
		t.Fatalf("accepted --to with --input: %s", out)
	}
	assigned := exactlyOneJSONObject(t, mustCLI(t, "reassign", id, "--to", "coder", "--actor", "coder"))
	if assigned["assignee"] != "coder" || assigned["from_assignee"] != "" {
		t.Fatalf("assignment: %v", assigned)
	}
	transferred := exactlyOneJSONObject(t, mustCLI(t, "reassign", id, "--to", "user", "--actor", "coder"))
	if transferred["assignee"] != "user" || transferred["from_assignee"] != "coder" {
		t.Fatalf("transfer: %v", transferred)
	}
}

func TestQueryReassignSignoff(t *testing.T) {
	t.Chdir(t.TempDir())
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "signoff", "work"))["id"].(string)
	mustCLI(t, "claim", id, "--actor", "coder")
	mustCLI(t, "submit", id, "--actor", "coder")
	mustCLI(t, "claim", id, "--actor", "reviewer")
	mustCLI(t, "approve", id, "--actor", "reviewer")
	result := exactlyOneJSONObject(t, mustCLI(t, "query", "id:"+id, "state:signoff", "unclaimed", "::", "reassign", "--to", "user", "--handoff", "explanation", "-m", "routed", "--actor", "reviewer"))
	if result["changed"] != true || result["state"] != "signoff" || result["assignee"] != "user" || result["from_assignee"] != "" {
		t.Fatalf("signoff assignment: %v", result)
	}
	before := mustCLI(t, "show", id, "--full")
	if !strings.Contains(before, "explanation") || !strings.Contains(before, "routed") {
		t.Fatalf("context missing: %s", before)
	}
	for _, args := range [][]string{
		{"query", "id:" + id, "unclaimed", "::", "reassign", "--to", "other", "--actor", "reviewer"},
		{"query", "id:" + id, "::", "reassign", "--to", "other", "--actor", "reviewer"},
		{"query", "id:" + id, "::", "reassign", id, "--to", "other", "--actor", "reviewer"},
		{"query", "id:" + id, "::", "reassign", "--actor", "reviewer"},
		{"query", "--actor", "reviewer", "id:" + id, "::", "reassign", "--to", "other", "--actor", "reviewer"},
	} {
		if out, code := runCLI(t, args...); code == 0 {
			t.Fatalf("accepted unsafe reassignment: %v %s", args, out)
		}
		if after := mustCLI(t, "show", id, "--full"); after != before {
			t.Fatalf("failed reassignment changed ticket: %s", after)
		}
	}
}

func TestQueryReassignRequiresSingleMatch(t *testing.T) {
	t.Chdir(t.TempDir())
	mustCLI(t, "init")
	var ids []string
	for _, title := range []string{"first", "second"} {
		ids = append(ids, exactlyOneJSONObject(t, mustCLI(t, "create", title, "work"))["id"].(string))
	}
	for _, query := range []string{"state:open", "state:signoff"} {
		out, code := runCLI(t, "query", query, "::", "reassign", "--to", "user", "--actor", "coder")
		if code == 0 || errCode(t, out) != "invalid_argument" {
			t.Fatalf("cardinality: %d %s", code, out)
		}
	}
	for _, id := range ids {
		view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
		if view["assignee"] != nil {
			t.Fatalf("cardinality failure mutated ticket: %v", view)
		}
	}
	result := exactlyOneJSONObject(t, mustCLI(t, "query", "--actor", "coder", "id:"+ids[0], "::", "reassign", "--to", "user"))
	if result["assignee"] != "user" {
		t.Fatalf("producer actor: %v", result)
	}
}

func TestReassignEveryLifecycleState(t *testing.T) {
	t.Chdir(t.TempDir())
	mustCLI(t, "init")
	for _, state := range []string{"open", "hold", "review", "signoff", "closed", "rejected"} {
		t.Run(state, func(t *testing.T) {
			id := exactlyOneJSONObject(t, mustCLI(t, "create", state, "work"))["id"].(string)
			mustCLI(t, "state", id, state)
			result := exactlyOneJSONObject(t, mustCLI(t, "query", "id:"+id, "state:"+state, "unclaimed", "::", "reassign", "--to", "user", "--handoff", "context", "--actor", "router"))
			if result["id"] != id || result["state"] != state || result["assignee"] != "user" || result["from_assignee"] != "" || result["changed"] != true || result["handoff"] != "context" {
				t.Fatalf("response does not confirm reassignment: %v", result)
			}
			out, code := runCLI(t, "reassign", id, "--to", "other", "--actor", "router")
			if code == 0 || errCode(t, out) != "already_claimed" {
				t.Fatalf("owner protection: %d %s", code, out)
			}
			noop := exactlyOneJSONObject(t, mustCLI(t, "reassign", id, "--to", "user", "--actor", "user"))
			if noop["id"] != id || noop["state"] != state || noop["assignee"] != "user" || noop["from_assignee"] != "user" || noop["changed"] != false {
				t.Fatalf("no-op result: %v", noop)
			}
			transferred := exactlyOneJSONObject(t, mustCLI(t, "reassign", id, "--to", "other", "--actor", "user"))
			if transferred["state"] != state || transferred["from_assignee"] != "user" || transferred["assignee"] != "other" || transferred["changed"] != true {
				t.Fatalf("transfer result: %v", transferred)
			}
		})
	}
}
