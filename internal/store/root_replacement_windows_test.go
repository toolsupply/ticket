//go:build windows

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRootHandleBlocksRepositoryPathReplacement(t *testing.T) {
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
	oldRoot := root + ".old"
	if err := os.Rename(root, oldRoot); err == nil {
		t.Fatal("repository root replacement succeeded while rooted handle was open")
	}
	st.Close()
	if err := os.Rename(root, oldRoot); err != nil {
		t.Fatalf("rename repository root after close: %v", err)
	}
}

func TestStoreLocalHandleBlocksReplacement(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "tickets")
	if _, err := InitRoot(root); err != nil {
		t.Fatalf("init: %v", err)
	}
	st, err := Open(base, OpenOptions{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	oldLocal := filepath.Join(root, ".local-old")
	if err := os.Rename(filepath.Join(root, ".local"), oldLocal); err == nil {
		st.Close()
		t.Fatal(".local replacement succeeded while rooted handle was open")
	}
	st.Close()
	if err := os.Rename(filepath.Join(root, ".local"), oldLocal); err != nil {
		t.Fatalf("rename .local after close: %v", err)
	}
}

func TestReadTaskRejectsWindowsInterposedSymlink(t *testing.T) {
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
	id := "20260921-22346"
	if err := st.PublishTicket(id, []byte("managed\n"), 1<<20); err != nil {
		t.Fatalf("publish: %v", err)
	}
	target := filepath.Join(root, id, "TASK.md")
	managedOpenHook = func() {
		managedOpenHook = nil
		if err := os.Remove(target); err != nil {
			t.Errorf("interpose remove: %v", err)
			return
		}
		if err := os.Symlink(filepath.Join("..", "..", "config.json"), target); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}
	defer func() { managedOpenHook = nil }()
	if _, _, err := st.ReadTask(id, 1<<20); err == nil {
		t.Fatal("interposed symlink was accepted")
	}
}
