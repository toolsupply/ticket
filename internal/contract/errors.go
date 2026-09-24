// Package contract defines the stable interface contract of the ticket CLI.
package contract

// ErrorCode is the stable, machine-readable discriminator for failures.
// It is part of the API 2 contract and must not be renamed without an API version
// change.
type ErrorCode string

// Error codes by class, mirroring the exit-code table in the spec.
const (
	// Class: invocation/input error (exit 2).
	ErrInvalidArgument    ErrorCode = "invalid_argument"
	ErrInvalidJSON        ErrorCode = "invalid_json"
	ErrMissingActor       ErrorCode = "missing_actor"
	ErrUnsupportedVersion ErrorCode = "unsupported_version"
	ErrFileTooLarge       ErrorCode = "file_too_large"

	// Class: resolution failure (exit 3).
	ErrRepoNotFound ErrorCode = "repo_not_found"
	ErrNotFound     ErrorCode = "not_found"
	ErrAmbiguousID  ErrorCode = "ambiguous_id"

	// Class: conflict/precondition (exit 4).
	ErrAlreadyClaimed       ErrorCode = "already_claimed"
	ErrConflict             ErrorCode = "conflict"
	ErrInvalidTransition    ErrorCode = "invalid_transition"
	ErrArchived             ErrorCode = "archived"
	ErrDependencyUnresolved ErrorCode = "dependency_unresolved"
	ErrLockTimeout          ErrorCode = "lock_timeout"

	// Class: invalid stored data (exit 5).
	ErrInvalidRepository ErrorCode = "invalid_repository"
	ErrInvalidTicket     ErrorCode = "invalid_ticket"
	ErrDependencyCycle   ErrorCode = "dependency_cycle"
	ErrParentCycle       ErrorCode = "parent_cycle"
	ErrReadinessCycle    ErrorCode = "readiness_cycle"
	ErrDanglingReference ErrorCode = "dangling_reference"

	// Class: storage/runtime failure (exit 6).
	ErrIOError               ErrorCode = "io_error"
	ErrRandomnessUnavailable ErrorCode = "randomness_unavailable"
	// ErrInternalError is emitted by panic recovery at the CLI boundary.
	ErrInternalError ErrorCode = "internal_error"
)

// exitCodes maps every error code to its process exit class.
var exitCodes = map[ErrorCode]int{
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

// ExitCode returns the process exit code for an error code. It panics on
// unknown codes so that adding a code without an exit class is a build or
// test failure, never a silent default.
func ExitCode(code ErrorCode) int {
	ec, ok := exitCodes[code]
	if !ok {
		panic("contract: no exit code registered for error " + string(code))
	}
	return ec
}

// AllErrorCodes lists every registered code in stable sorted order.
func AllErrorCodes() []ErrorCode {
	codes := make([]ErrorCode, 0, len(exitCodes))
	for c := range exitCodes {
		codes = append(codes, c)
	}
	for i := 0; i < len(codes); i++ {
		for j := i + 1; j < len(codes); j++ {
			if codes[j] < codes[i] {
				codes[i], codes[j] = codes[j], codes[i]
			}
		}
	}
	return codes
}

// Error is a structured failure. Code is the stable discriminator; Message
// is brief, actionable English and not a machine-parsing surface; Details
// carries bounded optional context and omits irrelevant fields.
type Error struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// NewError builds an Error. details may be nil.
func NewError(code ErrorCode, message string, details map[string]any) *Error {
	return &Error{Code: code, Message: message, Details: details}
}

// Error implements the error interface.
func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }
