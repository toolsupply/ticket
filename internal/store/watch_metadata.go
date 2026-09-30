package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/toolsupply/ticket/internal/identity"
)

// WatchMetadata describes one filesystem entry that can affect the active
// ticket snapshot. Modification times are normalized to UTC while preserving
// the precision reported by the filesystem.
type WatchMetadata struct {
	Path    string
	Type    fs.FileMode
	Size    int64
	ModTime time.Time
}

// EnumerateActiveWatchMetadata inspects active ticket directory structure and
// managed TASK.md entries without reading their contents or following
// symlinks. It does not open a Store, acquire a lock, or touch .local state.
func EnumerateActiveWatchMetadata(rootPath string) ([]WatchMetadata, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, err
	}
	var result []WatchMetadata
	for _, entry := range entries {
		name := entry.Name()
		if name == ArchiveDirName {
			continue
		}
		info, err := root.Lstat(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		switch identity.ClassifyActiveRootEntry(name, info.Mode().Type(), info.IsDir()) {
		case identity.IgnoreActiveRootEntry:
			continue
		case identity.ActiveRootSymlink, identity.ActiveRootMalformedTicketDirectory:
			result = append(result, watchMetadata(name, info))
			continue
		case identity.ActiveRootTicketDirectory:
			// Continue below to inspect this canonical ticket directory.
		default:
			continue
		}

		ticketRoot, err := root.OpenRoot(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		openedDirInfo, statErr := ticketRoot.Stat(".")
		if statErr != nil {
			ticketRoot.Close()
			return nil, statErr
		}
		if !os.SameFile(info, openedDirInfo) || !openedDirInfo.IsDir() {
			ticketRoot.Close()
			continue
		}
		result = append(result, watchMetadata(name, info))

		taskInfo, taskErr := ticketRoot.Lstat("TASK.md")
		closeErr := ticketRoot.Close()
		if taskErr != nil && !os.IsNotExist(taskErr) {
			return nil, taskErr
		}
		if taskErr == nil {
			result = append(result, watchMetadata(filepath.ToSlash(filepath.Join(name, "TASK.md")), taskInfo))
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func watchMetadata(path string, info os.FileInfo) WatchMetadata {
	return WatchMetadata{
		Path:    path,
		Type:    info.Mode().Type(),
		Size:    info.Size(),
		ModTime: info.ModTime().UTC(),
	}
}
