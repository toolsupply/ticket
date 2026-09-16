package cli

import (
	"strings"
	"testing"
)

func TestOpenAndRejectUseCurrentTicketWithPositionalText(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	created := exactlyOneJSONObject(t, mustCLI(t, "create", "Current transition", "Exercise current-ticket transitions."))
	id := created["id"].(string)
	if _, code := runCLI(t, "hold", id); code != 0 {
		t.Fatalf("hold: exit=%d", code)
	}
	if out, code := runCLIHuman(t, "open", "Continue from the current ticket"); code != 0 || !strings.Contains(out, "open "+id) {
		t.Fatalf("open with current ticket and handoff: exit=%d output=%q", code, out)
	}
	opened := exactlyOneJSONObject(t, mustCLI(t, "show", id, "--full"))
	if opened["state"] != "open" || !strings.Contains(opened["body"].(string), "Continue from the current ticket") {
		t.Fatalf("opened ticket: %v", opened)
	}

	second := exactlyOneJSONObject(t, mustCLI(t, "create", "Current rejection", "Exercise current-ticket rejection."))["id"].(string)
	if out, code := runCLIHuman(t, "reject", "Duplicate work"); code != 0 || !strings.Contains(out, "rejected "+second) {
		t.Fatalf("reject with current ticket and outcome: exit=%d output=%q", code, out)
	}
	rejected := exactlyOneJSONObject(t, mustCLI(t, "show", second))
	if rejected["state"] != "rejected" || rejected["sections"].(map[string]any)["outcome"].(map[string]any)["text"] != "Duplicate work" {
		t.Fatalf("rejected ticket: %v", rejected)
	}
}

func TestUpgradeDoesNotAcceptActor(t *testing.T) {
	if out, code := runCLI(t, "upgrade", "--actor", "codex"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("upgrade actor flag: exit=%d output=%q", code, out)
	}
	out, code := runCLIHuman(t, "help", "upgrade")
	if code != 0 || strings.Contains(out, "--actor") {
		t.Fatalf("upgrade help actor option: exit=%d output=%q", code, out)
	}
}
