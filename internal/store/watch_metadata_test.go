package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestEnumerateActiveWatchMetadataScopeAndOrdering(t *testing.T) {
	root := t.TempDir()
	idA := "20260929-00001"
	idB := "20260929-00002"
	for _, id := range []string{idB, idA} {
		if err := os.Mkdir(filepath.Join(root, id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	taskPath := filepath.Join(root, idA, "TASK.md")
	if err := os.WriteFile(taskPath, []byte("not parsed as markdown"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory named TASK.md would fail a content read, but remains valid
	// input to this metadata-only enumerator.
	if err := os.Mkdir(filepath.Join(root, idB, "TASK.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, idA, "attachment.bin"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".local"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".local", "change"), []byte("marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ArchiveDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ArchiveDirName, "20260929-00003"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ArchiveDirName, "20260929-00003", "TASK.md"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}

	stamp := time.Date(2026, time.September, 29, 12, 34, 56, 789123000, time.UTC)
	if err := os.Chtimes(taskPath, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	beforeMarker, err := os.ReadFile(filepath.Join(root, ".local", "change"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := EnumerateActiveWatchMetadata(root)
	if err != nil {
		t.Fatalf("enumerate metadata: %v", err)
	}
	wantPaths := []string{idA, idA + "/TASK.md", idB, idB + "/TASK.md"}
	paths := make([]string, len(got))
	for i, entry := range got {
		paths[i] = entry.Path
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("metadata paths=%v, want %v", paths, wantPaths)
	}
	if !got[0].Type.IsDir() || got[1].Type != 0 || got[1].Size != int64(len("not parsed as markdown")) {
		t.Fatalf("directory/task metadata=%+v %+v", got[0], got[1])
	}
	if !got[3].Type.IsDir() {
		t.Fatalf("TASK.md directory type=%v, want directory", got[3].Type)
	}
	info, err := os.Stat(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := info.ModTime().UTC(); !got[1].ModTime.Equal(want) {
		t.Fatalf("task modification time=%s, want filesystem precision %s", got[1].ModTime, want)
	}
	afterMarker, err := os.ReadFile(filepath.Join(root, ".local", "change"))
	if err != nil || !reflect.DeepEqual(beforeMarker, afterMarker) {
		t.Fatalf("enumeration changed .local/change: before=%q after=%q err=%v", beforeMarker, afterMarker, err)
	}
}

func TestEnumerateActiveWatchMetadataPreservesSubsecondChanges(t *testing.T) {
	root := t.TempDir()
	id := "20260929-00001"
	if err := os.Mkdir(filepath.Join(root, id), 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(root, id, "TASK.md")
	contents := []byte("same size")
	if err := os.WriteFile(taskPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	firstStamp := time.Date(2026, time.September, 29, 12, 34, 56, 125_000_000, time.UTC)
	secondStamp := firstStamp.Add(250 * time.Millisecond)
	setModTime := func(stamp time.Time) {
		t.Helper()
		if err := os.Chtimes(taskPath, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	metadataForTask := func() WatchMetadata {
		t.Helper()
		entries, err := EnumerateActiveWatchMetadata(root)
		if err != nil {
			t.Fatalf("enumerate metadata: %v", err)
		}
		for _, entry := range entries {
			if entry.Path == id+"/TASK.md" {
				return entry
			}
		}
		t.Fatal("TASK.md metadata not found")
		return WatchMetadata{}
	}

	setModTime(firstStamp)
	first := metadataForTask()
	setModTime(secondStamp)
	second := metadataForTask()
	if first.ModTime.Equal(second.ModTime) {
		t.Skip("filesystem metadata does not expose sub-second modification times")
	}
	if delta := second.ModTime.Sub(first.ModTime); delta <= 0 || delta >= time.Second {
		t.Fatalf("mtime delta=%s, want a positive sub-second change", delta)
	}
	if first.Path != second.Path || first.Type != second.Type || first.Size != second.Size || first.Size != int64(len(contents)) {
		t.Fatalf("non-time metadata changed: before=%+v after=%+v", first, second)
	}
}

func TestEnumerateActiveWatchMetadataDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	id := "20260929-00001"
	if err := os.Mkdir(filepath.Join(root, id), 0o755); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "TASK.md"), []byte("external contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "20260929-00002")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linkPath := filepath.Join(root, id, "TASK.md")
	if err := os.Symlink(filepath.Join(external, "TASK.md"), linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linkInfo, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatal(err)
	}

	got, err := EnumerateActiveWatchMetadata(root)
	if err != nil {
		t.Fatalf("enumerate metadata: %v", err)
	}
	rootLinkInfo, err := os.Lstat(filepath.Join(root, "20260929-00002"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[1].Path != id+"/TASK.md" || got[1].Type&os.ModeSymlink == 0 || got[1].Size != linkInfo.Size() ||
		got[2].Path != "20260929-00002" || got[2].Type&os.ModeSymlink == 0 || got[2].Size != rootLinkInfo.Size() {
		t.Fatalf("metadata=%+v; want active dir and in-scope symlink metadata without external ticket", got)
	}
}

func TestEnumerateActiveWatchMetadataIncludesMalformedDirectoriesAndRootSymlinks(t *testing.T) {
	root := t.TempDir()
	canonicalID := "20260929-00001"
	malformedID := "20260929-0000x"
	canonicalLinkID := "20260929-00002"
	for _, name := range []string{canonicalID, malformedID} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "TASK.md"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := []string{canonicalLinkID, "root-link"}
	for _, name := range links {
		if err := os.Symlink(external, filepath.Join(root, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	if err := os.Symlink(external, filepath.Join(root, ".local")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(external, filepath.Join(root, ArchiveDirName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkInfo, err := os.Lstat(filepath.Join(root, "root-link"))
	if err != nil {
		t.Fatal(err)
	}
	canonicalLinkInfo, err := os.Lstat(filepath.Join(root, canonicalLinkID))
	if err != nil {
		t.Fatal(err)
	}

	got, err := EnumerateActiveWatchMetadata(root)
	if err != nil {
		t.Fatalf("enumerate metadata: %v", err)
	}
	wantPaths := []string{canonicalID, canonicalLinkID, malformedID, "root-link"}
	paths := make([]string, len(got))
	for i, entry := range got {
		paths[i] = entry.Path
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("metadata paths=%v, want %v", paths, wantPaths)
	}
	if got[1].Type&os.ModeSymlink == 0 || got[1].Size != canonicalLinkInfo.Size() {
		t.Fatalf("canonical root symlink metadata=%+v, want Lstat metadata %+v", got[1], canonicalLinkInfo)
	}
	if got[3].Type&os.ModeSymlink == 0 || got[3].Size != linkInfo.Size() {
		t.Fatalf("root symlink metadata=%+v, want Lstat metadata %+v", got[3], linkInfo)
	}
}

func TestEnumerateActiveWatchMetadataMissingTaskAndNoRepositoryMutation(t *testing.T) {
	root := t.TempDir()
	id := "20260929-00001"
	if err := os.Mkdir(filepath.Join(root, id), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := EnumerateActiveWatchMetadata(root)
	if err != nil {
		t.Fatalf("enumerate metadata: %v", err)
	}
	if len(got) != 1 || got[0].Path != id {
		t.Fatalf("metadata=%+v, want ticket directory only", got)
	}
	if _, err := os.Lstat(filepath.Join(root, ".local")); !os.IsNotExist(err) {
		t.Fatalf("enumeration created .local: %v", err)
	}
}
