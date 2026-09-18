package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConvenienceWritesDoNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	mustCLI(t, "init")
	id := exactlyOneJSONObject(t, mustCLI(t, "create", "Local marker safety", "Keep advisory markers confined."))["id"].(string)
	local := filepath.Join(dir, "tickets", ".local")
	outsideCurrent := filepath.Join(dir, "outside-current")
	outsideChange := filepath.Join(dir, "outside-change")
	if err := os.WriteFile(outsideCurrent, []byte("keep current\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideChange, []byte("keep change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(local, "current")
	change := filepath.Join(local, "change")
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(change); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideCurrent, current); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outsideChange, change); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if out, code := runCLI(t, "show", id); code != 0 {
		t.Fatalf("show with symlinked current marker: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "claim", id, "--actor", "worker"); code != 0 {
		t.Fatalf("claim with symlinked change marker: exit=%d out=%q", code, out)
	}
	for path, want := range map[string]string{
		outsideCurrent: "keep current\n",
		outsideChange:  "keep change\n",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("symlink target changed: %s: %q", path, data)
		}
	}
}
