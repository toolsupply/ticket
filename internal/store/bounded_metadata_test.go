package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
)

func TestLoadConfigRejectsOversizedMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(strings.Repeat("x", int(configMaxBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(root)
	if !hasStoreCode(err, contract.ErrFileTooLarge) {
		t.Fatalf("oversized config error=%v", err)
	}
}

func TestLoadConfigAcceptsExactLimitAndRejectsLimitPlusOne(t *testing.T) {
	root := t.TempDir()
	base := []byte(`{"format_version":1}`)
	exact := append(append([]byte(nil), base...), []byte(strings.Repeat(" ", int(configMaxBytes)-len(base)))...)
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, exact, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(root); err != nil {
		t.Fatalf("exact config limit: %v", err)
	}
	if err := os.WriteFile(path, append(exact, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(root); !hasStoreCode(err, contract.ErrFileTooLarge) {
		t.Fatalf("config limit+1 error=%v", err)
	}
}

func TestChangeStateRejectsOversizedMarker(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".local"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".local", "change"), []byte(strings.Repeat("x", int(changeMarkerMaxBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeState(root); !errors.Is(err, ErrReadLimit) {
		t.Fatalf("oversized change marker error=%v", err)
	}
}

func TestChangeStateAcceptsExactLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".local"), 0o700); err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("x", int(changeMarkerMaxBytes))
	if err := os.WriteFile(filepath.Join(root, ".local", "change"), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ChangeState(root); err != nil || got != value {
		t.Fatalf("exact change marker: value=%q err=%v", got, err)
	}
}

func TestInitRejectsOversizedOwnedMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(strings.Repeat("x", 128<<10+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InitRoot(root); !hasStoreCode(err, contract.ErrFileTooLarge) {
		t.Fatalf("oversized init metadata: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("init modified rejected target: %v", err)
	}
}

func TestReadBoundedFileReadsAtMostLimitPlusOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata")
	if err := os.WriteFile(path, []byte("123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBoundedFile(path, 8); !errors.Is(err, ErrReadLimit) {
		t.Fatalf("limit error=%v", err)
	}
	data, err := ReadBoundedFile(path, 9)
	if err != nil || string(data) != "123456789" {
		t.Fatalf("boundary read=%q err=%v", data, err)
	}
}

func hasStoreCode(err error, want contract.ErrorCode) bool {
	var ce *contract.Error
	return errors.As(err, &ce) && ce.Code == want
}
