// Package terminaltitle provides best-effort terminal title updates for the
// human interactive shell. It never returns an error or affects application
// behavior when title output is unsupported.
package terminaltitle

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

const maxTitleRunes = 200

// Set writes an OSC 2 title only when stdout appears to be an interactive
// terminal. Title output failures are intentionally ignored.
func Set(title string) {
	set(title, os.Getenv("TERM"), os.Stdout.Stat, func(value string) error {
		_, err := io.WriteString(os.Stdout, value)
		return err
	})
}

func set(title, term string, stat func() (os.FileInfo, error), write func(string) error) {
	setForOS(runtime.GOOS, title, term, stat, write)
}

func setForOS(goos, title, term string, stat func() (os.FileInfo, error), write func(string) error) {
	if !supportedForOS(goos, term, stat) {
		return
	}
	title = sanitize(title)
	if title == "" {
		return
	}
	_ = write(fmt.Sprintf("\x1b]2;%s\x1b\\", title))
}

func supported(term string, stat func() (os.FileInfo, error)) bool {
	return supportedForOS(runtime.GOOS, term, stat)
}

func supportedForOS(goos, term string, stat func() (os.FileInfo, error)) bool {
	if goos != "windows" && (term == "" || term == "dumb") {
		return false
	}
	info, err := stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func sanitize(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// Drop C0 controls, ESC, BEL, and DEL.
		default:
			b.WriteRune(r)
		}
	}
	runes := []rune(b.String())
	if len(runes) > maxTitleRunes {
		runes = runes[:maxTitleRunes]
	}
	return string(runes)
}
