package cli

import (
	"strings"
	"testing"
)

func TestActorReportsConfiguredEnvironment(t *testing.T) {
	t.Setenv("TICKET_ACTOR", "agent-00")
	out, code := runCLIHuman(t, "actor")
	if code != 0 || out != "agent-00\n" {
		t.Fatalf("human actor: exit=%d out=%q", code, out)
	}
	result := exactlyOneJSONObject(t, mustCLI(t, "actor"))
	if result["actor"] != "agent-00" || result["source"] != "TICKET_ACTOR" {
		t.Fatalf("environment actor: %v", result)
	}
}

func TestActorReportsExplicitUnsetState(t *testing.T) {
	t.Setenv("TICKET_ACTOR", "")
	result := exactlyOneJSONObject(t, mustCLI(t, "actor"))
	if result["actor"] != nil || result["source"] != nil {
		t.Fatalf("unset actor invented a value: %v", result)
	}
	out, code := runCLIHuman(t, "actor")
	if code != 0 || !strings.Contains(out, "no actor configured") {
		t.Fatalf("human unset actor: exit=%d out=%q", code, out)
	}
}
