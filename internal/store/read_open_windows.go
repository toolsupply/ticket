//go:build windows

package store

import (
	"errors"
	"os"
	"syscall"
	"time"
)

const windowsBoundedReadOpenRetries = 5

// ERROR_SHARING_VIOLATION is Win32 error 32. The Go syscall package does not
// export this constant.
const windowsErrorSharingViolation syscall.Errno = 32

var (
	windowsBoundedPathOpen   = os.Open
	windowsBoundedRootOpen   = openRootNoFollow
	windowsBoundedRetrySleep = time.Sleep
)

func openBoundedReadFile(path string) (*os.File, error) {
	return retryWindowsBoundedReadOpen(func() (*os.File, error) {
		return windowsBoundedPathOpen(path)
	})
}

func openBoundedReadRoot(root *os.Root, name string) (*os.File, error) {
	return retryWindowsBoundedReadOpen(func() (*os.File, error) {
		return windowsBoundedRootOpen(root, name, os.O_RDONLY, 0)
	})
}

func retryWindowsBoundedReadOpen(open func() (*os.File, error)) (*os.File, error) {
	for attempt := 0; ; attempt++ {
		file, err := open()
		if err == nil || !errors.Is(err, windowsErrorSharingViolation) || attempt == windowsBoundedReadOpenRetries {
			return file, err
		}
		windowsBoundedRetrySleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
}
