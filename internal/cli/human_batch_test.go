package cli

import (
	"strings"
	"testing"
)

func TestHumanCloseAllUsesPastTense(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	var ids []string
	for _, title := range []string{"first", "second"} {
		out, code := runCLI(t, "create", title)
		if code != 0 {
			t.Fatalf("create %q: exit=%d out=%q", title, code, out)
		}
		ids = append(ids, exactlyOneJSONObject(t, out)["id"].(string))
	}

	out, code := runCLIHuman(t, "close", "all")
	if code != 0 {
		t.Fatalf("close all: exit=%d out=%q", code, out)
	}
	for _, id := range ids {
		if !strings.Contains(out, id+": hold -> closed") {
			t.Fatalf("close all transition output: %q", out)
		}
	}
}
