package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

var changeSequence atomic.Uint64

// SignalChange writes the advisory local wake-up marker. The marker carries
// no repository state and is excluded from SCM by .local/. A sequence suffix
// makes successive signals distinguishable even on coarse filesystems.
func (st *Store) SignalChange() error {
	value := fmt.Sprintf("%d-%d\n", time.Now().UnixNano(), changeSequence.Add(1))
	return os.WriteFile(filepath.Join(st.Root, ".local", "change"), []byte(value), 0o600)
}

// ChangeState returns the marker contents, treating a missing marker as the
// initial state.
func ChangeState(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, ".local", "change"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
