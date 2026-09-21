//go:build linux || darwin

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRootHandleSurvivesRepositoryPathReplacement(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "tickets")
	if created, err := InitRoot(root); err != nil || !created {
		t.Fatalf("init: created=%v err=%v", created, err)
	}
	st, err := Open(base, OpenOptions{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	id := "20260921-12345"
	if err := st.PublishTicket(id, []byte("before\n"), 1<<20); err != nil {
		t.Fatalf("publish: %v", err)
	}
	oldRoot := root + ".old"
	if err := os.Rename(root, oldRoot); err != nil {
		t.Fatalf("rename repository root: %v", err)
	}
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("replace repository root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte("{\"format_version\":1}\n"), 0o600); err != nil {
		t.Fatalf("write replacement config: %v", err)
	}
	data, _, err := st.ReadTask(id, 1<<20)
	if err != nil {
		t.Fatalf("read through stable root: %v", err)
	}
	if string(data) != "before\n" {
		t.Fatalf("read through stable root = %q", data)
	}
	if _, err := os.Stat(filepath.Join(oldRoot, id, "TASK.md")); err != nil {
		t.Fatalf("original repository lost its ticket: %v", err)
	}
}

func TestReadTaskRejectsInRootSymlink(t *testing.T) {
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
	id := "20260921-12346"
	if err := st.PublishTicket(id, []byte("managed\n"), 1<<20); err != nil {
		t.Fatalf("publish: %v", err)
	}
	target := filepath.Join(root, id, "TASK.md")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove task: %v", err)
	}
	if err := os.Symlink("../../config.json", target); err != nil {
		t.Fatalf("symlink task: %v", err)
	}
	if _, _, err := st.ReadTask(id, 1<<20); err == nil {
		t.Fatal("symlinked TASK.md was accepted")
	}
}

func TestReadTaskRejectsInterposedSymlink(t *testing.T) {
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
	id := "20260921-12347"
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
		if err := os.Symlink("../../config.json", target); err != nil {
			t.Errorf("interpose symlink: %v", err)
		}
	}
	defer func() { managedOpenHook = nil }()
	if _, _, err := st.ReadTask(id, 1<<20); err == nil {
		t.Fatal("interposed symlink was accepted")
	}
}

func TestReplaceTaskRejectsInterposedSymlink(t *testing.T) {
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
	id := "20260921-12348"
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
		if err := os.Symlink("../../config.json", target); err != nil {
			t.Errorf("interpose symlink: %v", err)
		}
	}
	defer func() { managedOpenHook = nil }()
	if _, err := st.ReplaceTask(id, []byte("changed\n"), 1<<20); err == nil {
		t.Fatal("interposed symlink was accepted for replacement")
	}
}
