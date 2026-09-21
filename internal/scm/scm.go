// Package scm provides the optional source-control lifecycle around a ticket
// repository. Ticket domain code remains independent of source control.
package scm

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
)

// Backend synchronizes and publishes a ticket repository using a native SCM
// command. Paths passed to Commit are relative to root.
type Backend interface {
	Update(root string) error
	ValidateLocal(root string) error
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
	return ConfigureValues(os.Getenv("TICKET_SCM"), os.Getenv("TICKET_SCM_MODE"))
}

// ConfigureValues selects an SCM backend from already-resolved settings.
// Keeping environment lookup in Configure preserves the existing direct API
// while scoped CLI invocations can supply their effective values explicitly.
func ConfigureValues(kindValue, modeValue string) (Backend, error) {
	kind := strings.TrimSpace(kindValue)
	if err := ValidateValues(kindValue, modeValue); err != nil {
		return nil, err
	}
	if kind == "" || kind == "none" {
		return nil, nil
	}
	if _, err := exec.LookPath(kind); err != nil {
		return nil, contract.NewError(contract.ErrIOError,
			"Configured SCM executable "+kind+" was not found.", nil)
	}
	return &backend{kind: kind, run: execCommand}, nil
}

// ValidateValues checks SCM settings without looking up the executable. It is
// used while loading user scopes so unused scopes remain portable.
func ValidateValues(kindValue, modeValue string) error {
	kind := strings.TrimSpace(kindValue)
	if kind == "" || kind == "none" {
		return nil
	}
	if kind != "git" && kind != "svn" {
		return contract.NewError(contract.ErrInvalidArgument,
			"Unsupported TICKET_SCM "+kind+"; use none, git, or svn.", nil)
	}
	if strings.TrimSpace(modeValue) != "sync" {
		return contract.NewError(contract.ErrInvalidArgument,
			"Unsupported TICKET_SCM_MODE; use sync.", nil)
	}
	return nil
}

func newBackend(kind string, run commandRunner) Backend {
	return &backend{kind: kind, run: run}
}

func (b *backend) Update(root string) error {
	switch b.kind {
	case "git":
		hasUpstream, err := b.gitHasUpstream(root)
		if err != nil {
			return err
		}
		if !hasUpstream {
			return nil
		}
		return b.command(root, "git pull", "pull", "--ff-only")
	case "svn":
		return b.command(root, "svn update", "update", ".")
	default:
		return unsupportedBackend(b.kind)
	}
}

// ValidateLocal rejects runtime state that the configured SCM considers
// versioned. .local is process-owned state and must remain outside SCM so a
// synchronization cannot replace the live lock or local markers.
func (b *backend) ValidateLocal(root string) error {
	switch b.kind {
	case "git":
		output, err := b.run(root, b.kind, "ls-files", "--error-unmatch", "--", ".local")
		if err == nil {
			return contract.NewError(contract.ErrInvalidRepository,
				".local is versioned Git content; refusing to synchronize it.", nil)
		}
		if code, ok := exitCode(err); ok && code == 1 {
			return nil
		}
		return b.failure("git ls-files", output, err)
	case "svn":
		output, err := b.run(root, b.kind, "status", "--depth", "empty", "--xml", "--", svnLiteralPath(".local"))
		if err != nil {
			return b.failure("svn status", output, err)
		}
		status := string(output)
		if strings.Contains(status, "<wc-status") && !strings.Contains(status, `item="unversioned"`) {
			return contract.NewError(contract.ErrInvalidRepository,
				".local is versioned SVN content; refusing to synchronize it.", nil)
		}
		return nil
	default:
		return unsupportedBackend(b.kind)
	}
}

// gitHasUpstream treats a local-only repository as a valid transport target.
// The first probe preserves useful failures for paths that are not Git work
// trees; once the work tree is known to be valid, a missing upstream simply
// means there is no remote synchronization step to perform.
func (b *backend) gitHasUpstream(root string) (bool, error) {
	target, err := b.gitUpstream(root)
	return target != nil, err
}

type gitPublishTarget struct {
	remote string
	ref    string
}

