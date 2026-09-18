package store

import (
	"os"
	"runtime"
	"testing"
)

func TestLocalMarkerUsesRestrictiveDefaultAndPreservesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide portable permission-bit semantics")
	}

	path := t.TempDir() + "/marker"
	if err := writeLocalFile(path, []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("default marker mode = %04o, want 0600", got)
	}

	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeLocalFile(path, []byte("second\n")); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("preserved marker mode = %04o, want 0640", got)
	}
}
