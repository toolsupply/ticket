package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
)

func TestUserConfigRejectsOversizedFileBeforeParsing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(userConfigMaxBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := runCLI(t, "--config", path, "list")
	if code != contract.ExitCode(contract.ErrFileTooLarge) || errCode(t, out) != string(contract.ErrFileTooLarge) {
		t.Fatalf("oversized user config: exit=%d out=%q", code, out)
	}
}

func TestUserConfigAcceptsExactLimitAndRejectsNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	path := filepath.Join(dir, "config.json")
	base := []byte(`{"scopes":{}}`)
	exact := append(append([]byte(nil), base...), []byte(strings.Repeat(" ", int(userConfigMaxBytes)-len(base)))...)
	if err := os.WriteFile(path, exact, 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := runCLI(t, "--config", path, "list")
	if code != 0 {
		t.Fatalf("exact user config limit: exit=%d out=%q", code, out)
	}
	result := exactlyOneJSONObject(t, out)
	if _, ok := result["items"]; !ok {
		t.Fatalf("exact user config limit response missing items: %v", result)
	}
	if err := os.Mkdir(filepath.Join(dir, "config-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, code = runCLI(t, "--config", filepath.Join(dir, "config-dir"), "list")
	if code != contract.ExitCode(contract.ErrInvalidArgument) || errCode(t, out) != string(contract.ErrInvalidArgument) {
		t.Fatalf("non-regular user config: exit=%d out=%q", code, out)
	}
}

func TestCurrentMarkerExactBoundarySelectsAndLimitPlusOneRejects(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Current marker boundary", "Select only when the marker is within its bound."))["id"].(string)
	marker := filepath.Join(dir, "tickets", ".local", "current")
	exact := id + strings.Repeat(" ", 128-len(id))
	if len(exact) != 128 {
		t.Fatalf("exact marker length=%d", len(exact))
	}
	if err := os.WriteFile(marker, []byte(exact), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runCLIHumanError(t, "status")
	if code != 0 || stderr != "" || !strings.Contains(stdout, id) {
		t.Fatalf("exact current marker was not selected: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if err := os.WriteFile(marker, []byte(exact+" "), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = runCLIHumanError(t, "status")
	if code == 0 || stdout != "" || !strings.Contains(stderr, "requires an ID") {
		t.Fatalf("limit-plus-one current marker was selected: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestOversizedCurrentMarkerCannotSelectTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLIHuman(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Current marker", "Do not select from an oversized marker."))["id"].(string)
	marker := filepath.Join(dir, "tickets", ".local", "current")
	if err := os.WriteFile(marker, []byte(id+strings.Repeat("x", 128)), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runCLIHumanError(t, "status")
	if code == 0 || stdout != "" || !strings.Contains(stderr, "requires an ID") {
		t.Fatalf("oversized current marker selected a ticket: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCurrentMarkerSymlinkAndDirectoryCannotSelectTicket(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Current marker type", "Do not select from unsafe marker types."))["id"].(string)
	marker := filepath.Join(dir, "tickets", ".local", "current")
	outside := filepath.Join(dir, "outside-current")
	if err := os.WriteFile(outside, []byte(id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			_ = os.RemoveAll(marker)
			if kind == "symlink" {
				if err := os.Symlink(outside, marker); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else if err := os.Mkdir(marker, 0o700); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := runCLIHumanError(t, "status")
			if code == 0 || stdout != "" || !strings.Contains(stderr, "requires an ID") {
				t.Fatalf("unsafe marker selected ticket: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestWaitRejectsUnsafeChangeMarkerWithoutClaiming(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Wait marker", "Do not claim when the marker is unsafe."))["id"].(string)
	marker := filepath.Join(dir, "tickets", ".local", "change")
	outside := filepath.Join(dir, "outside-change")
	if err := os.WriteFile(outside, []byte("change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"oversized", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			_ = os.RemoveAll(marker)
			switch kind {
			case "oversized":
				if err := os.WriteFile(marker, []byte(strings.Repeat("x", 257)), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(outside, marker); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "directory":
				if err := os.Mkdir(marker, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			out, code := runCLI(t, "wait", "--claim", "--actor", "worker")
			if code != contract.ExitCode(contract.ErrIOError) || errCode(t, out) != string(contract.ErrIOError) {
				t.Fatalf("unsafe change marker: exit=%d out=%q", code, out)
			}
			view := exactlyOneJSONObject(t, mustCLI(t, "show", id))
			if view["assignee"] != nil {
				t.Fatalf("unsafe marker claimed ticket: %v", view)
			}
		})
	}
}

func TestInteractiveSeedRejectsCurrentMarkerSymlinkAndDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Interactive marker", "Do not seed from unsafe marker types."))["id"].(string)
	marker := filepath.Join(dir, "tickets", ".local", "current")
	outside := filepath.Join(dir, "outside-current")
	if err := os.WriteFile(outside, []byte(id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			_ = os.RemoveAll(marker)
			if kind == "symlink" {
				if err := os.Symlink(outside, marker); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else if err := os.Mkdir(marker, 0o700); err != nil {
				t.Fatal(err)
			}
			out, stderr, code := runShell(t, "exit\n")
			if code != 0 || out != "" || !strings.Contains(stderr, "no current ticket") || strings.Contains(stderr, id) {
				t.Fatalf("unsafe interactive seed: code=%d stdout=%q stderr=%q", code, out, stderr)
			}
		})
	}
}
