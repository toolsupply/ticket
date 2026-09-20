package scm

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
)

type recordedCommand struct {
	dir  string
	name string
	args []string
}

func TestConfigureEnvironment(t *testing.T) {
	t.Setenv("TICKET_SCM", "")
	t.Setenv("TICKET_SCM_MODE", "unsupported")
	backend, err := Configure()
	if err != nil || backend != nil {
		t.Fatalf("unset SCM: backend=%v err=%v", backend, err)
	}

	t.Setenv("TICKET_SCM", "none")
	backend, err = Configure()
	if err != nil || backend != nil {
		t.Fatalf("none SCM: backend=%v err=%v", backend, err)
	}

	t.Setenv("TICKET_SCM", "mercurial")
	if _, err := Configure(); !hasCode(err, contract.ErrInvalidArgument) {
		t.Fatalf("unknown SCM error=%v", err)
	}
	t.Setenv("TICKET_SCM", "git")
	t.Setenv("TICKET_SCM_MODE", "batch")
	if _, err := Configure(); !hasCode(err, contract.ErrInvalidArgument) {
		t.Fatalf("unknown mode error=%v", err)
	}

	t.Setenv("TICKET_SCM_MODE", "sync")
	t.Setenv("PATH", t.TempDir())
	if _, err := Configure(); !hasCode(err, contract.ErrIOError) {
		t.Fatalf("missing executable error=%v", err)
	}
}

