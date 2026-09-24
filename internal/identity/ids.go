// Package identity holds the ticket identifier primitives shared by the
// production code. Ticket IDs are date-stamped and safe as directory
// names: YYYYMMDD-NNNNN.
package identity

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// TimestampIDLen is the ASCII length of a full ticket ID.
const TimestampIDLen = 8 + 1 + 5

// IDSource produces a full ID for the supplied entity prefix. The ticket
// source uses an empty prefix; the parameter remains for the store interface.
type IDSource interface {
	ID(prefix string) (string, error)
}

// ErrRandomnessUnavailable is retained for the IDSource error contract used
// by test and embedding implementations.
var ErrRandomnessUnavailable = errors.New("randomness unavailable")

// RandomSource creates date-stamped IDs from the current UTC time.
type RandomSource struct{}

var timestampSequence uint64

func (RandomSource) ID(prefix string) (string, error) {
	if prefix != "" {
		return "", fmt.Errorf("unsupported ID prefix %q", prefix)
	}
	now := time.Now().UTC()
	// Keep repeated calls in one process distinct within the same millisecond.
	// The ticket-root lock serializes cooperating processes and publication
	// still performs the authoritative collision check.
	seq := atomic.AddUint64(&timestampSequence, 1) - 1
	suffix := (now.UnixMilli() + int64(seq)) % 100000
	return fmt.Sprintf("%s-%05d", now.Format("20060102"), suffix), nil
}

// ValidID reports whether id uses the canonical date-stamped format.
func ValidID(id string) bool {
	_, body, ok := PrefixOf(id)
	if !ok || len(body) != TimestampIDLen || body[8] != '-' {
		return false
	}
	if _, err := time.Parse("20060102", body[:8]); err != nil {
		return false
	}
	for i, c := range body {
		if i == 8 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ValidShorthand reports whether id is a nonempty prefix of a canonical
// YYYYMMDD-NNNNN identifier. A full identifier is handled by ValidID.
func ValidShorthand(id string) bool {
	if len(id) == 0 || len(id) >= TimestampIDLen {
		return false
	}

	// A shorthand through the date portion must still be extendable to a
	// real calendar date. This rejects prefixes such as 202613 while keeping
	// short, useful prefixes such as 2 or 2026 valid.
	dateLen := len(id)
	if dateLen > 8 {
		dateLen = 8
	}
	for i := 0; i < dateLen; i++ {
		if id[i] < '0' || id[i] > '9' {
			return false
		}
	}
	switch {
	case len(id) <= 4:
		return true
	case len(id) == 5:
		return id[4] == '0' || id[4] == '1'
	case len(id) == 6:
		month := int(id[4]-'0')*10 + int(id[5]-'0')
		return month >= 1 && month <= 12
	case len(id) == 7:
		month := int(id[4]-'0')*10 + int(id[5]-'0')
		if month < 1 || month > 12 {
			return false
		}
		dayTens := id[6] - '0'
		if dayTens <= 2 {
			return true
		}
		return dayTens == 3 && (month == 1 || month == 3 || month == 5 ||
			month == 7 || month == 8 || month == 10 || month == 12)
	case len(id) == 8:
		_, err := time.Parse("20060102", id)
		return err == nil
	default:
		if id[8] != '-' {
			return false
		}
		if !ValidID(id[:8] + "-00000") {
			return false
		}
		for i := 9; i < len(id); i++ {
			if id[i] < '0' || id[i] > '9' {
				return false
			}
		}
		return true
	}
}

// PrefixOf splits a canonical ID into its empty prefix and date-stamped body.
func PrefixOf(id string) (prefix, body string, ok bool) {
	if len(id) != TimestampIDLen {
		return "", "", false
	}
	return "", id, true
}
