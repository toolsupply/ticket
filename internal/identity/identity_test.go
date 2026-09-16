package identity_test

import (
	"testing"
	"time"

	"ticket/internal/identity"
)

func TestRandomSourceUsesTimestampIDFormat(t *testing.T) {
	src := identity.RandomSource{}
	first, err := src.ID("")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != identity.TimestampIDLen || !identity.ValidID(first) {
		t.Fatalf("timestamp ID=%q len=%d valid=%v", first, len(first), identity.ValidID(first))
	}
	if _, err := time.Parse("20060102", first[:8]); err != nil {
		t.Fatalf("invalid date: %v", err)
	}
	if first[8] != '-' {
		t.Fatalf("missing separator: %q", first)
	}
	second, err := src.ID("")
	if err != nil || second == first {
		t.Fatalf("second ID=%q err=%v", second, err)
	}
	if _, err := src.ID("bad_"); err == nil {
		t.Fatal("unsupported prefix accepted")
	}
}

func TestTimestampIDValidation(t *testing.T) {
	if !identity.ValidID("20260913-00000") {
		t.Fatal("canonical timestamp ID rejected")
	}
	for _, id := range []string{
		"20260229-00000", "20261301-00000", "20260913-0000x",
		"20260913-000000", "20260913_00000", "2026091300000",
		"tk_20260913-00000", "../20260913-00000",
	} {
		if identity.ValidID(id) {
			t.Fatalf("invalid ID accepted: %q", id)
		}
	}
}

func TestShorthandValidation(t *testing.T) {
	for _, shorthand := range []string{
		"2", "2026", "202609", "20260913", "20260913-", "20260913-0000",
	} {
		if !identity.ValidShorthand(shorthand) {
			t.Fatalf("valid shorthand rejected: %q", shorthand)
		}
	}
	for _, shorthand := range []string{
		"", "x", "2026-", "202613", "2026023", "20260913_", "20260913-00x",
		"20260913-00000",
	} {
		if identity.ValidShorthand(shorthand) {
			t.Fatalf("invalid shorthand accepted: %q", shorthand)
		}
	}
}

func TestPrefixOf(t *testing.T) {
	id := "20260913-00000"
	prefix, body, ok := identity.PrefixOf(id)
	if !ok || prefix != "" || body != id {
		t.Fatalf("PrefixOf: %q %q %v", prefix, body, ok)
	}
	for _, invalid := range []string{"short", "tk_20260913-00000", "zz_20260913-00000"} {
		if _, _, ok := identity.PrefixOf(invalid); ok {
			t.Fatalf("invalid ID split: %q", invalid)
		}
	}
}
