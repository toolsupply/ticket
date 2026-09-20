package terminaltitle

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type terminalInfo struct{}

type regularInfo struct{}

func (terminalInfo) Name() string       { return "terminal" }
func (terminalInfo) Size() int64        { return 0 }
func (terminalInfo) Mode() os.FileMode  { return os.ModeCharDevice }
func (terminalInfo) ModTime() time.Time { return time.Time{} }
func (terminalInfo) IsDir() bool        { return false }
func (terminalInfo) Sys() any           { return nil }
func (regularInfo) Name() string        { return "regular" }
func (regularInfo) Size() int64         { return 0 }
func (regularInfo) Mode() os.FileMode   { return 0 }
func (regularInfo) ModTime() time.Time  { return time.Time{} }
func (regularInfo) IsDir() bool         { return false }
func (regularInfo) Sys() any            { return nil }

func TestSanitize(t *testing.T) {
	got := sanitize("ticket\norc\r\t\x1b\x07\x00\x1f\x7f✓")
	if got != "ticket orc  ✓" {
		t.Fatalf("sanitize=%q bytes=%v", got, []byte(got))
	}
	if got := sanitize(strings.Repeat("界", maxTitleRunes+1)); len([]rune(got)) != maxTitleRunes || !strings.HasSuffix(got, "界") {
		t.Fatalf("unicode truncation len=%d value suffix=%q", len([]rune(got)), got[len(got)-3:])
	}
	if got := sanitize("\x00\x1b\x07\x7f"); got != "" {
		t.Fatalf("control-only title=%q", got)
	}
}

func TestSetSupportAndOutput(t *testing.T) {
	stat := func() (os.FileInfo, error) { return terminalInfo{}, nil }
	var output string
	setForOS("linux", "ticket — repo", "xterm", stat, func(value string) error { output = value; return nil })
	if output != "\x1b]2;ticket — repo\x1b\\" {
		t.Fatalf("title output=%q", output)
	}
	output = ""
	setForOS("linux", "ticket", "", stat, func(value string) error { output = value; return nil })
	if output != "" {
		t.Fatalf("unset TERM emitted %q", output)
	}
	output = ""
	setForOS("linux", "ticket", "dumb", stat, func(value string) error { output = value; return nil })
	if output != "" {
		t.Fatalf("dumb TERM emitted %q", output)
	}
	output = ""
	setForOS("linux", "ticket", "xterm", func() (os.FileInfo, error) { return nil, errors.New("stat failed") }, func(value string) error { output = value; return nil })
	if output != "" {
		t.Fatalf("stat failure emitted %q", output)
	}
	output = ""
	setForOS("linux", "ticket", "xterm", func() (os.FileInfo, error) { return regularInfo{}, nil }, func(value string) error { output = value; return nil })
	if output != "" {
		t.Fatalf("regular file emitted %q", output)
	}
	if !supportedForOS("windows", "", stat) || !supportedForOS("windows", "dumb", stat) {
		t.Fatal("Windows support incorrectly depends on TERM")
	}
}

func TestSetIgnoresWriteFailure(t *testing.T) {
	setForOS("linux", "ticket", "xterm", func() (os.FileInfo, error) { return terminalInfo{}, nil }, func(string) error {
		return errors.New("write failed")
	})
}
