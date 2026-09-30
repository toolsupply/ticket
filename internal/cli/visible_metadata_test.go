package cli

import (
	"reflect"
	"testing"
)

func TestVisibleMetadataStringValuesPersistAcrossOwnership(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")

	for _, actor := range []string{"false", "true", "null", "123"} {
		t.Run(actor, func(t *testing.T) {
			id := exactlyOneJSONObject(t, mustCLI(t, "create", "Visible metadata strings", "Round-trip visible values.",
				"--tag", "false", "--tag", "true", "--tag", "null", "--tag", "123"))["id"].(string)

			claimed := exactlyOneJSONObject(t, mustCLI(t, "claim", id, "--actor", actor))
			if claimed["assignee"] != actor || claimed["changed"] != true {
				t.Fatalf("claim result = %v", claimed)
			}

			view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
			if view["assignee"] != actor {
				t.Fatalf("show/reparse assignee = %#v, want %q; view=%v", view["assignee"], actor, view)
			}
			if tags, ok := view["tags"].([]any); !ok || !reflect.DeepEqual(tags, []any{"123", "false", "null", "true"}) {
				t.Fatalf("visible tags = %#v", view["tags"])
			}
			reclaimed := exactlyOneJSONObject(t, mustCLI(t, "claim", id, "--actor", actor))
			if reclaimed["assignee"] != actor || reclaimed["changed"] != false {
				t.Fatalf("same-actor re-claim = %v", reclaimed)
			}

			other := "other-actor"
			if out, code := runCLI(t, "claim", id, "--actor", other); code == 0 || errCode(t, out) != "already_claimed" {
				t.Fatalf("other-actor claim: exit=%d out=%q", code, out)
			}
			if out, code := runCLI(t, "release", id, "--actor", other); code == 0 || errCode(t, out) != "already_claimed" {
				t.Fatalf("other-actor release: exit=%d out=%q", code, out)
			}

			released := exactlyOneJSONObject(t, mustCLI(t, "release", id, "--actor", actor))
			if released["changed"] != true {
				t.Fatalf("matching-actor release = %v", released)
			}
			view = exactlyOneJSONObject(t, mustCLI(t, "show", id))
			if view["assignee"] != nil {
				t.Fatalf("release retained assignee: %#v", view["assignee"])
			}
			runCLIStdinMust(t, `{"set":{"blocked_reason":"[brackets] {braces} true"}}`, "update", id, "--input", "-")
			view = exactlyOneJSONObject(t, mustCLI(t, "show", id))
			if view["blocked_reason"] != "[brackets] {braces} true" {
				t.Fatalf("blocked reason = %#v", view["blocked_reason"])
			}
		})
	}
}
