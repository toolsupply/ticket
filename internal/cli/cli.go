// Package cli implements process invocation: argument decoding, the JSON
// output envelope, and exit-code mapping. Domain and storage layers live in
// separate packages; the CLI only maps their typed results to the v1
// interface contract.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"

	"ticket/internal/contract"
	"ticket/internal/domain"
	"ticket/internal/store"
)

// Public protocol versions.
const (
	APIVersion     = contract.APIVersion
	StorageVersion = contract.StorageVersion
)

// Version is the build version reported by the version command. Release and
// Makefile builds replace the development value from the root VERSION file.
var Version = "dev"

// Commit is the optional build commit, settable at link time via
// -ldflags "-X ticket/internal/cli.Commit=<sha>".
var Commit = ""

// Run executes one CLI invocation and returns the process exit code. Human
// output is the default; -j/--json selects the compact JSON contract.
func Run(args []string, out io.Writer) int {
	jsonOutput := jsonRequested(args)
	var stdout bytes.Buffer
	err := dispatch(args, &stdout)
	if err == nil {
		if !jsonOutput && shouldPage(args, stdout.Len()) {
			if pageWithLess(stdout.Bytes()) {
				return 0
			}
		}
		out.Write(stdout.Bytes())
		return 0
	}
	var ce *contract.Error
	if !errors.As(err, &ce) {
		// A non-contract failure is a bug; report it bounded. Never
		// emit a false success.
		ce = contract.NewError(contract.ErrInternalError, "Internal error.", nil)
	}
	if !jsonOutput {
		fmt.Fprintf(os.Stderr, "error: %s\n", humanErrorMessage(ce))
		return contract.ExitCode(ce.Code)
	}
	_ = emitJSON(out, struct {
		Error *contract.Error `json:"error"`
	}{Error: ce})
	return contract.ExitCode(ce.Code)
}

func humanErrorMessage(ce *contract.Error) string {
	if ce == nil || len(ce.Details) == 0 {
		if ce == nil {
			return "Internal error."
		}
		return ce.Message
	}
	raw, ok := ce.Details["diagnostics"]
	if !ok {
		return ce.Message
	}
	var diagnostics []map[string]any
	switch values := raw.(type) {
	case []map[string]any:
		diagnostics = values
	case []any:
		for _, value := range values {
			if diagnostic, ok := value.(map[string]any); ok {
				diagnostics = append(diagnostics, diagnostic)
			}
		}
	}
	for _, diagnostic := range diagnostics {
		if severity, _ := diagnostic["severity"].(string); severity != "error" {
			continue
		}
		detail, _ := diagnostic["message"].(string)
		if detail == "" {
			continue
		}
		if line, ok := diagnostic["line"].(int); ok && line > 0 {
			return fmt.Sprintf("%s (line %d: %s)", ce.Message, line, detail)
		}
		return ce.Message + " (" + detail + ")"
	}
	return ce.Message
}

