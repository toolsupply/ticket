package cli

import (
	"strings"
	"testing"
)

func TestReadOnlyCommandsRejectActor(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	commands := []string{"init", "create", "delete", "list", "grep", "ready", "show", "edit", "status", "path", "check", "version", "actor", "info", "help"}
	for _, command := range commands {
		args := []string{command, "--actor", "codex"}
		if command == "grep" {
			args = []string{command, "ticket", "--actor", "codex"}
		}
		if out, code := runCLI(t, args...); code == 0 || errCode(t, out) != "invalid_argument" {
			t.Fatalf("%s actor accepted: exit=%d output=%q", command, code, out)
		}
		out, code := runCLIHuman(t, "help", command)
		if code != 0 || strings.Contains(out, "--actor") {
			t.Fatalf("%s help actor option: exit=%d output=%q", command, code, out)
		}
	}
}
