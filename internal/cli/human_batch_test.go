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
	for _, title := range []string{"first", "second"} {
		if out, code := runCLI(t, "create", title); code != 0 {
			t.Fatalf("create %q: exit=%d out=%q", title, code, out)
		}
	}

	out, code := runCLIHuman(t, "close", "all")
	if code != 0 {
		t.Fatalf("close all: exit=%d out=%q", code, out)
	}
	if out != "closed 2 tickets\n" {
		t.Fatalf("close all output: %q", out)
	}
	if strings.Contains(out, "close 2 tickets") {
		t.Fatalf("close all used the command name: %q", out)
	}
}
