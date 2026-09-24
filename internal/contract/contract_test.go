package contract

import "testing"

// exitCodeClasses is the exit-code table used by the command interface.
var exitCodeClasses = map[ErrorCode]int{
	ErrInvalidArgument:    2,
	ErrInvalidJSON:        2,
	ErrMissingActor:       2,
	ErrUnsupportedVersion: 2,
	ErrFileTooLarge:       2,

	ErrRepoNotFound: 3,
	ErrNotFound:     3,
	ErrAmbiguousID:  3,

	ErrAlreadyClaimed:       4,
	ErrConflict:             4,
	ErrInvalidTransition:    4,
	ErrArchived:             4,
	ErrDependencyUnresolved: 4,
	ErrLockTimeout:          4,

	ErrInvalidRepository: 5,
	ErrInvalidTicket:     5,
	ErrDependencyCycle:   5,
	ErrParentCycle:       5,
	ErrReadinessCycle:    5,
	ErrDanglingReference: 5,

	ErrIOError:               6,
	ErrRandomnessUnavailable: 6,
	ErrInternalError:         6,
}

func TestExitCodeClasses(t *testing.T) {
	for code, want := range exitCodeClasses {
		if got := ExitCode(code); got != want {
			t.Errorf("ExitCode(%s) = %d, want %d", code, got, want)
		}
	}
}

func TestAllErrorCodesCoverRegistry(t *testing.T) {
	all := AllErrorCodes()
	if len(all) == 0 {
		t.Fatal("expected registered error codes")
	}
	for _, c := range all {
		// Panics if unregistered; also validates class ranges.
		if got := ExitCode(c); got < 2 || got > 6 {
			t.Errorf("ExitCode(%s) = %d outside error classes 2..6", c, got)
		}
	}
}
