package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFailedReplacementRecoversMissingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "TASK.md")
	tmp := filepath.Join(dir, "TASK.md.tmp")
	if err := os.WriteFile(tmp, []byte("complete replacement\n"), 0o600); err != nil {
		t.Fatalf("write temporary replacement: %v", err)
	}

	// This models the documented Windows ReplaceFileW failure shape: the old
	// target has disappeared while the complete replacement is still present
	// under its temporary name.
	if err := recoverFailedReplacement(target, tmp); err != nil {
		t.Fatalf("recover failed replacement: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read recovered target: %v", err)
	}
	if string(data) != "complete replacement\n" {
		t.Fatalf("recovered target: %q", data)
	}
	if _, err := os.Lstat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temporary replacement remains after recovery: %v", err)
	}
}

func TestFailedReplacementCleansTemporaryWhenTargetSurvives(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "TASK.md")
	tmp := filepath.Join(dir, "TASK.md.tmp")
	if err := os.WriteFile(target, []byte("original\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(tmp, []byte("complete replacement\n"), 0o600); err != nil {
		t.Fatalf("write temporary replacement: %v", err)
	}

	if err := recoverFailedReplacement(target, tmp); err != nil {
		t.Fatalf("clean failed replacement: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read surviving target: %v", err)
	}
	if string(data) != "original\n" {
		t.Fatalf("surviving target changed: %q", data)
	}
	if _, err := os.Lstat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temporary replacement remains after cleanup: %v", err)
	}
}

func TestFailedReplacementKeepsTemporaryWhenRecoveryFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing", "TASK.md")
	tmp := filepath.Join(dir, "TASK.md.tmp")
	if err := os.WriteFile(tmp, []byte("complete replacement\n"), 0o600); err != nil {
		t.Fatalf("write temporary replacement: %v", err)
	}

	if err := recoverFailedReplacement(target, tmp); err == nil {
		t.Fatal("expected recovery failure")
	}
	if _, err := os.Lstat(tmp); err != nil {
		t.Fatalf("temporary replacement was removed after failed recovery: %v", err)
	}
}
