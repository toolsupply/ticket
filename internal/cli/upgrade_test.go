package cli

import (
	"strings"
	"testing"
)

func TestUpgradeHelp(t *testing.T) {
	out, code := runCLIHuman(t, "upgrade", "-h")
	if code != 0 {
		t.Fatalf("upgrade help exit=%d output=%q", code, out)
	}
	for _, want := range []string{
		"upgrade - Update the Linux ticket executable from GitHub Releases.",
		"Usage:\n  ticket upgrade [options]",
		"Examples:\n  $ ticket upgrade",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("upgrade help missing %q in %q", want, out)
		}
	}
}
