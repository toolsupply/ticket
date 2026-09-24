package cli

import (
	"strings"
	"testing"
)

func TestLifecycleStateAliasesUseCanonicalCLIOutput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Legacy state", "Exercise compatibility."))["id"].(string)

	state := exactlyOneJSONObject(t, mustCLI(t, "state", id, "completed"))
	if state["state"] != "closed" || state["from_state"] != "open" {
		t.Fatalf("legacy state output: %v", state)
	}

	canonical := exactlyOneJSONObject(t, mustCLI(t, "list", "--state", "closed"))
	legacy := exactlyOneJSONObject(t, mustCLI(t, "list", "--state", "completed"))
	if canonical["items"].([]any)[0].(map[string]any)["id"] != id || legacy["items"].([]any)[0].(map[string]any)["id"] != id {
		t.Fatalf("state selector results: canonical=%v legacy=%v", canonical, legacy)
	}
	if canonical["items"].([]any)[0].(map[string]any)["state"] != "closed" || legacy["items"].([]any)[0].(map[string]any)["state"] != "closed" {
		t.Fatalf("state selector output: canonical=%v legacy=%v", canonical, legacy)
	}

	for _, selector := range []string{"closed", "completed"} {
		out, code := runCLI(t, "close", selector)
		if code == 0 || errCode(t, out) != "invalid_argument" || !strings.Contains(out, "only nonterminal states") {
			t.Fatalf("close selector %q: exit=%d output=%q", selector, code, out)
		}
	}
}
