// Package scm provides the optional source-control lifecycle around a ticket
// repository. Ticket domain code remains independent of source control.
package scm

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"ticket/internal/contract"
)

// Backend synchronizes and publishes a ticket repository using a native SCM
// command. Paths passed to Commit are relative to root.
type Backend interface {
	Update(root string) error
	Commit(root, message string, paths []string) error
	Publish(root string) error
}

type commandRunner func(dir, name string, args ...string) ([]byte, error)

type backend struct {
	kind string
	run  commandRunner
}

// Configure selects the optional SCM backend from the process environment.
// An unset or "none" backend disables all SCM commands.
func Configure() (Backend, error) {
	kind := strings.TrimSpace(os.Getenv("TICKET_SCM"))
	if kind == "" || kind == "none" {
		return nil, nil
	}
	if kind != "git" && kind != "svn" {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"Unsupported TICKET_SCM "+kind+"; use none, git, or svn.", nil)
	}
	mode := strings.TrimSpace(os.Getenv("TICKET_SCM_MODE"))
	if mode != "sync" {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"Unsupported TICKET_SCM_MODE; use sync.", nil)
	}
	if _, err := exec.LookPath(kind); err != nil {
		return nil, contract.NewError(contract.ErrIOError,
			"Configured SCM executable "+kind+" was not found.", nil)
	}
	return &backend{kind: kind, run: execCommand}, nil
}

func newBackend(kind string, run commandRunner) Backend {
	return &backend{kind: kind, run: run}
}

func (b *backend) Update(root string) error {
	switch b.kind {
	case "git":
		// A local repository without an upstream is a valid synchronization
		// target. Validate the work tree first, then pull only when the
		// current branch has an upstream configured.
		if err := b.command(root, "git rev-parse", "rev-parse", "--show-toplevel"); err != nil {
			return err
		}
		if _, err := b.run(root, b.kind, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err != nil {
			return nil
		}
		return b.command(root, "git pull", "pull", "--ff-only")
	case "svn":
		return b.command(root, "svn update", "update", ".")
	default:
		return unsupportedBackend(b.kind)
	}
}

func (b *backend) Commit(root, message string, paths []string) error {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	switch b.kind {
	case "git":
		// Add only the ticket-root path, excluding ticket-root-local state.
		// --only then prevents unrelated pre-staged checkout changes from
		// entering the ticket commit.
		addArgs := []string{"add", "--"}
		addArgs = append(addArgs, gitScopedPaths(paths)...)
		if err := b.command(root, "git add", addArgs...); err != nil {
			return err
		}
		pending, err := b.gitPending(root, paths)
		if err != nil {
			return err
		}
		if !pending {
			return nil
		}
		args := []string{"commit", "--only", "-m", message, "--"}
		args = append(args, gitScopedPaths(paths)...)
		return b.command(root, "git commit", args...)
	case "svn":
		if err := b.svnAdd(root, paths); err != nil {
			return err
		}
		scoped, err := svnScopedPaths(root, paths)
		if err != nil {
			return err
		}
		if len(scoped) == 0 {
			return nil
		}
		pending, err := b.svnPending(root, scoped)
		if err != nil {
			return err
		}
		if !pending {
			return nil
		}
		args := []string{"commit"}
		args = append(args, scoped...)
		args = append(args, "-m", message)
		return b.command(root, "svn commit", args...)
	default:
		return unsupportedBackend(b.kind)
	}
}

func (b *backend) gitPending(root string, paths []string) (bool, error) {
	args := []string{"diff", "--cached", "--quiet", "--"}
	args = append(args, gitScopedPaths(paths)...)
	_, err := b.run(root, b.kind, args...)
	if err == nil {
		return false, nil
	}
	if code, ok := exitCode(err); ok && code == 1 {
		return true, nil
	}
	return false, b.failure("git diff", nil, err)
}

func (b *backend) svnPending(root string, paths []string) (bool, error) {
	args := append([]string{"status", "--quiet"}, paths...)
	output, err := b.run(root, b.kind, args...)
	if err != nil {
		return false, b.failure("svn status", output, err)
	}
	return len(strings.TrimSpace(string(output))) > 0, nil
}

func gitScopedPaths(paths []string) []string {
	result := append([]string(nil), paths...)
	for _, path := range paths {
		if path == "." {
			result = append(result, ":(exclude).local")
		}
	}
	return result
}

func (b *backend) svnAdd(root string, paths []string) error {
	scoped, err := svnScopedPaths(root, paths)
	if err != nil {
		return err
	}
	for _, path := range scoped {
		if err := b.command(root, "svn add", "add", "--force", "--depth", "infinity", path); err != nil {
			return err
		}
	}
	return nil
}

func svnScopedPaths(root string, paths []string) ([]string, error) {
	if len(paths) != 1 || paths[0] != "." {
		result := make([]string, 0, len(paths))
		for _, path := range paths {
			if path != ".local" && path != ".git" && path != ".svn" {
				result = append(result, path)
			}
		}
		return result, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError, "Cannot inspect SCM ticket root: "+err.Error(), nil)
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		switch entry.Name() {
		case ".local", ".git", ".svn":
			continue
		default:
			result = append(result, entry.Name())
		}
	}
	return result, nil
}

func (b *backend) Publish(root string) error {
	switch b.kind {
	case "git":
		return b.command(root, "git push", "push")
	case "svn":
		// svn commit publishes the change.
		return nil
	default:
		return unsupportedBackend(b.kind)
	}
}

func (b *backend) command(dir, action string, args ...string) error {
	output, err := b.run(dir, b.kind, args...)
	if err == nil {
		return nil
	}
	return b.failure(action, output, err)
}

func (b *backend) failure(action string, output []byte, _ error) error {
	detail := strings.TrimSpace(string(output))
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	message := fmt.Sprintf("%s failed", action)
	if detail != "" {
		message += ": " + detail
	}
	return contract.NewError(contract.ErrIOError, message, nil)
}

func exitCode(err error) (int, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	return exitErr.ExitCode(), true
}

func execCommand(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func unsupportedBackend(kind string) error {
	return contract.NewError(contract.ErrInvalidArgument, "Unsupported SCM backend "+kind+".", nil)
}
