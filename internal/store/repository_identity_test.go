package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/toolsupply/ticket/internal/contract"
)

const testRepositoryID = "8d1268c4-6a64-4b9b-95c9-d5598a150e86"

func makeLegacyRepository(t *testing.T) string {
	t.Helper()
	root := initTicketRoot(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(root, configFileName), []byte(`{"format_version":1,"name":"Legacy repository"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRepositoryIdentityBackfillsOnceAndPreservesName(t *testing.T) {
	root := makeLegacyRepository(t)
	first, err := Open(root, OpenOptions{Root: root})
	if err != nil {
		t.Fatalf("open legacy repository: %v", err)
	}
	id := first.Cfg.ID
	if !validRepositoryID(id) || first.Cfg.Name != "Legacy repository" {
		t.Fatalf("backfilled config=%+v", first.Cfg)
	}
	first.Close()

	stored, err := LoadConfig(root)
	if err != nil || stored.ID != id || stored.Name != "Legacy repository" {
		t.Fatalf("stored config=%+v err=%v", stored, err)
	}
	second, err := Open(root, OpenOptions{Root: root})
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	defer second.Close()
	if second.Cfg.ID != id {
		t.Fatalf("identity changed from %q to %q", id, second.Cfg.ID)
	}
}

func TestDeferredRepositoryIDBackfillPreservesConfigAndHoldsLock(t *testing.T) {
	root := makeLegacyRepository(t)
	path := filepath.Join(root, configFileName)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	st, err := Open(root, OpenOptions{Root: root, DeferRepositoryIDBackfill: true})
	if err != nil {
		t.Fatalf("open legacy repository without backfill: %v", err)
	}
	defer st.Close()
	if st.Cfg.ID != "" {
		t.Fatalf("deferred open returned repository ID %q", st.Cfg.ID)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != string(original) {
		t.Fatalf("deferred open changed config: %s err=%v", current, err)
	}

	other, err := AcquireLock(filepath.Join(root, ".local"), 50*time.Millisecond)
	if err == nil {
		other.Release()
		t.Fatal("deferred open did not retain the ticket-root lock")
	}
	var ce *contract.Error
	if !asContract(err, &ce) || ce.Code != contract.ErrLockTimeout {
		t.Fatalf("unexpected competing lock error: %v", err)
	}

	id, changed, err := st.EnsureRepositoryID()
	if err != nil || !changed || !validRepositoryID(id) {
		t.Fatalf("ensure repository ID id=%q changed=%t err=%v", id, changed, err)
	}
	if st.Cfg.ID != id {
		t.Fatalf("store config ID=%q want %q", st.Cfg.ID, id)
	}
	idAgain, changedAgain, err := st.EnsureRepositoryID()
	if err != nil || changedAgain || idAgain != id {
		t.Fatalf("repeat assurance id=%q changed=%t err=%v; want id=%q unchanged", idAgain, changedAgain, err, id)
	}
}

func TestDeferredRepositoryIDBackfillRejectsMalformedExistingID(t *testing.T) {
	root := initTicketRoot(t, t.TempDir())
	original := []byte(`{"format_version":1,"id":"invalid"}`)
	if err := os.WriteFile(filepath.Join(root, configFileName), original, 0o644); err != nil {
		t.Fatal(err)
	}
	if st, err := Open(root, OpenOptions{Root: root, DeferRepositoryIDBackfill: true}); err == nil {
		st.Close()
		t.Fatal("deferred open accepted malformed repository ID")
	} else if !strings.Contains(err.Error(), "canonical UUIDv4") {
		t.Fatalf("malformed ID error=%v", err)
	}
	current, err := os.ReadFile(filepath.Join(root, configFileName))
	if err != nil || string(current) != string(original) {
		t.Fatalf("malformed config was changed: %s err=%v", current, err)
	}
}

func TestEnsureRepositoryIDUsesCurrentConfig(t *testing.T) {
	root := makeLegacyRepository(t)
	st, err := Open(root, OpenOptions{Root: root, DeferRepositoryIDBackfill: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := os.WriteFile(filepath.Join(root, configFileName), []byte(`{"format_version":1,"id":"`+testRepositoryID+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	id, changed, err := st.EnsureRepositoryID()
	if err != nil || changed || id != testRepositoryID {
		t.Fatalf("assurance id=%q changed=%t err=%v; want current ID unchanged", id, changed, err)
	}
	if st.Cfg.ID != testRepositoryID {
		t.Fatalf("store config ID=%q want current ID %q", st.Cfg.ID, testRepositoryID)
	}
}

func TestRepositoryIdentityConcurrentLegacyBackfill(t *testing.T) {
	root := makeLegacyRepository(t)
	const workers = 12
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			st, err := Open(root, OpenOptions{Root: root})
			if err != nil {
				errs <- err
				return
			}
			ids <- st.Cfg.ID
			st.Close()
		}()
	}
	group.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent open: %v", err)
	}
	var identity string
	for id := range ids {
		if !validRepositoryID(id) {
			t.Errorf("invalid returned identity %q", id)
		}
		if identity == "" {
			identity = id
		} else if id != identity {
			t.Errorf("concurrent opens returned divergent IDs %q and %q", identity, id)
		}
	}
	stored, err := LoadConfig(root)
	if err != nil || stored.ID != identity {
		t.Fatalf("persisted identity=%q err=%v, concurrent identity=%q", stored.ID, err, identity)
	}
}

