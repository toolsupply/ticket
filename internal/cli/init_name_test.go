package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolsupply/ticket/internal/store"
)

func TestInitNameFlagPersistsRepositoryName(t *testing.T) {
	for _, test := range []struct {
		name string
		flag string
	}{
		{name: "long", flag: "--name"},
		{name: "short", flag: "-n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if out, code := runCLI(t, "init", test.flag, "Project tickets"); code != 0 {
				t.Fatalf("init: exit=%d out=%q", code, out)
			}
			cfg, err := store.LoadConfig(filepath.Join(dir, "tickets"))
			if err != nil || cfg.Name != "Project tickets" {
				t.Fatalf("named repository config = %+v err=%v", cfg, err)
			}
			info := exactlyOneJSONObject(t, mustCLI(t, "info"))
			if info["name"] != "Project tickets" {
				t.Fatalf("repository info name = %#v", info["name"])
			}
		})
	}
}

func TestInitHelpDocumentsOptionalName(t *testing.T) {
	out, code := runCLI(t, "init", "--help")
	if code != 0 || !strings.Contains(out, "-n, --name") || !strings.Contains(out, "Optional repository display name") {
		t.Fatalf("init help: exit=%d out=%q", code, out)
	}
}
