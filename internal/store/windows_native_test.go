//go:build windows

package store

import (
	"os"
	"path/filepath"
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
