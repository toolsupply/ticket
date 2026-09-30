//go:build windows

package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Native Windows gates. Cross-compilation and vet are not verification:
// these tests exercise the real ReplaceFileW and LockFileEx contracts and
// must be run on native Windows.

func TestWindowsReplacePublishesCompleteObject(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("old bytes\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	tmp := filepath.Join(dir, "f.txt.new")
	if err := os.WriteFile(tmp, []byte("new complete bytes\n"), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	if err := publishReplace(target, tmp); err != nil {
		t.Fatalf("publishReplace: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "new complete bytes\n" {
		t.Fatalf("target not replaced: %q", data)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("replacement source still present (ReplaceFileW moves it)")
	}
}

func TestWindowsReplaceFailureLeavesTargetIntact(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("old bytes\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	// A missing replacement source is a failure, not a silent success:
	// ReplaceFileW fails, the error surfaces, and the target is intact.
	if err := publishReplace(target, filepath.Join(dir, "missing.tmp")); err == nil {
		t.Fatal("publishReplace with a missing source must fail")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "old bytes\n" {
		t.Fatalf("target changed after failed publish: %q", data)
	}
	// A subsequent successful publish still works after the failure.
	tmp := filepath.Join(dir, "ok.tmp")
	if err := os.WriteFile(tmp, []byte("new bytes\n"), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	if err := publishReplace(target, tmp); err != nil {
		t.Fatalf("publishReplace after failure: %v", err)
	}
	data, err = os.ReadFile(target)
	if err != nil || string(data) != "new bytes\n" {
		t.Fatalf("target not replaced after failure case: %q (%v)", data, err)
	}
}

func TestWindowsRootReplaceRetriesTransientAccessDenied(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json.new"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write replacement: %v", err)
	}

	original := rootRename
	defer func() { rootRename = original }()
	attempts := 0
	rootRename = func(root *os.Root, oldname, newname string) error {
		attempts++
		if attempts < 3 {
			return syscall.ERROR_ACCESS_DENIED
		}
		return original(root, oldname, newname)
	}
	if err := publishReplaceRoot(root, "config.json", "config.json.new"); err != nil {
		t.Fatalf("publish after transient access denied: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("root rename attempts=%d want 3", attempts)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || string(data) != "new\n" {
		t.Fatalf("replacement target=%q err=%v", data, err)
	}
}

func TestWindowsRootReplaceRetriesTransientSharingViolation(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json.new"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write replacement: %v", err)
	}

	original := rootRename
	defer func() { rootRename = original }()
	attempts := 0
	rootRename = func(root *os.Root, oldname, newname string) error {
		attempts++
		if attempts < 3 {
			return windowsErrorSharingViolation
		}
		return original(root, oldname, newname)
	}
	if err := publishReplaceRoot(root, "config.json", "config.json.new"); err != nil {
		t.Fatalf("publish after transient sharing violation: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("root rename attempts=%d want 3", attempts)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || string(data) != "new\n" {
		t.Fatalf("replacement target=%q err=%v", data, err)
	}
}

func TestWindowsRootReplaceDoesNotRetryOtherPermissionErrors(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()
	original := rootRename
	defer func() { rootRename = original }()
	attempts := 0
	rootRename = func(*os.Root, string, string) error {
		attempts++
		return fs.ErrPermission
	}
	err = publishReplaceRoot(root, "config.json", "config.json.new")
	if !errors.Is(err, fs.ErrPermission) || attempts != 1 {
		t.Fatalf("replacement error=%v attempts=%d, want permission error after one attempt", err, attempts)
	}
}

func TestWindowsBoundedReadRetriesSharingViolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata")
	if err := os.WriteFile(path, []byte("bounded metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()

	originalPathOpen, originalRootOpen, originalSleep := windowsBoundedPathOpen, windowsBoundedRootOpen, windowsBoundedRetrySleep
	defer func() {
		windowsBoundedPathOpen = originalPathOpen
		windowsBoundedRootOpen = originalRootOpen
		windowsBoundedRetrySleep = originalSleep
	}()
	windowsBoundedRetrySleep = func(time.Duration) {}

	t.Run("path open", func(t *testing.T) {
		attempts := 0
		windowsBoundedPathOpen = func(path string) (*os.File, error) {
			attempts++
			if attempts < 3 {
				return nil, windowsErrorSharingViolation
			}
			return originalPathOpen(path)
		}
		data, err := ReadBoundedFile(path, 100)
		if err != nil || string(data) != "bounded metadata" {
			t.Fatalf("bounded path read=%q err=%v", data, err)
		}
		if attempts != 3 {
			t.Fatalf("path open attempts=%d want 3", attempts)
		}
	})

	t.Run("rooted open", func(t *testing.T) {
		attempts := 0
		windowsBoundedRootOpen = func(root *os.Root, name string, flags int, perm os.FileMode) (*os.File, error) {
			attempts++
			if attempts < 3 {
				return nil, windowsErrorSharingViolation
			}
			return originalRootOpen(root, name, flags, perm)
		}
		data, err := ReadBoundedRoot(root, "metadata", 100)
		if err != nil || string(data) != "bounded metadata" {
			t.Fatalf("bounded rooted read=%q err=%v", data, err)
		}
		if attempts != 3 {
			t.Fatalf("rooted open attempts=%d want 3", attempts)
		}
	})
}

func TestWindowsBoundedReadDoesNotRetryOtherErrors(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()

	originalPathOpen, originalRootOpen, originalSleep := windowsBoundedPathOpen, windowsBoundedRootOpen, windowsBoundedRetrySleep
	defer func() {
		windowsBoundedPathOpen = originalPathOpen
		windowsBoundedRootOpen = originalRootOpen
		windowsBoundedRetrySleep = originalSleep
	}()
	windowsBoundedRetrySleep = func(time.Duration) { t.Fatal("non-sharing error was retried") }
	wantErr := fs.ErrPermission

	attempts := 0
	windowsBoundedPathOpen = func(string) (*os.File, error) {
		attempts++
		return nil, wantErr
	}
	if _, err := ReadBoundedFile(filepath.Join(t.TempDir(), "unused"), 100); !errors.Is(err, wantErr) || attempts != 1 {
		t.Fatalf("path open error=%v attempts=%d, want original error after one attempt", err, attempts)
	}

	attempts = 0
	windowsBoundedRootOpen = func(*os.Root, string, int, os.FileMode) (*os.File, error) {
		attempts++
		return nil, wantErr
	}
	if _, err := ReadBoundedRoot(root, "unused", 100); !errors.Is(err, wantErr) || attempts != 1 {
		t.Fatalf("rooted open error=%v attempts=%d, want original error after one attempt", err, attempts)
	}
}

func TestWindowsBoundedReadStopsAfterSharingRetries(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()

	originalPathOpen, originalRootOpen, originalSleep := windowsBoundedPathOpen, windowsBoundedRootOpen, windowsBoundedRetrySleep
	defer func() {
		windowsBoundedPathOpen = originalPathOpen
		windowsBoundedRootOpen = originalRootOpen
		windowsBoundedRetrySleep = originalSleep
	}()
	windowsBoundedRetrySleep = func(time.Duration) {}

	attempts := 0
	windowsBoundedPathOpen = func(string) (*os.File, error) {
		attempts++
		return nil, windowsErrorSharingViolation
	}
	if _, err := ReadBoundedFile(filepath.Join(t.TempDir(), "unused"), 100); err != windowsErrorSharingViolation {
		t.Fatalf("persistent path sharing error=%v, want original sharing violation", err)
	}
	if want := windowsBoundedReadOpenRetries + 1; attempts != want {
		t.Fatalf("persistent path open attempts=%d want %d", attempts, want)
	}

	attempts = 0
	windowsBoundedRootOpen = func(*os.Root, string, int, os.FileMode) (*os.File, error) {
		attempts++
		return nil, windowsErrorSharingViolation
	}
	if _, err := ReadBoundedRoot(root, "unused", 100); err != windowsErrorSharingViolation {
		t.Fatalf("persistent rooted sharing error=%v, want original sharing violation", err)
	}
	if want := windowsBoundedReadOpenRetries + 1; attempts != want {
		t.Fatalf("persistent rooted open attempts=%d want %d", attempts, want)
	}
}

func TestWindowsLockExclusivity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock")
	f1, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	defer f1.Close()
	if err := acquire(f1, 2*time.Second); err != nil {
		t.Fatalf("acquire f1: %v", err)
	}

	// A second handle must time out while f1 holds the exclusive lock.
	f2, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open second handle: %v", err)
	}
	defer f2.Close()
	if err := acquire(f2, 300*time.Millisecond); err == nil {
		t.Fatalf("second acquire must fail while the lock is held")
	} else if err != errLockTimeout {
		t.Fatalf("expected lock timeout, got %v", err)
	}

	// After release, acquisition succeeds again.
	release(f1)
	f3, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open third handle: %v", err)
	}
	defer f3.Close()
	if err := acquire(f3, 2*time.Second); err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	release(f3)
}