func TestBackendCommandSemantics(t *testing.T) {
	var calls []recordedCommand
	changes := exec.Command("sh", "-c", "exit 1")
	changesErr := changes.Run()
	if changesErr == nil {
		t.Fatal("expected an exit status for the changed fixture")
	}
	// Keep the real *exec.ExitError so the Git status convention is tested.
	run := func(dir, name string, args ...string) ([]byte, error) {
		calls = append(calls, recordedCommand{dir: dir, name: name, args: append([]string(nil), args...)})
		if name == "git" && len(args) > 1 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" {
			return []byte("origin/main\n"), nil
		}
		if name == "git" && len(args) > 0 && args[0] == "diff" {
			return nil, changesErr
		}
		if name == "svn" && len(args) > 0 && args[0] == "status" {
			return []byte(" M existing\n"), nil
		}
		return nil, nil
	}

	git := newBackend("git", run)
	if err := git.Update("/tickets"); err != nil {
		t.Fatal(err)
	}
	if err := git.Commit("/tickets", "ticket: update", []string{"."}); err != nil {
		t.Fatal(err)
	}
	if err := git.Publish("/tickets"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 9 {
		t.Fatalf("git calls: %+v", calls)
	}
	if got := strings.Join(calls[0].args, " "); got != "rev-parse --show-toplevel" {
		t.Fatalf("git worktree args=%q", got)
	}
	if got := strings.Join(calls[1].args, " "); got != "rev-parse --abbrev-ref --symbolic-full-name @{u}" {
		t.Fatalf("git upstream args=%q", got)
	}
	if got := strings.Join(calls[2].args, " "); got != "pull --ff-only" {
		t.Fatalf("git update args=%q", got)
	}
	if got := strings.Join(calls[3].args, " "); got != "add -- . :(exclude,glob)[.]local/**" {
		t.Fatalf("git add args=%q", got)
	}
	if got := strings.Join(calls[4].args, " "); got != "diff --cached --quiet -- . :(exclude,glob)[.]local/**" {
		t.Fatalf("git pending args=%q", got)
	}
	if got := strings.Join(calls[5].args, " "); got != "commit --only -m ticket: update -- . :(exclude,glob)[.]local/**" {
		t.Fatalf("git commit args=%q", got)
	}
	if got := strings.Join(calls[6].args, " "); got != "rev-parse --show-toplevel" {
		t.Fatalf("git publish worktree args=%q", got)
	}
	if got := strings.Join(calls[7].args, " "); got != "rev-parse --abbrev-ref --symbolic-full-name @{u}" {
		t.Fatalf("git publish upstream args=%q", got)
	}
	if got := strings.Join(calls[8].args, " "); got != "push" {
		t.Fatalf("git publish args=%q", got)
	}
	for _, call := range calls {
		if call.name != "git" || call.dir != "/tickets" {
			t.Fatalf("git command context: %+v", call)
		}
	}

	calls = nil
	svnRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(svnRoot, "existing"), []byte("ticket"), 0o644); err != nil {
		t.Fatal(err)
	}
	svn := newBackend("svn", run)
	if err := svn.Update(svnRoot); err != nil {
		t.Fatal(err)
	}
	if err := svn.Commit(svnRoot, "ticket: update", []string{"."}); err != nil {
		t.Fatal(err)
	}
	if err := svn.Publish("/tickets/subtree"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 || calls[0].name != "svn" || calls[1].name != "svn" {
		t.Fatalf("svn calls: %+v", calls)
	}
	if got := strings.Join(calls[0].args, " "); got != "update ." {
		t.Fatalf("svn update args=%q", got)
	}
	if got := strings.Join(calls[1].args, " "); got != "add --force --depth infinity existing" {
		t.Fatalf("svn add args=%q", got)
	}
	if got := strings.Join(calls[2].args, " "); got != "status --quiet existing" {
		t.Fatalf("svn status args=%q", got)
	}
	if got := strings.Join(calls[3].args, " "); got != "commit existing -m ticket: update" {
		t.Fatalf("svn commit args=%q", got)
	}
	calls = nil
	svnRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(svnRoot, "ticket-dir"), []byte("ticket"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(svnRoot, ".local"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := svn.Commit(svnRoot, "ticket: create", []string{"."}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || strings.Join(calls[0].args, " ") != "add --force --depth infinity ticket-dir" ||
		strings.Join(calls[1].args, " ") != "status --quiet ticket-dir" ||
		strings.Join(calls[2].args, " ") != "commit ticket-dir -m ticket: create" {
		t.Fatalf("svn scoped add/commit: %+v", calls)
	}
}

func TestSVNCommitSchedulesOnlyExplicitMissingPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing"), []byte("ticket\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unrelated-missing"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "unrelated-missing")); err != nil {
		t.Fatal(err)
	}

	var calls []recordedCommand
	run := func(dir, name string, args ...string) ([]byte, error) {
		calls = append(calls, recordedCommand{dir: dir, name: name, args: append([]string(nil), args...)})
		if name != "svn" || len(args) < 3 || args[0] != "status" || args[1] != "--quiet" {
			return nil, nil
		}
		if len(args) == 3 && args[2] == "deleted-ticket" {
			return []byte("!       deleted-ticket\n"), nil
		}
		if len(args) == 3 && args[2] == "unversioned-missing" {
			return nil, nil
		}
		return []byte(" M existing\n!       unrelated-missing\n"), nil
	}

	if err := newBackend("svn", run).Commit(root, "ticket: delete", []string{".", "deleted-ticket", "unversioned-missing"}); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(calls))
	for _, call := range calls {
		got = append(got, strings.Join(call.args, " "))
	}
	want := []string{
		"status --quiet deleted-ticket",
		"delete --force deleted-ticket",
		"status --quiet unversioned-missing",
		"add --force --depth infinity existing",
		"status --quiet existing deleted-ticket",
		"commit existing deleted-ticket -m ticket: delete",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("SVN calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, call := range calls {
		if strings.Contains(strings.Join(call.args, " "), "unrelated-missing") {
			t.Fatalf("unrelated missing path was scheduled or committed: %+v", calls)
		}
	}

	calls = nil
	if err := newBackend("svn", func(dir, name string, args ...string) ([]byte, error) {
		calls = append(calls, recordedCommand{dir: dir, name: name, args: append([]string(nil), args...)})
		if name == "svn" && len(args) >= 3 && args[0] == "status" && args[1] == "--quiet" && args[2] == "deleted-ticket" {
			return []byte("D       deleted-ticket\n"), nil
		}
		if name == "svn" && len(args) > 0 && args[0] == "status" {
			return []byte(" M existing\n"), nil
		}
		return nil, nil
	}).Commit(root, "ticket: retry delete", []string{".", "deleted-ticket"}); err != nil {
		t.Fatal(err)
	}
	got = got[:0]
	for _, call := range calls {
		got = append(got, strings.Join(call.args, " "))
	}
	want = []string{
		"status --quiet deleted-ticket",
		"add --force --depth infinity existing",
		"status --quiet existing deleted-ticket",
		"commit existing deleted-ticket -m ticket: retry delete",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("SVN retry calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestGitSynchronizationAllowsLocalRepositoryWithoutUpstream(t *testing.T) {
	var calls []recordedCommand
	noUpstream := errors.New("no upstream configured")
	run := func(dir, name string, args ...string) ([]byte, error) {
		calls = append(calls, recordedCommand{dir: dir, name: name, args: append([]string(nil), args...)})
		if len(args) > 1 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" {
			return nil, noUpstream
		}
		return []byte("/repo\n"), nil
	}

	if err := newBackend("git", run).Update("/tickets"); err != nil {
		t.Fatalf("local update: %v", err)
	}
	if err := newBackend("git", run).Publish("/tickets"); err != nil {
		t.Fatalf("local publish: %v", err)
	}
	want := []string{
		"rev-parse --show-toplevel",
		"rev-parse --abbrev-ref --symbolic-full-name @{u}",
		"rev-parse --show-toplevel",
		"rev-parse --abbrev-ref --symbolic-full-name @{u}",
	}
	if len(calls) != len(want) {
		t.Fatalf("local synchronization calls: %+v", calls)
	}
	for i, call := range calls {
		if got := strings.Join(call.args, " "); got != want[i] {
			t.Fatalf("local synchronization call %d=%q want %q", i, got, want[i])
		}
	}
}

func TestGitSynchronizationAllowsRealLocalRepositoryWithoutUpstream(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "tickets")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	if err := newBackend("git", execCommand).Update(root); err != nil {
		t.Fatalf("local Git repository update: %v", err)
	}
	if err := newBackend("git", execCommand).Publish(root); err != nil {
		t.Fatalf("local Git repository publish: %v", err)
	}
}

func TestBackendFailureIncludesBoundedCommandOutput(t *testing.T) {
	// The repository and upstream probes succeed; pull is the failing
	// synchronization step whose diagnostic should be reported.
	backend := newBackend("git", func(_ string, _ string, args ...string) ([]byte, error) {
		if args[0] == "rev-parse" {
			if len(args) > 1 && args[1] == "--abbrev-ref" {
				return []byte("origin/main\n"), nil
			}
			return []byte("/repo\n"), nil
		}
		return []byte("fatal: update failed"), errors.New("exit status 1")
	})
	err := backend.Update("/tickets")
	if !hasCode(err, contract.ErrIOError) || !strings.Contains(err.Error(), "git pull failed: fatal: update failed") {
		t.Fatalf("failure=%v", err)
	}
}

func TestGitCommitRestrictsPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for _, ignored := range []bool{false, true} {
		name := "unignored"
		if ignored {
			name = "ignored"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "tickets")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			runGit := func(args ...string) {
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, output)
				}
			}
			runGit("init")
			runGit("config", "user.email", "test@example.invalid")
			runGit("config", "user.name", "Ticket Test")
			if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("source\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "TASK.md"), []byte("ticket\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if ignored {
				if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".local/\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			runGit("add", ".")
			runGit("commit", "-m", "initial")
			if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("unrelated\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "TASK.md"), []byte("ticket changed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, ".local"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".local", "current"), []byte("local\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGit("add", "source.txt")
			backend := newBackend("git", execCommand)
			if err := backend.Commit(root, "ticket: update", []string{"."}); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("git", "show", "--format=", "--name-only", "HEAD")
			cmd.Dir = dir
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(out)) != "tickets/TASK.md" {
				t.Fatalf("committed paths=%q", out)
			}
			cmd = exec.Command("git", "diff", "--cached", "--name-only")
			cmd.Dir = dir
			out, err = cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(out)) != "source.txt" {
				t.Fatalf("unrelated staged paths=%q", out)
			}
		})
	}
}

func hasCode(err error, want contract.ErrorCode) bool {
	var ce *contract.Error
	return errors.As(err, &ce) && ce.Code == want
}
