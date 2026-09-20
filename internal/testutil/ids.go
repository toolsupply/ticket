// Package testutil provides deterministic, non-production fixtures for
// tests: deterministic/failing ID sources and a fixture
// repository builder. Production identity primitives live in
// internal/identity; this package only contributes reproducible test
// data and must never be imported by non-test code.
package testutil

import (
	"fmt"

	"github.com/toolsupply/ticket/internal/identity"
)

// DeterministicSource issues reproducible IDs from a fixed seed. It is for
// fixtures only: it is not cryptographically random and must never be the
// production source.
type DeterministicSource struct {
	seed uint64
	n    uint64
}

// NewDeterministicSource creates a deterministic source for a seed.
func NewDeterministicSource(seed uint64) *DeterministicSource {
	return &DeterministicSource{seed: seed}
}

// ID implements identity.IDSource.
func (d *DeterministicSource) ID(prefix string) (string, error) {
	if prefix != "" {
		return "", fmt.Errorf("unsupported ID prefix %q", prefix)
	}
	v := d.mix(d.seed)%90000 + d.n
	d.n++
	return fmt.Sprintf("20000101-%05d", v), nil
}

func (d *DeterministicSource) mix(v uint64) uint64 {
	v ^= v >> 33
	v *= 0xff51_afd7_ed55_8ccd
	v ^= v >> 29
	v *= 0xc4ce_b991_fd03_75a3
	v ^= v >> 32
	return v
}

// FailingSource models a broken randomness backend (spec S05).
type FailingSource struct{}

// ID implements identity.IDSource and always fails.
func (FailingSource) ID(prefix string) (string, error) {
	return "", identity.ErrRandomnessUnavailable
}