// gitUpstream resolves the configured upstream into explicit push arguments.
// A local repository without an upstream is a supported no-op; malformed
// nonempty probe output is rejected rather than falling back to ambient
// push.default behavior.
func (b *backend) gitUpstream(root string) (*gitPublishTarget, error) {
	if err := b.command(root, "git rev-parse", "rev-parse", "--show-toplevel"); err != nil {
		return nil, err
	}
	// Probe the legacy upstream spelling as a compatibility check, but use
	// Git's format atoms below for the authoritative remote/ref split. The
	// rev-parse probe fails when an upstream is configured but not fetched yet.
	_, _ = b.run(root, b.kind, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	metadata, err := b.run(root, b.kind, "for-each-ref", "--format=%(upstream:remotename)%00%(upstream:remoteref)", "HEAD")
	if err != nil {
		return nil, b.failure("git for-each-ref", metadata, err)
	}
	if strings.TrimSpace(string(metadata)) == "" {
		branchData, branchErr := b.run(root, b.kind, "symbolic-ref", "--quiet", "--short", "HEAD")
		if branchErr != nil {
			return nil, nil
		}
		branch := strings.TrimSpace(string(branchData))
		if branch == "" {
			return nil, nil
		}
		metadata, err = b.run(root, b.kind, "for-each-ref", "--format=%(upstream:remotename)%00%(upstream:remoteref)", "refs/heads/"+branch)
		if err != nil {
			return nil, b.failure("git for-each-ref", metadata, err)
		}
	}
	metadataText := strings.TrimSpace(string(metadata))
	if metadataText == "" {
		return nil, nil
	}
	remote, remoteRef, ok := strings.Cut(metadataText, "\x00")
	remote = strings.TrimSpace(remote)
	remoteRef = strings.TrimSpace(remoteRef)
	if ok && remote == "" && remoteRef == "" {
		return nil, nil
	}
	if !ok || remote == "" || remoteRef == "" {
		return nil, contract.NewError(contract.ErrIOError,
			"Git upstream does not identify an explicit remote and ref; refusing to publish.", nil)
	}
	const headsPrefix = "refs/heads/"
	if !strings.HasPrefix(remoteRef, headsPrefix) {
		return nil, contract.NewError(contract.ErrIOError,
			"Git upstream does not identify an explicit remote and ref; refusing to publish.", nil)
	}
	ref := strings.TrimPrefix(remoteRef, headsPrefix)
	return &gitPublishTarget{remote: remote, ref: ref}, nil
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
		deleted, err := b.svnDeleteMissing(root, paths)
		if err != nil {
			return err
		}
		if err := b.svnAdd(root, paths); err != nil {
			return err
		}
		scoped, err := svnScopedPaths(root, paths, deleted)
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
		args := []string{"commit", "-m", message, "--"}
		args = append(args, svnLiteralPaths(scoped)...)
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
	args := append([]string{"status", "--quiet", "--"}, svnLiteralPaths(paths)...)
	output, err := b.run(root, b.kind, args...)
	if err != nil {
		return false, b.failure("svn status", output, err)
	}
	return len(strings.TrimSpace(string(output))) > 0, nil
}

func gitScopedPaths(paths []string) []string {
	rootScoped := false
	for _, path := range paths {
		if path == "." {
			rootScoped = true
			break
		}
	}
	if rootScoped {
		// Keep the exclusion wildcarded: a literal .local prefix makes git add
		// diagnose an ignored directory as an explicitly requested path.
		return []string{".", ":(exclude,glob)[.]local/**"}
	}
	result := append([]string(nil), paths...)
	return result
}

func (b *backend) svnAdd(root string, paths []string) error {
	scoped, err := svnScopedPaths(root, paths, nil)
	if err != nil {
		return err
	}
	for _, path := range scoped {
		if _, err := os.Lstat(filepath.Join(root, path)); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return contract.NewError(contract.ErrIOError, "Cannot inspect SVN path: "+err.Error(), nil)
		}
		if err := b.command(root, "svn add", "add", "--force", "--depth", "infinity", "--", svnLiteralPath(path)); err != nil {
			return err
		}
	}
	return nil
}

func (b *backend) svnDeleteMissing(root string, paths []string) ([]string, error) {
	var deleted []string
	for _, path := range paths {
		if path == "." || path == ".local" || path == ".git" || path == ".svn" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, path)); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, contract.NewError(contract.ErrIOError, "Cannot inspect SVN deletion path: "+err.Error(), nil)
		}
		output, err := b.run(root, b.kind, "status", "--quiet", "--", svnLiteralPath(path))
		if err != nil {
			return nil, b.failure("svn status", output, err)
		}
		status := svnDeletionStatus(output)
		if status == "" {
			continue
		}
		if status == "missing" {
			if err := b.command(root, "svn delete", "delete", "--force", "--", svnLiteralPath(path)); err != nil {
				return nil, err
			}
		}
		deleted = append(deleted, path)
	}
	return deleted, nil
}

func svnDeletionStatus(output []byte) string {
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case '!':
			return "missing"
		case 'D':
			return "scheduled"
		}
	}
	return ""
}

// svnLiteralPath terminates a path with an empty peg revision so Subversion
// treats embedded '@' characters as literal filename data. Callers must also
// place an option terminator before the returned paths.
func svnLiteralPath(path string) string {
	return path + "@"
}

func svnLiteralPaths(paths []string) []string {
	result := make([]string, len(paths))
	for i, path := range paths {
		result[i] = svnLiteralPath(path)
	}
	return result
}

func svnScopedPaths(root string, paths, deleted []string) ([]string, error) {
	rootScoped := false
	for _, path := range paths {
		if path == "." {
			rootScoped = true
			break
		}
	}
	if !rootScoped {
		result := make([]string, 0, len(paths)+len(deleted))
		for _, path := range paths {
			if path == ".local" || path == ".git" || path == ".svn" {
				continue
			}
			if _, err := os.Lstat(filepath.Join(root, path)); err == nil {
				result = append(result, path)
			} else if !os.IsNotExist(err) {
				return nil, contract.NewError(contract.ErrIOError, "Cannot inspect SCM path: "+err.Error(), nil)
			}
		}
		result = appendMissingPaths(result, deleted)
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
	seen := make(map[string]bool, len(result))
	for _, path := range result {
		seen[path] = true
	}
	for _, path := range deleted {
		if path != "." && path != ".local" && path != ".git" && path != ".svn" && !seen[path] {
			result = append(result, path)
			seen[path] = true
		}
	}
	return result, nil
}

func appendMissingPaths(paths, additions []string) []string {
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		seen[path] = true
	}
	for _, path := range additions {
		if !seen[path] {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	return paths
}

func (b *backend) Publish(root string) error {
	switch b.kind {
	case "git":
		target, err := b.gitUpstream(root)
		if err != nil {
			return err
		}
		if target == nil {
			return nil
		}
		return b.command(root, "git push", "push", "--", target.remote, "HEAD:refs/heads/"+target.ref)
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
