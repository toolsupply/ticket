package cli

// The CLI tests invoke fake SCM, editor, and Markdown decorator programs.
// Keep those programs in Go so the tests run on Windows as well as Unix;
// executable shell scripts are not directly runnable on Windows.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func init() {
	if mode := os.Getenv("TICKET_TEST_HELPER"); mode != "" {
		os.Exit(runTestHelper(mode))
	}
}

func installTestHelper(t testingT, target, mode string) string {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && filepath.Ext(target) == "" {
		target += ".exe"
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TICKET_TEST_HELPER", mode)
	return target
}

func installFakeSCM(t testingT, dir, mode string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	installTestHelper(t, filepath.Join(dir, "git"), mode)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type testingT interface {
	Helper()
	Fatal(...any)
	Setenv(string, string)
}

func runTestHelper(mode string) int {
	args := os.Args[1:]
	switch mode {
	case "scm-lifecycle":
		appendHelperLog(os.Getenv("SCM_LOG"), currentDirectory()+"|"+strings.Join(args, " "))
		if firstArg(args) == "diff" {
			return 1
		}
	case "scm-push-retry":
		appendHelperLog(os.Getenv("SCM_LOG"), strings.Join(args, " "))
		switch firstArg(args) {
		case "diff":
			if fileExists(os.Getenv("SCM_STATE")) {
				return 0
			}
			return 1
		case "commit":
			if err := touchHelperFile(os.Getenv("SCM_STATE")); err != nil {
				return 2
			}
		case "push":
			if os.Getenv("FAIL_PUSH") == "1" {
				return 1
			}
		}
	case "scm-push-retry-editor-write":
		appendHelperLog(os.Getenv("SCM_LOG"), strings.Join(args, " "))
		switch firstArg(args) {
		case "diff":
			if fileExists(os.Getenv("SCM_STATE")) {
				return 0
			}
			return 1
		case "commit":
			if err := touchHelperFile(os.Getenv("SCM_STATE")); err != nil {
				return 2
			}
		case "push":
			if os.Getenv("FAIL_PUSH") == "1" {
				return 1
			}
		default:
			if err := os.WriteFile(lastArg(args), []byte(os.Getenv("TEST_EDITOR_BODY")), 0o600); err != nil {
				return 2
			}
		}
	case "scm-commit-retry":
		appendHelperLog(os.Getenv("SCM_LOG"), strings.Join(args, " "))
		switch firstArg(args) {
		case "diff":
			if fileExists(os.Getenv("SCM_STATE")) {
				return 0
			}
			return 1
		case "commit":
			if os.Getenv("FAIL_COMMIT") == "1" {
				return 1
			}
			if err := touchHelperFile(os.Getenv("SCM_STATE")); err != nil {
				return 2
			}
		}
	case "scm-reload-config":
		if err := os.WriteFile("config.json", []byte("{\"format_version\":2}\n"), 0o600); err != nil {
			return 2
		}
	case "scm-update-failure":
		fmt.Fprintln(os.Stderr, "remote unavailable")
		return 1
	case "editor-append":
		if err := appendHelperFile(lastArg(args), os.Getenv("TEST_EDITOR_BODY")); err != nil {
			return 2
		}
	case "editor-write":
		if err := os.WriteFile(lastArg(args), []byte(os.Getenv("TEST_EDITOR_BODY")), 0o600); err != nil {
			return 2
		}
	case "editor-retry":
		path := lastArg(args)
		if !fileExists(os.Getenv("EDITOR_COUNT")) {
			if err := touchHelperFile(os.Getenv("EDITOR_COUNT")); err != nil {
				return 2
			}
			if err := os.WriteFile(path, []byte(os.Getenv("TEST_EDITOR_INVALID")), 0o600); err != nil {
				return 2
			}
		} else if err := os.WriteFile(path, []byte(os.Getenv("TEST_EDITOR_VALID")), 0o600); err != nil {
			return 2
		}
	case "editor-block":
		if err := touchHelperFile(os.Getenv("EDITOR_STARTED")); err != nil {
			return 2
		}
		for !fileExists(os.Getenv("EDITOR_RELEASE")) {
			time.Sleep(10 * time.Millisecond)
		}
		if err := appendHelperFile(lastArg(args), os.Getenv("TEST_EDITOR_BODY")); err != nil {
			return 2
		}
	case "editor-fail":
		if err := appendHelperFile(lastArg(args), os.Getenv("TEST_EDITOR_BODY")); err != nil {
			return 2
		}
		return 7
	case "decorator-record":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return 2
		}
		if err := os.WriteFile(os.Getenv("DECORATOR_INPUT"), data, 0o600); err != nil {
			return 2
		}
		if path := os.Getenv("DECORATOR_ARGS"); path != "" {
			if err := os.WriteFile(path, []byte(strings.Join(args, "|")), 0o600); err != nil {
				return 2
			}
		}
		fmt.Fprintln(os.Stdout, "decorated output")
	case "decorator-fail":
		fmt.Fprintln(os.Stderr, "renderer stderr")
		return 7
	default:
		fmt.Fprintln(os.Stderr, "unknown test helper mode:", mode)
		return 2
	}
	return 0
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func lastArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[len(args)-1]
}

func currentDirectory() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func touchHelperFile(path string) error {
	if path == "" {
		return fmt.Errorf("empty helper path")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func appendHelperFile(path, content string) error {
	if path == "" {
		return fmt.Errorf("empty helper path")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func appendHelperLog(path, line string) {
	if err := appendHelperFile(path, line+"\n"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
