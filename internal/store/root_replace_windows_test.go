//go:build windows

package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsRootedReplaceTaskUsesRootReplacement(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "tickets")
	if _, err := InitRoot(root); err != nil {
		t.Fatalf("init: %v", err)
	}
	st, err := Open(base, OpenOptions{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	id := "20260921-32345"
	if err := st.PublishTicket(id, []byte("before\n"), 1<<20); err != nil {
		t.Fatalf("publish: %v", err)
	}
	changed, err := st.ReplaceTask(id, []byte("after\n"), 1<<20)
	if err != nil || !changed {
		t.Fatalf("rooted replacement: changed=%v err=%v", changed, err)
	}
	data, err := os.ReadFile(filepath.Join(root, id, "TASK.md"))
	if err != nil || string(data) != "after\n" {
		t.Fatalf("replacement bytes=%q err=%v", data, err)
	}
}

func TestWindowsRootedReplaceFailurePreservesTarget(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "tickets")
	if _, err := InitRoot(root); err != nil {
		t.Fatalf("init: %v", err)
	}
	st, err := Open(base, OpenOptions{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	id := "20260921-32346"
	if err := st.PublishTicket(id, []byte("before\n"), 1<<20); err != nil {
		t.Fatalf("publish: %v", err)
	}
	original := rootRename
	rootRename = func(*os.Root, string, string) error { return errors.New("injected rename failure") }
	defer func() { rootRename = original }()
	if _, err := st.ReplaceTask(id, []byte("after\n"), 1<<20); err == nil {
		t.Fatal("injected replacement failure was ignored")
	}
	data, err := os.ReadFile(filepath.Join(root, id, "TASK.md"))
	if err != nil || string(data) != "before\n" {
		t.Fatalf("failed replacement changed target: bytes=%q err=%v", data, err)
	}
}
