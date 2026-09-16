package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ticket/internal/contract"
	"ticket/internal/testutil"
)

// initTicketRoot creates a ticket repository at base/tickets and
// returns the ticket root path.
func initTicketRoot(t testing.TB, base string) string {
	t.Helper()
	root := filepath.Join(base, "tickets")
	if _, err := InitRoot(root); err != nil {
		t.Fatalf("init: %v", err)
	}
	return root
}

// S01: init is idempotent.
func TestInitIdempotent(t *testing.T) {
	dir := t.TempDir()
	created, err := InitRoot(dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !created {
		t.Fatalf("created=%v", created)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("config.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err != nil {
		t.Fatalf("README.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Fatalf(".gitignore missing: %v", err)
	}
	configBytes, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || string(configBytes) != "{\"format_version\":1}\n" {
		t.Fatalf("config bytes=%q err=%v", configBytes, err)
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.FormatVersion != 1 {
		t.Fatalf("format version=%d", cfg.FormatVersion)
	}
	// Second init: no change.
	created2, err := InitRoot(dir)
	if err != nil {
		t.Fatalf("second init: %v", err)
	}
	if created2 {
		t.Fatalf("second init unexpectedly created files: created=%v", created2)
	}
}

// S02: init into a non-empty target fails without touching user data.
func TestInitNonEmptyFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := InitRoot(dir)
	if err == nil {
		t.Fatal("expected error for non-empty target")
	}
	var ce *contract.Error
	if !asContract(err, &ce) || ce.Code != contract.ErrInvalidRepository {
		t.Fatalf("wrong error: %+v", err)
	}
}

func asContract(err error, ce **contract.Error) bool {
	return errors.As(err, ce)
}

// S03: discovery finds the nearest ticket root from nested directories.
func TestDiscoverNested(t *testing.T) {
	base := t.TempDir()
	root := initTicketRoot(t, base)
	deep := filepath.Join(base, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(deep)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if got != root {
		t.Fatalf("discovered %q want %q", got, root)
	}
	// From the ticket root itself.
	got, err = Discover(root)
	if err != nil || got != root {
		t.Fatalf("discover from root: %q %v", got, err)
	}
}

func TestDiscoverCrossesNestedGitRoot(t *testing.T) {
	base := t.TempDir()
	root := initTicketRoot(t, base)
	deep := filepath.Join(base, "src", "component")
	if err := os.MkdirAll(filepath.Join(deep, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(deep)
	if err != nil {
		t.Fatalf("discover through nested git root: %v", err)
	}
	if got != root {
		t.Fatalf("discovered %q want %q", got, root)
	}
}

func TestDiscoverExplicitTicketRoot(t *testing.T) {
	base := t.TempDir()
	moduleRoot := filepath.Join(base, "tickets", "module-a")
	if _, err := InitRoot(moduleRoot); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(base, "src", "component")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TICKET_ROOT", filepath.Join("..", "..", "tickets", "module-a"))
	got, err := Discover(deep)
	if err != nil {
		t.Fatalf("discover explicit root: %v", err)
	}
	if got != moduleRoot {
		t.Fatalf("discovered %q want %q", got, moduleRoot)
	}
}

// S04/S06: shorthand and full ID resolution, ambiguity, dangling.
func TestResolveID(t *testing.T) {
	base := t.TempDir()
	src := testutil.NewDeterministicSource(7)
	root := initTicketRoot(t, base)
	st, err := Open(root, OpenOptions{IDSource: src})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id1, err := st.NewID("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, id1), 0o755); err != nil {
		t.Fatal(err)
	}
	full, err := st.ResolveID(id1, true)
	if err != nil || full != id1 {
		t.Fatalf("full resolve: %q %v", full, err)
	}
	short := id1[:7]
	got, err := st.ResolveID(short, true)
	if err != nil || got != id1 {
		t.Fatalf("shorthand resolve: %q %v", got, err)
	}
	// Non-existent reference (same length, different body).
	last := id1[len(id1)-1]
	if last == 'b' {
		last = 'a'
	} else {
		last = 'b'
	}
	if _, err := st.ResolveID(id1[:len(id1)-1]+string(last), true); err == nil {
		t.Fatal("expected not_found")
	}
	for _, invalid := range []string{"x", "2026-", "202613", "20260913_"} {
		if _, err := st.ResolveID(invalid, true); err == nil {
			t.Fatalf("invalid shorthand accepted: %q", invalid)
		} else {
			var ce *contract.Error
			if !asContract(err, &ce) || ce.Code != contract.ErrInvalidArgument {
				code := contract.ErrorCode("<unknown>")
				if ce != nil {
					code = ce.Code
				}
				t.Fatalf("invalid shorthand %q: code=%s want invalid_argument", invalid, code)
			}
		}
	}
}

// S05: entropy failure surfaces randomness_unavailable.
func TestNewIDFailingSource(t *testing.T) {
	base := t.TempDir()
	initTicketRoot(t, base)
	st, err := Open(base, OpenOptions{IDSource: testutil.FailingSource{}})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.NewID(""); err == nil {
		t.Fatal("expected randomness_unavailable")
	}
}

// P01: a second opener cannot take the ticket-root lock while the first
// holds it; it times out, and succeeds after release.
func TestLockExclusion(t *testing.T) {
	base := t.TempDir()
	initTicketRoot(t, base)
	st, err := Open(base, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	other, err := AcquireLock(filepath.Join(st.Root, ".local"), 300*time.Millisecond)
	if err == nil {
		other.Release()
		t.Fatal("expected lock_timeout")
	}
	var ce *contract.Error
	if !asContract(err, &ce) || ce.Code != contract.ErrLockTimeout {
		t.Fatalf("wrong lock error: %+v", err)
	}
	st.Close()
	other, err = AcquireLock(filepath.Join(st.Root, ".local"), time.Second)
	if err != nil {
		t.Fatalf("reopen after release: %v", err)
	}
	other.Release()
}

// P01/P05: publishing a ticket never overwrites an existing target.
func TestPublishTicketNoOverwrite(t *testing.T) {
	base := t.TempDir()
	src := testutil.NewDeterministicSource(1)
	root := initTicketRoot(t, base)
	st, err := Open(base, OpenOptions{IDSource: src})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.NewID("")
	if err != nil {
		t.Fatal(err)
	}
	err = st.PublishTicket(id, []byte("v1"), (1 << 20))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, id, "TASK.md")); err != nil {
		t.Fatalf("published file missing: %v", err)
	}
	// Overwrite attempt must fail as the internal sentinel.
	if err := st.PublishTicket(id, []byte("v2"), (1 << 20)); err != ErrTargetExists {
		t.Fatalf("expected ErrTargetExists, got %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, id, "TASK.md")); err != nil || string(data) != "v1" {
		t.Fatalf("target was clobbered: %q", data)
	}
	// Published content remains untouched by the failed collision.
	data, err := os.ReadFile(filepath.Join(root, id, "TASK.md"))
	if err != nil || string(data) != "v1" {
		t.Fatalf("published content changed: %q", data)
	}
}

func symlinkOrSkip(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
}

func TestDiscoverRejectsSymlinkedTicketRoot(t *testing.T) {
	base := t.TempDir()
	external := filepath.Join(base, "external")
	if _, err := InitRoot(external); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(external, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(base, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, external, filepath.Join(project, "tickets"))
	if _, err := Discover(project); err == nil {
		t.Fatal("symlinked ticket root was discovered")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "keep" {
		t.Fatalf("external sentinel changed: %q (%v)", data, err)
	}
}

func TestOpenRejectsSymlinkedRepositoryMetadata(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		base := t.TempDir()
		root := initTicketRoot(t, base)
		external := filepath.Join(base, "external-config.json")
		config, err := os.ReadFile(filepath.Join(root, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(external, config, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(root, "config.json")); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, external, filepath.Join(root, "config.json"))
		if _, err := Open(base, OpenOptions{}); err == nil {
			t.Fatal("symlinked config was accepted")
		} else if code, ok := err.(*contract.Error); !ok || code.Code != contract.ErrInvalidRepository {
			t.Fatalf("wrong error: %v", err)
		}
		data, err := os.ReadFile(external)
		if err != nil || !bytes.Equal(data, config) {
			t.Fatalf("external config changed: %q (%v)", data, err)
		}
	})

	t.Run("local", func(t *testing.T) {
		base := t.TempDir()
		root := initTicketRoot(t, base)
		external := filepath.Join(base, "external-local")
		if err := os.Mkdir(external, 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(external, "sentinel")
		if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(root, ".local")); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, external, filepath.Join(root, ".local"))
		if _, err := Open(base, OpenOptions{}); err == nil {
			t.Fatal("symlinked .local was accepted")
		} else if code, ok := err.(*contract.Error); !ok || code.Code != contract.ErrInvalidRepository {
			t.Fatalf("wrong error: %v", err)
		}
		data, err := os.ReadFile(sentinel)
		if err != nil || string(data) != "keep" {
			t.Fatalf("external local sentinel changed: %q (%v)", data, err)
		}
	})

	t.Run("lock", func(t *testing.T) {
		base := t.TempDir()
		root := initTicketRoot(t, base)
		external := filepath.Join(base, "external-lock")
		if err := os.WriteFile(external, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, external, filepath.Join(root, ".local", "lock"))
		if _, err := Open(base, OpenOptions{}); err == nil {
			t.Fatal("symlinked lock was accepted")
		} else if code, ok := err.(*contract.Error); !ok || code.Code != contract.ErrInvalidRepository {
			t.Fatalf("wrong error: %v", err)
		}
		data, err := os.ReadFile(external)
		if err != nil || string(data) != "keep" {
			t.Fatalf("external lock sentinel changed: %q (%v)", data, err)
		}
	})
}

func TestPublishTicketRejectsExternalSentinelCollision(t *testing.T) {
	base := t.TempDir()
	root := initTicketRoot(t, base)
	st, err := Open(base, OpenOptions{IDSource: testutil.NewDeterministicSource(1)})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.NewID("")
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(base, "external-ticket")
	if err := os.Mkdir(external, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(external, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, external, filepath.Join(root, id))
	if err := st.PublishTicket(id, []byte("new"), 1<<20); err != ErrTargetExists {
		t.Fatalf("expected collision, got %v", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "keep" {
		t.Fatalf("external ticket sentinel changed: %q (%v)", data, err)
	}
}

func TestTicketStorageRejectsTraversalIDs(t *testing.T) {
	base := t.TempDir()
	initTicketRoot(t, base)
	st, err := Open(base, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	external := filepath.Join(base, "external")
	if err := os.WriteFile(external, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceTask("../external", []byte("changed"), 1<<20); err == nil {
		t.Fatal("ReplaceTask accepted a traversal-shaped ID")
	}
	if err := st.PublishTicket("../external", []byte("changed"), 1<<20); err == nil {
		t.Fatal("PublishTicket accepted a traversal-shaped ID")
	}
	data, err := os.ReadFile(external)
	if err != nil || string(data) != "keep" {
		t.Fatalf("external sentinel changed: %q (%v)", data, err)
	}
}
