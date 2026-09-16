//go:build linux || darwin

package store

import (
	"os"
	"syscall"
	"time"
)

// unix flock primitive: advisory, tied to the open file description,
// released automatically when the process exits.
func acquire(f *os.File, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !flockWouldBlock(err) {
			return err
		}
		if time.Now().Add(lockPollInterval).After(deadline) {
			return errLockTimeout
		}
		time.Sleep(lockPollInterval)
	}
}

func release(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

func flockWouldBlock(err error) bool {
	return err == syscall.EWOULDBLOCK || err == syscall.EAGAIN || err == syscall.EINTR
}
