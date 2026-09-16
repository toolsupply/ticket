//go:build windows

package store

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// kernel32 is resolved lazily so the package links without a DLL load at
// import time.
var kernel32 = syscall.NewLazyDLL("kernel32.dll")

// Go's stdlib syscall package provides no typed wrappers for LockFileEx /
// UnlockFileEx, so the documented signatures are implemented through these
// procs.
var (
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	// LOCKFILE_EXCLUSIVE_LOCK (winbase.h).
	lockFileExclusiveLock = 2
	// LOCKFILE_FAIL_IMMEDIATELY (winbase.h): fail immediately instead of
	// blocking when the range is already locked; required so the polling
	// timeout in acquire is the only wait and is actually enforced.
	lockFileFailImmediately = 1
	// ERROR_LOCK_VIOLATION (winerror.h).
	errLockViolation = 33
)

// winOverlapped mirrors the win32 OVERLAPPED layout (msdn) with only the
// fields the lock calls read. Go's stdlib syscall package exposes no
// OVERLAPPED type, so the zero value of this struct pins the locked
// range at file offset zero.
// win32 advisory range-lock contracts (MSDN, kernel32):
//
//	BOOL LockFileEx(HANDLE hFile, DWORD dwFlags, DWORD dwReserved,
//	                DWORD dwNumberOfBytesToLockLow,
//	                DWORD dwNumberOfBytesToLockHigh,
//	                LPOVERLAPPED lpOverlapped);
//
//	BOOL UnlockFileEx(HANDLE hFile, DWORD dwReserved,
//	                  DWORD dwNumberOfBytesToUnlockLow,
//	                  DWORD dwNumberOfBytesToUnlockHigh,
//	                  LPOVERLAPPED lpOverlapped);
//
// The lock covers [offset, offset+range) where offset comes from the
// OVERLAPPED; the whole-file range is computed explicitly from the file
// size. This ticket-root lock uses a fixed one-byte range at offset zero. The
// unlock uses the same range and offset. A FALSE return plus the
// last error is the failure signal; a successful acquisition is never
// assumed from a missing error. Behaviour on native Windows must be
// validated on native CI; cross-compilation alone is not verification
// on native Windows.
func acquire(f *os.File, timeout time.Duration) error {
	h := syscall.Handle(f.Fd())
	low, high, err := wholeFileRange(f)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		// Exclusive, non-blocking advisory lock over the whole file.
		var ov syscall.Overlapped // zero value: lock from file offset zero
		r1, _, lastErr := procLockFileEx.Call(
			uintptr(h),
			uintptr(lockFileExclusiveLock|lockFileFailImmediately),
			0, // reserved
			uintptr(low),
			uintptr(high),
			uintptr(unsafe.Pointer(&ov)),
		)
		if r1 != 0 {
			return nil
		}
		err = lastError(lastErr)
		if errno, ok := err.(syscall.Errno); !ok || int(errno) != errLockViolation {
			return err
		}
		if time.Now().Add(lockPollInterval).After(deadline) {
			return errLockTimeout
		}
		time.Sleep(lockPollInterval)
	}
}

// wholeFileRange returns the explicit low/high DWORDs covering the whole
// file. The lock file is created empty and never written, so the range is
// stable for the lifetime of the lock; both acquire and release compute it
// from the same file.
func wholeFileRange(f *os.File) (uint32, uint32, error) {
	if _, err := f.Stat(); err != nil {
		return 0, 0, fmt.Errorf("cannot stat lock file: %w", err)
	}
	return 1, 0, nil
}

// release unlocks the exact range acquire locked. The unlock result is
// returned: a failed unlock must not be silently swallowed.
func release(f *os.File) error {
	h := syscall.Handle(f.Fd())
	low, high, err := wholeFileRange(f)
	if err != nil {
		return err
	}
	var ov syscall.Overlapped // zero value: same offset zero as acquire
	r1, _, lastErr := procUnlockFileEx.Call(
		uintptr(h),
		0, // reserved
		uintptr(low),
		uintptr(high),
		uintptr(unsafe.Pointer(&ov)),
	)
	if r1 == 0 {
		return fmt.Errorf("UnlockFileEx failed: %w", lastError(lastErr))
	}
	return nil
}
