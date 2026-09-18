package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("module root not found")
		}
		dir = parent
	}
}

func buildCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ticket")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/ticket")
	cmd.Dir = moduleRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	return bin
}

func runCLI(t *testing.T, bin string, args ...string) (stdout string, exit int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return out.String(), exit
}

func TestVersionCommand(t *testing.T) {
	bin := buildCLI(t)
	stdout, exit := runCLI(t, bin, "version", "-j")
	if exit != 0 {
		t.Fatalf("exit = %d, stdout %q", exit, stdout)
	}
	if !strings.HasSuffix(stdout, "\n") || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("stdout is not exactly one LF-terminated line: %q", stdout)
	}
	var v struct {
		Version        string `json:"version"`
		APIVersion     int    `json:"api_version"`
		StorageVersion int    `json:"storage_version"`
	}
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if v.APIVersion != 1 || v.StorageVersion != 1 {
		t.Fatalf("version = %+v, want api_version 1 storage_version 1", v)
	}
	if v.Version == "" {
		t.Fatal("empty version string")
	}
	// No human prose on stdout in JSON mode: must parse as one object.
	var junk any
	if err := json.Unmarshal([]byte(stdout[:strings.LastIndexByte(stdout, '\n')+1]), &junk); err != nil {
		t.Fatalf("stdout is not one JSON object: %v", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	bin := buildCLI(t)
	stdout, exit := runCLI(t, bin, "definitely-not-a-command", "-j")
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("parse error envelope: %v (%q)", err, stdout)
	}
	if env.Error.Code != "invalid_argument" {
		t.Fatalf("code = %q, want invalid_argument", env.Error.Code)
	}
}

func TestNoArgumentsShowsHelp(t *testing.T) {
	bin := buildCLI(t)
	cmd := exec.Command(bin)
	cmd.Dir = t.TempDir()
	cmd.Env = os.Environ()
	var output bytes.Buffer
	cmd.Stdout = &output
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run ticket: %v", err)
	}
	stdout := output.String()
	if exit != 0 {
		t.Fatalf("exit = %d, stdout %q", exit, stdout)
	}
	if !strings.HasPrefix(stdout, "\n") || !strings.HasSuffix(stdout, "\n\n") {
		t.Fatalf("help should have surrounding blank lines: %q", stdout)
	}
	if !strings.Contains(stdout, "A repository-local task manager from https://github.com/toolsupply/ticket") ||
		!strings.Contains(stdout, "https://github.com/toolsupply/ticket") ||
		!strings.Contains(stdout, "ticket <command> [options]") ||
		!strings.Contains(stdout, "Run 'ticket help <command>' or 'ticket <command> -h'") {
		t.Fatalf("missing command index: %q", stdout)
	}
}
