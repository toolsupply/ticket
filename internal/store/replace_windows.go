//go:build windows

package store

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var rootRename = func(root *os.Root, oldname, newname string) error {
	return root.Rename(oldname, newname)
}

func publishReplaceRoot(root *os.Root, target, tmp string) error {
	// Root.Rename is the descriptor-relative replacement primitive. The
	// managed contract requires complete replacement and preservation of the
	// canonical target when publication fails; native tests exercise both
	// properties through Store.ReplaceTask.
	return rootRename(root, tmp, target)
}

func recoverFailedReplacement(target, tmp string) error {
	if _, err := os.Lstat(target); err == nil {
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmp, target)
}

// procReplaceFileW resolves ReplaceFileW (kernel32). The exported name is
// "ReplaceFileW"; kernel32 exports no suffix-less "ReplaceFile", so
// resolving the bare name fails at call time on native Windows.
//
// Go's stdlib syscall package provides no typed wrapper for ReplaceFileW,
// so the documented signature is implemented through this proc.
var procReplaceFileW = kernel32.NewProc("ReplaceFileW")

// win32 replacement contract (MSDN, kernel32):
//
//	BOOL ReplaceFileW(LPCWSTR lpReplacedFileName,
//	                   LPCWSTR lpReplacementFileName,
//	                   LPCWSTR lpBackupFileName,
//	                   DWORD  dwReplaceFileFlags,
//	                   LPVOID lpExclude,
//	                   LPVOID lpReserved);
//
// lpReplacedFileName is the target being replaced; lpReplacementFileName is
// the fully qualified path of the file that becomes the target.
// lpBackupFileName NULL discards the replaced bytes. dwReplaceFileFlags must
// be zero. No file handle is involved. The call returns a BOOL: zero means
// failure, and only then is the last error meaningful. Native behavior is
// validated separately from cross-compilation.
func publishReplace(target, tmp string) error {
	original, err := syscall.UTF16FromString(target)
	if err != nil {
		return fmt.Errorf("cannot encode target path: %w", err)
	}
	replacement, err := syscall.UTF16FromString(tmp)
	if err != nil {
		return fmt.Errorf("cannot encode replacement path: %w", err)
	}
	r1, _, lastErr := procReplaceFileW.Call(
		uintptr(unsafe.Pointer(&original[0])),    // lpReplacedFileName
		uintptr(unsafe.Pointer(&replacement[0])), // lpReplacementFileName
		0,                                        // lpBackupFileName: no backup
		0,                                        // dwReplaceFileFlags
		0,                                        // lpExclude
		0,                                        // lpReserved
	)
	if r1 == 0 {
		return fmt.Errorf("ReplaceFileW failed: %w", lastError(lastErr))
	}
	return nil
}

// ERROR_UNABLE_TO_MOVE_REPLACEMENT (1176) can leave the replacement under
// its temporary name while the old target has already disappeared.
// lastError reports the win32 last-error, consulting it only after a
// documented failure (the BOOL check already passed).
func lastError(err error) error {
	if err == nil {
		return syscall.GetLastError()
	}
	return err
}