func shouldPage(args []string, size int) bool {
	if size < 4000 || len(args) == 0 || args[0] != "show" {
		return false
	}
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func pageWithLess(data []byte) bool {
	less, err := exec.LookPath("less")
	if err != nil {
		return false
	}
	cmd := exec.Command(less, "-R")
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run() == nil
}

// dispatch runs the command and writes its success output to stdout.
// Contract errors are returned typed; anything else becomes
// internal_error. Panics become bounded internal_error, never a false
// success.
func dispatch(args []string, stdout *bytes.Buffer) (err error) {
	defer func() {
		if r := recover(); r != nil {
			debugFlag := len(args) > 0 && scanFlag(args[1:], "--debug")
			if debugFlag {
				os.Stderr.Write(debug.Stack())
			}
			err = contract.NewError(contract.ErrInternalError,
				fmt.Sprintf("Internal error: %v", r), nil)
		}
	}()
	// Per-invocation state: never leak across dispatches.
	helpFlag = false
	helpSeen = false
	ctx := &commandContext{stdout: stdout, cwd: cwd(), globalOpts: globalOpts{json: jsonRequested(args)}}
	if len(args) == 0 {
		return emitTopLevelHelp(ctx)
	}
	name := args[0]
	rest := args[1:]
	switch name {
	case "-h", "--help":
		if !onlyJSONFlags(rest) {
			return contract.NewError(contract.ErrInvalidArgument,
				name+" accepts no arguments.", nil)
		}
		return emitTopLevelHelp(ctx)
	case "-v", "--version":
		if !onlyJSONFlags(rest) {
			return contract.NewError(contract.ErrInvalidArgument,
				name+" accepts no arguments.", nil)
		}
		return emitTopLevelVersion(ctx)
	case "-j", "--json":
		if len(rest) == 0 {
			return emitTopLevelHelp(ctx)
		}
		if rest[0] == "-j" || rest[0] == "--json" {
			return contract.NewError(contract.ErrInvalidArgument, "Duplicate flag --json.", nil)
		}
		normalized := append([]string{rest[0]}, rest[1:]...)
		normalized = append(normalized, "--json")
		return dispatch(normalized, stdout)
	case "-la", "-al":
		return cmdList(ctx, append([]string{"-l", "-a"}, rest...), true)
	case "version":
		return cmdVersion(ctx, rest)
	case "actor":
		return cmdActor(ctx, rest)
	case "init":
		return cmdInit(ctx, rest)
	case "create":
		return cmdCreate(ctx, rest)
	case "delete":
		return cmdDelete(ctx, rest)
	case "bump":
		return cmdBump(ctx, rest)
	case "new":
		return cmdNew(ctx, rest)
	case "list":
		return cmdList(ctx, rest, false)
	case "ls":
		return cmdList(ctx, rest, true)
	case "grep":
		return cmdGrep(ctx, rest)
	case "ready":
		return cmdReady(ctx, rest)
	case "next":
		return cmdNext(ctx, rest)
	case "wait":
		return cmdWait(ctx, rest)
	case "show":
		return cmdShow(ctx, rest)
	case "edit":
		return cmdEdit(ctx, rest)
	case "submit":
		return cmdSubmit(ctx, rest)
	case "hold":
		return cmdHold(ctx, rest)
	case "open":
		return cmdOpen(ctx, rest)
	case "status":
		return cmdStatus(ctx, rest)
	case "path":
		return cmdPath(ctx, rest)
	case "update":
		return cmdUpdate(ctx, rest)
	case "claim":
		return cmdClaim(ctx, rest)
	case "release":
		return cmdRelease(ctx, rest)
	case "close":
		return cmdClose(ctx, rest, "close", func(st *store.Store, id string, in transitionInput, actor string) (any, error) {
			return domain.Close(st, id, domain.CloseOptions{Outcome: valueOrEmpty(in.Outcome), Actor: actor, Message: in.Message})
		})
	case "approve":
		return cmdApprove(ctx, rest)
	case "accept":
		return cmdApprove(ctx, rest)
	case "reject":
		return cmdReject(ctx, rest)
	case "check":
		return cmdCheck(ctx, rest)
	case "help":
		return cmdHelp(ctx, rest)
	default:
		return contract.NewError(contract.ErrInvalidArgument,
			"Unknown command: "+name+".", nil)
	}
}

func cwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

type versionOutput struct {
	Version        string `json:"version"`
	APIVersion     int    `json:"api_version"`
	StorageVersion int    `json:"storage_version"`
	Commit         string `json:"commit,omitempty"`
}

func cmdVersion(ctx *commandContext, rest []string) error {
	p := &parser{}
	ctx.registerWithoutActor(p)
	p.help = &helpFlag
	p.helpSeen = &helpSeen
	if err := p.parse(rest); err != nil {
		return err
	}
	if helpFlag {
		return emitHelpCommand(ctx, "version")
	}
	if err := p.requireNoPositionals("version"); err != nil {
		return err
	}
	if !ctx.json {
		fmt.Fprintf(ctx.stdout, "ticket %s (api %d, storage %d)\n", Version, APIVersion, StorageVersion)
		return nil
	}
	return emitJSON(ctx.stdout, versionOutput{
		Version:        Version,
		APIVersion:     APIVersion,
		StorageVersion: StorageVersion,
		Commit:         Commit,
	})
}

// scanFlag reports whether name appears as a flag (before any --
// terminator).
func scanFlag(rest []string, name string) bool {
	for _, a := range rest {
		if a == "--" {
			break
		}
		if a == name {
			return true
		}
	}
	return false
}

func jsonRequested(args []string) bool {
	return scanFlag(args, "-j") || scanFlag(args, "--json")
}

func onlyJSONFlags(args []string) bool {
	for _, arg := range args {
		if arg != "-j" && arg != "--json" {
			return false
		}
	}
	return true
}

// emitJSON writes one compact JSON document plus LF. HTML escaping is off:
// ticket content is literal data, not markup.
func emitJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