func TestRepositoryIdentitySurvivesMoveAndCopy(t *testing.T) {
	root := initTicketRoot(t, t.TempDir())
	original, err := Open(root, OpenOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	id := original.Cfg.ID
	original.Close()

	moved := filepath.Join(t.TempDir(), "moved-repository")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	openedMoved, err := Open(moved, OpenOptions{Root: moved})
	if err != nil || openedMoved.Cfg.ID != id {
		t.Fatalf("moved repository identity=%q want=%q err=%v", openedMovedID(openedMoved), id, err)
	}
	openedMoved.Close()

	copied := filepath.Join(t.TempDir(), "copied-repository")
	if err := os.MkdirAll(filepath.Join(copied, ".local"), 0o755); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(moved, configFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copied, configFileName), config, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copied, ".local", "lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	openedCopy, err := Open(copied, OpenOptions{Root: copied})
	if err != nil || openedCopy.Cfg.ID != id {
		t.Fatalf("copied repository identity=%q want=%q err=%v", openedCopyID(openedCopy), id, err)
	}
	openedCopy.Close()
}

func openedMovedID(st *Store) string {
	if st == nil {
		return ""
	}
	return st.Cfg.ID
}

func openedCopyID(st *Store) string { return openedMovedID(st) }

func TestRepositoryIdentityRejectsMalformedPersistedID(t *testing.T) {
	for _, test := range []struct {
		name string
		id   string
	}{
		{name: "invalid string", id: `"not-a-uuid"`},
		{name: "empty string", id: `""`},
		{name: "null", id: `null`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := initTicketRoot(t, t.TempDir())
			path := filepath.Join(root, configFileName)
			original := []byte(`{"format_version":1,"id":` + test.id + `}`)
			if err := os.WriteFile(path, original, 0o644); err != nil {
				t.Fatal(err)
			}
			if st, err := Open(root, OpenOptions{Root: root}); err == nil {
				st.Close()
				t.Fatal("malformed persisted ID was accepted")
			} else if !strings.Contains(err.Error(), "canonical UUIDv4") {
				t.Fatalf("malformed ID error=%v", err)
			}
			config, err := os.ReadFile(path)
			if err != nil || string(config) != string(original) {
				t.Fatalf("malformed ID was rewritten: config=%s err=%v", config, err)
			}
		})
	}
}

func TestRepositoryIdentityBackfillFailureReturnsNoTemporaryID(t *testing.T) {
	root := makeLegacyRepository(t)
	originalWriter := writeRepositoryConfigRoot
	writeRepositoryConfigRoot = func(*os.Root, Config) error { return os.ErrPermission }
	defer func() { writeRepositoryConfigRoot = originalWriter }()

	st, err := Open(root, OpenOptions{Root: root})
	if err == nil {
		st.Close()
		t.Fatal("failed settings backfill returned an open store")
	}
	if !strings.Contains(err.Error(), "lack a stable ID and cannot be updated") {
		t.Fatalf("backfill failure is unclear: %v", err)
	}
	cfg, loadErr := LoadConfig(root)
	if loadErr != nil || cfg.ID != "" {
		t.Fatalf("failed migration persisted temporary identity: cfg=%+v err=%v", cfg, loadErr)
	}
}

func TestRepositoryIdentityAcceptsCanonicalUUIDv4(t *testing.T) {
	if !validRepositoryID(testRepositoryID) {
		t.Fatalf("test ID %q is not canonical UUIDv4", testRepositoryID)
	}
	for _, value := range []string{
		"8d1268c4-6a64-1b9b-95c9-d5598a150e86",
		"8d1268c4-6a64-4b9b-15c9-d5598a150e86",
		"8D1268C4-6A64-4B9B-95C9-D5598A150E86",
	} {
		if validRepositoryID(value) {
			t.Errorf("invalid UUID accepted: %s", value)
		}
	}
}
