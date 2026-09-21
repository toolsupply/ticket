package scm

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
		if name == "git" && len(args) > 0 && args[0] == "remote" {
			return []byte("origin\n"), nil
		}
		if name == "git" && len(args) > 0 && args[0] == "for-each-ref" {
			return []byte("origin\x00refs/heads/main\n"), nil
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
	if len(calls) != 11 {
		t.Fatalf("git calls: %+v", calls)
	}
	if got := strings.Join(calls[0].args, " "); got != "rev-parse --show-toplevel" {
		t.Fatalf("git worktree args=%q", got)
	}
	if got := strings.Join(calls[1].args, " "); got != "rev-parse --abbrev-ref --symbolic-full-name @{u}" {
		t.Fatalf("git upstream args=%q", got)
	}
	if got := strings.Join(calls[2].args, " "); got != "for-each-ref --format=%(upstream:remotename)%00%(upstream:remoteref) HEAD" {
		t.Fatalf("git upstream metadata args=%q", got)
	}
	if got := strings.Join(calls[3].args, " "); got != "pull --ff-only" {
		t.Fatalf("git update args=%q", got)
	}
	if got := strings.Join(calls[4].args, " "); got != "add -- . :(exclude,glob)[.]local/**" {
		t.Fatalf("git add args=%q", got)
	}
	if got := strings.Join(calls[5].args, " "); got != "diff --cached --quiet -- . :(exclude,glob)[.]local/**" {
		t.Fatalf("git pending args=%q", got)
	}
	if got := strings.Join(calls[6].args, " "); got != "commit --only -m ticket: update -- . :(exclude,glob)[.]local/**" {
		t.Fatalf("git commit args=%q", got)
	}
	if got := strings.Join(calls[7].args, " "); got != "rev-parse --show-toplevel" {
		t.Fatalf("git publish worktree args=%q", got)
	}
	if got := strings.Join(calls[8].args, " "); got != "rev-parse --abbrev-ref --symbolic-full-name @{u}" {
		t.Fatalf("git publish upstream args=%q", got)
	}
	if got := strings.Join(calls[9].args, " "); got != "for-each-ref --format=%(upstream:remotename)%00%(upstream:remoteref) HEAD" {
		t.Fatalf("git publish metadata args=%q", got)
	}
	if got := strings.Join(calls[10].args, " "); got != "push -- origin HEAD:refs/heads/main" {
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
	if got := strings.Join(calls[1].args, " "); got != "add --force --depth infinity -- existing@" {
		t.Fatalf("svn add args=%q", got)
	}
	if got := strings.Join(calls[2].args, " "); got != "status --quiet -- existing@" {
		t.Fatalf("svn status args=%q", got)
	}
	if got := strings.Join(calls[3].args, " "); got != "commit -m ticket: update -- existing@" {
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
	if len(calls) != 3 || strings.Join(calls[0].args, " ") != "add --force --depth infinity -- ticket-dir@" ||
		strings.Join(calls[1].args, " ") != "status --quiet -- ticket-dir@" ||
		strings.Join(calls[2].args, " ") != "commit -m ticket: create -- ticket-dir@" {
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
		if len(args) == 4 && args[2] == "--" && args[3] == "deleted-ticket@" {
			return []byte("!       deleted-ticket\n"), nil
		}
		if len(args) == 4 && args[2] == "--" && args[3] == "unversioned-missing@" {
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
		"status --quiet -- deleted-ticket@",
		"delete --force -- deleted-ticket@",
		"status --quiet -- unversioned-missing@",
		"add --force --depth infinity -- existing@",
		"status --quiet -- existing@ deleted-ticket@",
		"commit -m ticket: delete -- existing@ deleted-ticket@",
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
		if name == "svn" && len(args) >= 4 && args[0] == "status" && args[1] == "--quiet" && args[2] == "--" && args[3] == "deleted-ticket@" {
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
		"status --quiet -- deleted-ticket@",
		"add --force --depth infinity -- existing@",
		"status --quiet -- existing@ deleted-ticket@",
		"commit -m ticket: retry delete -- existing@ deleted-ticket@",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("SVN retry calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSVNPathsAreLiteralAcrossMutationCommands(t *testing.T) {
	root := t.TempDir()
	paths := []string{"-leading-dash", "--long-option-looking-name", "name with spaces", "Unicode-☃", "@", "name@123", "--targets=targets.txt"}
	for _, path := range paths {
		if err := os.WriteFile(filepath.Join(root, path), []byte("ticket"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var calls []recordedCommand
	run := func(dir, name string, args ...string) ([]byte, error) {
		calls = append(calls, recordedCommand{dir: dir, name: name, args: append([]string(nil), args...)})
		if name == "svn" && len(args) >= 2 && args[0] == "status" && args[1] == "--quiet" {
			return []byte("M       ticket\n"), nil
		}
		return nil, nil
	}
	if err := newBackend("svn", run).Commit(root, "ticket: hostile", paths); err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		want := []string{"add", "--force", "--depth", "infinity", "--", path + "@"}
		if !reflect.DeepEqual(calls[i].args, want) {
			t.Fatalf("add call %d=%q want %q", i, calls[i].args, want)
		}
	}
	wantPending := append([]string{"status", "--quiet", "--"}, svnLiteralPaths(paths)...)
	if !reflect.DeepEqual(calls[len(paths)].args, wantPending) {
		t.Fatalf("pending call=%q want %q", calls[len(paths)].args, wantPending)
	}
	wantCommit := append([]string{"commit", "-m", "ticket: hostile", "--"}, svnLiteralPaths(paths)...)
	if !reflect.DeepEqual(calls[len(paths)+1].args, wantCommit) {
		t.Fatalf("commit call=%q want %q", calls[len(paths)+1].args, wantCommit)
	}

	for _, missing := range paths {
		if err := os.Remove(filepath.Join(root, missing)); err != nil {
			t.Fatal(err)
		}
		calls = nil
		runMissing := func(dir, name string, args ...string) ([]byte, error) {
			calls = append(calls, recordedCommand{dir: dir, name: name, args: append([]string(nil), args...)})
			if name == "svn" && len(args) >= 4 && args[0] == "status" && args[1] == "--quiet" && args[2] == "--" {
				if len(args) == 4 && args[3] == missing+"@" {
					return []byte("!       " + missing + "\n"), nil
				}
				return []byte("M       ticket\n"), nil
			}
			return nil, nil
		}
		if err := newBackend("svn", runMissing).Commit(root, "ticket: missing", []string{missing}); err != nil {
			t.Fatal(err)
		}
		wantMissing := [][]string{
			{"status", "--quiet", "--", missing + "@"},
			{"delete", "--force", "--", missing + "@"},
			{"status", "--quiet", "--", missing + "@"},
			{"commit", "-m", "ticket: missing", "--", missing + "@"},
		}
		if len(calls) != len(wantMissing) {
			t.Fatalf("missing %q calls=%q want %q", missing, calls, wantMissing)
		}
		for i, want := range wantMissing {
			if !reflect.DeepEqual(calls[i].args, want) {
				t.Fatalf("missing %q call %d=%q want %q", missing, i, calls[i].args, want)
			}
		}
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
		if len(args) > 0 && args[0] == "for-each-ref" {
			return nil, nil
		}
		if len(args) > 0 && args[0] == "symbolic-ref" {
			return []byte("main\n"), nil
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
		"for-each-ref --format=%(upstream:remotename)%00%(upstream:remoteref) HEAD",
		"symbolic-ref --quiet --short HEAD",
		"for-each-ref --format=%(upstream:remotename)%00%(upstream:remoteref) refs/heads/main",
		"rev-parse --show-toplevel",
		"rev-parse --abbrev-ref --symbolic-full-name @{u}",
		"for-each-ref --format=%(upstream:remotename)%00%(upstream:remoteref) HEAD",
		"symbolic-ref --quiet --short HEAD",
		"for-each-ref --format=%(upstream:remotename)%00%(upstream:remoteref) refs/heads/main",
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

func TestGitValidateLocalRejectsTrackedRuntimeState(t *testing.T) {
	trackedErr := exec.Command("sh", "-c", "exit 0")
	run := func(_ string, name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) > 0 && args[0] == "ls-files" {
			return []byte(".local/current\n"), trackedErr.Run()
		}
		return nil, nil
	}
	err := newBackend("git", run).ValidateLocal("/tickets")
	if !hasCode(err, contract.ErrInvalidRepository) || !strings.Contains(err.Error(), "versioned Git") {
		t.Fatalf("tracked .local validation: %v", err)
	}
}

func TestGitValidateLocalAllowsUntrackedRuntimeState(t *testing.T) {
	missingErr := exec.Command("sh", "-c", "exit 1")
	run := func(_ string, name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) > 0 && args[0] == "ls-files" {
			return nil, missingErr.Run()
		}
		return nil, nil
	}
	if err := newBackend("git", run).ValidateLocal("/tickets"); err != nil {
		t.Fatalf("untracked .local validation: %v", err)
	}
}

func TestSVNValidateLocalDistinguishesVersionedState(t *testing.T) {
	normal := newBackend("svn", func(_ string, _ string, _ ...string) ([]byte, error) {
		return []byte(`<status><target><entry path=".local"><wc-status item="normal"/></entry></target></status>`), nil
	})
	if err := normal.ValidateLocal("/tickets"); !hasCode(err, contract.ErrInvalidRepository) {
		t.Fatalf("versioned SVN .local was accepted: %v", err)
	}
	unversioned := newBackend("svn", func(_ string, _ string, _ ...string) ([]byte, error) {
		return []byte(`<status><target><entry path=".local"><wc-status item="unversioned"/></entry></target></status>`), nil
	})
	if err := unversioned.ValidateLocal("/tickets"); err != nil {
		t.Fatalf("unversioned SVN .local was rejected: %v", err)
	}
	failing := newBackend("svn", func(_ string, _ string, _ ...string) ([]byte, error) {
		return []byte("svn unavailable"), errors.New("exit status 1")
	})
	if err := failing.ValidateLocal("/tickets"); !hasCode(err, contract.ErrIOError) {
		t.Fatalf("SVN validation failure was not surfaced: %v", err)
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
	for _, args := range [][]string{
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "Ticket Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("ticket\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "README"}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := newBackend("git", execCommand).Update(root); err != nil {
		t.Fatalf("local Git repository update: %v", err)
	}
	if err := newBackend("git", execCommand).Publish(root); err != nil {
		t.Fatalf("local Git repository publish: %v", err)
	}
}

func TestGitPublishRejectsMalformedUpstreamTarget(t *testing.T) {
	err := newBackend("git", func(_ string, _ string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "rev-parse" && len(args) > 1 && args[1] == "--abbrev-ref" {
			return []byte("not-a-remote-ref\n"), nil
		}
		return []byte("/repo\n"), nil
	}).Publish("/tickets")
	if !hasCode(err, contract.ErrIOError) || !strings.Contains(err.Error(), "explicit remote and ref") {
		t.Fatalf("malformed upstream was not rejected: %v", err)
	}
}

func TestGitPublishUsesExactRemoteAndRefArguments(t *testing.T) {
	for _, tc := range []struct {
		name     string
		upstream string
		metadata string
		want     []string
	}{
		{name: "option-looking remote", upstream: "-x/main", metadata: "-x\x00refs/heads/main\n", want: []string{"push", "--", "-x", "HEAD:refs/heads/main"}},
		{name: "overlapping remote names", upstream: "team/origin/main", metadata: "team\x00refs/heads/origin/main\n", want: []string{"push", "--", "team", "HEAD:refs/heads/origin/main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			run := func(_ string, _ string, args ...string) ([]byte, error) {
				switch {
				case len(args) > 1 && args[0] == "rev-parse" && args[1] == "--abbrev-ref":
					return []byte(tc.upstream + "\n"), nil
				case len(args) > 0 && args[0] == "for-each-ref":
					return []byte(tc.metadata), nil
				case len(args) > 0 && args[0] == "push":
					got = append([]string(nil), args...)
					return nil, nil
				default:
					return []byte("/repo\n"), nil
				}
			}
			if err := newBackend("git", run).Publish("/tickets"); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("push args=%q want %q", got, tc.want)
			}
		})
	}
}

func TestGitPublishUsesConfiguredRemoteWithOverlappingNames(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
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
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("ticket\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README")
	runGit("commit", "-m", "initial")
	runGit("branch", "-M", "main")
	runGit("remote", "add", "team", "https://example.invalid/team.git")
	runGit("remote", "add", "team-origin", "https://example.invalid/origin.git")
	runGit("config", "branch.main.remote", "team-origin")
	runGit("config", "branch.main.merge", "refs/heads/main")
	var got []string
	run := func(root, name string, args ...string) ([]byte, error) {
		if name == "git" && len(args) > 0 && args[0] == "push" {
			got = append([]string(nil), args...)
			return nil, nil
		}
		return execCommand(root, name, args...)
	}
	if err := newBackend("git", run).Publish(dir); err != nil {
		t.Fatal(err)
	}
	want := []string{"push", "--", "team-origin", "HEAD:refs/heads/main"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("push args=%q want %q", got, want)
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
		if args[0] == "remote" {
			return []byte("origin\n"), nil
		}
		if args[0] == "for-each-ref" {
			return []byte("origin\x00refs/heads/main\n"), nil
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
			unrelatedStaged := filepath.Join(root, "unrelated-staged.txt")
			if err := os.WriteFile(unrelatedStaged, []byte("must remain staged\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			unrelatedRoot := filepath.Join(root, "unrelated-untracked.txt")
			if err := os.WriteFile(unrelatedRoot, []byte("must remain untracked\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, ".local"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".local", "current"), []byte("local\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runGit("add", "source.txt", "tickets/unrelated-staged.txt")
			backend := newBackend("git", execCommand)
			if err := backend.Commit(root, "ticket: update", []string{"TASK.md"}); err != nil {
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
			if strings.TrimSpace(string(out)) != "source.txt\ntickets/unrelated-staged.txt" {
				t.Fatalf("unrelated staged paths=%q", out)
			}
			if _, err := os.Stat(unrelatedRoot); err != nil {
				t.Fatalf("unrelated root file was removed: %v", err)
			}
			cmd = exec.Command("git", "status", "--porcelain", "--", "tickets/unrelated-untracked.txt")
			cmd.Dir = dir
			out, err = cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(out)) != "?? tickets/unrelated-untracked.txt" {
				t.Fatalf("unrelated root file was staged or committed: %q", out)
			}
		})
	}
}

func TestGitCommitIncludesExactTicketDeletion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "tickets")
	ticketID := "20260920-12345"
	ticketDir := filepath.Join(root, ticketID)
	if err := os.MkdirAll(ticketDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ticketDir, "TASK.md"), []byte("ticket\n"), 0o644); err != nil {
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
	runGit("add", ".")
	runGit("commit", "-m", "initial")
	if err := os.RemoveAll(ticketDir); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(root, "unrelated.txt")
	if err := os.WriteFile(unrelated, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := newBackend("git", execCommand).Commit(root, "ticket: delete", []string{ticketID}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "show", "--format=", "--name-status", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "D\ttickets/"+ticketID+"/TASK.md" {
		t.Fatalf("deletion commit=%q", out)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated file missing: %v", err)
	}
	cmd = exec.Command("git", "status", "--porcelain", "--", "tickets/unrelated.txt")
	cmd.Dir = dir
	out, err = cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "?? tickets/unrelated.txt" {
		t.Fatalf("unrelated deletion file status=%q", out)
	}
}

func hasCode(err error, want contract.ErrorCode) bool {
	var ce *contract.Error
	return errors.As(err, &ce) && ce.Code == want
}
