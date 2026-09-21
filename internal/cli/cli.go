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
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
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
// -ldflags "-X github.com/toolsupply/ticket/internal/cli.Commit=<sha>".
var Commit = ""

// Run executes one CLI invocation and returns the process exit code. Human
// output is the default; -j/--json selects the compact JSON contract.
func Run(args []string, out io.Writer) int {
	if interactiveRequested(args) {
		return runInteractive(args, out)
	}
	jsonOutput := jsonRequested(args)
	var stdout bytes.Buffer
	var err error
	if watchInvocation(args) {
		err = dispatchLive(args, &stdout, out)
	} else {
		err = dispatch(args, &stdout)
	}
	if err == nil {
		if stdout.Len() > 0 {
			if !jsonOutput && shouldPage(args, stdout.Len()) {
				if pageWithLess(stdout.Bytes()) {
					return 0
				}
			}
			out.Write(stdout.Bytes())
		}
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

func dispatchLive(args []string, stdout *bytes.Buffer, out io.Writer) error {
	g := globalOpts{json: jsonRequested(args), scopeName: strings.TrimSpace(os.Getenv("TICKET_SCOPE")), liveWriter: out}
	return dispatchWithGlobals(args, stdout, g, nil, os.Stdin, false)
}

func watchInvocation(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-j" || args[i] == "--json" || args[i] == "--debug":
			continue
		case args[i] == "-c" || args[i] == "--config" || args[i] == "--scope":
			i++
		case strings.HasPrefix(args[i], "--config=") || strings.HasPrefix(args[i], "--scope="):
			continue
		default:
			return canonicalCommand(args[i]) == "watch"
		}
	}
	return false
}

func humanErrorMessage(ce *contract.Error) string {
	if ce == nil || len(ce.Details) == 0 {
		if ce == nil {
			return "Internal error."
		}
		return safeSingleLine(ce.Message)
	}
	raw, ok := ce.Details["diagnostics"]
	if !ok {
		return safeSingleLine(ce.Message)
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
			return fmt.Sprintf("%s (line %d: %s)", safeSingleLine(ce.Message), line, safeSingleLine(detail))
		}
		return safeSingleLine(ce.Message) + " (" + safeSingleLine(detail) + ")"
	}
	return safeSingleLine(ce.Message)
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
	return dispatchWithInput(args, stdout, os.Stdin)
}

// dispatchWithInput executes one command with an invocation-local input
// stream. A nil input means that the invocation has no stdin payload; a
// non-nil reader is the only source available to commands that consume input.
func dispatchWithInput(args []string, stdout *bytes.Buffer, input io.Reader) (err error) {
	return dispatchWithSessionInput(args, stdout, nil, input, false)
}

func dispatchWithSession(args []string, stdout *bytes.Buffer, executor *sessionExecutor) (err error) {
	return dispatchWithSessionInput(args, stdout, executor, nil, false)
}

func dispatchWithSessionInput(args []string, stdout *bytes.Buffer, executor *sessionExecutor, input io.Reader, inputProvided bool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			debugFlag := scanFlag(args, "--debug")
			if debugFlag {
				os.Stderr.Write(debug.Stack())
			}
			err = contract.NewError(contract.ErrInternalError,
				fmt.Sprintf("Internal error: %v", r), nil)
		}
	}()
	if executor != nil {
		return dispatchWithGlobals(args, stdout, executor.globalOpts, executor, input, inputProvided)
	}
	g := globalOpts{json: jsonRequested(args), scopeName: strings.TrimSpace(os.Getenv("TICKET_SCOPE"))}
	return dispatchWithGlobals(args, stdout, g, nil, input, inputProvided)
}

func dispatchWithGlobals(args []string, stdout *bytes.Buffer, g globalOpts, executor *sessionExecutor, input io.Reader, inputProvided bool) error {
	var session *sessionState
	if executor != nil {
		session = &executor.state
		g = executor.globalOpts
		if g.machineTransport {
			g.json = true
		} else {
			g.json = jsonRequested(args)
		}
		g.debug = false
		g.configExplicit = false
		g.scopeExplicit = false
		g.sessionBound = true
	}
	var err error
	args, g, err = consumeLeadingGlobals(args, g)
	if err != nil {
		return err
	}
	args, err = normalizeObjectFirst(args)
	if err != nil {
		return err
	}
	// Per-invocation state: never leak across dispatches.
	helpFlag = false
	helpSeen = false
	commandCWD := cwd()
	if executor != nil {
		commandCWD = executor.cwd
	}
	var done <-chan struct{}
	if executor != nil {
		done = executor.done
	}
	ctx := &commandContext{stdout: stdout, cwd: commandCWD, globalOpts: g, session: session, executor: executor, done: done, input: input, inputProvided: inputProvided, liveWriter: g.liveWriter}
	if inputProvided && (len(args) == 0 || !commandAcceptsInvocationInput(args[0])) {
		return unexpectedInvocationInput("")
	}
	if len(args) == 0 {
		if !ctx.json {
			return emitCurrentSummaryOrHelp(ctx)
		}
		return emitTopLevelHelp(ctx)
	}
	rawName := args[0]
	name := canonicalCommand(rawName)
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
			ctx.json = true
			return emitTopLevelHelp(ctx)
		}
		if rest[0] == "-j" || rest[0] == "--json" {
			return contract.NewError(contract.ErrInvalidArgument, "Duplicate flag --json.", nil)
		}
		g.json = true
		return dispatchWithGlobals(rest, stdout, g, executor, input, inputProvided)
	case "-la", "-al":
		return cmdList(ctx, append([]string{"-l", "-a"}, rest...), true)
	case "version":
		return cmdVersion(ctx, rest)
	case "actor":
		return cmdActor(ctx, rest)
	case "info":
		return cmdInfo(ctx, rest)
	case "init":
		return cmdInit(ctx, rest)
	case "create":
		if rawName == "new" || rawName == "add" {
			return cmdNew(ctx, rest)
		}
		return cmdCreate(ctx, rest)
	case "delete":
		return cmdDelete(ctx, rest)
	case "bump":
		return cmdBump(ctx, rest)
	case "list":
		return cmdList(ctx, rest, rawName == "ls")
	case "grep":
		return cmdGrep(ctx, rest)
	case "ready":
		return cmdReady(ctx, rest)
	case "next":
		return cmdNext(ctx, rest)
	case "wait":
		return cmdWait(ctx, rest)
	case "watch":
		return cmdWatch(ctx, rest)
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
	case "review":
		return cmdReview(ctx, rest)
	case "state":
		return cmdState(ctx, rest)
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
	case "reject":
		return cmdReject(ctx, rest)
	case "check":
		return cmdCheck(ctx, rest)
	case "help":
		return cmdHelp(ctx, rest)
	default:
		if looksLikeTicketRef(name) {
			return cmdShow(ctx, args)
		}
		return contract.NewError(contract.ErrInvalidArgument,
			"Unknown command: "+name+".", nil)
	}
}

func commandAcceptsInvocationInput(name string) bool {
	command, ok := commandInfo(name)
	return ok && command.acceptsInvocationInput
}

func unexpectedInvocationInput(command string) error {
	return contract.NewError(contract.ErrInvalidArgument,
		"Request stdin is only supported by explicit stdin-consuming forms of create, update, release, reject, and close.",
		nil)
}

func normalizeObjectFirst(args []string) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}
	if _, _, ok := store.ParseID(args[0]); !ok {
		return args, nil
	}
	if len(args) == 1 || strings.HasPrefix(args[1], "-") {
		normalized := []string{"show", args[0]}
		return append(normalized, args[1:]...), nil
	}
	command := args[1]
	metadata, ok := commandInfo(command)
	if !ok || !metadata.objectFirst {
		message := "Object-first syntax supports only single-ticket commands."
		if knownCommand(command) {
			message = "Command " + command + " cannot be used after a ticket ID."
		}
		return nil, contract.NewError(contract.ErrInvalidArgument, message, nil)
	}
	normalized := make([]string, 0, len(args)+1)
	normalized = append(normalized, metadata.name, args[0])
	normalized = append(normalized, args[2:]...)
	return normalized, nil
}

func consumeLeadingGlobals(args []string, g globalOpts) ([]string, globalOpts, error) {
	seenJSON, seenScope, seenConfig, seenDebug := false, false, false, false
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "--debug":
			if seenDebug {
				return nil, g, contract.NewError(contract.ErrInvalidArgument, "Duplicate flag --debug.", nil)
			}
			seenDebug = true
			g.debug = true
			args = args[1:]
		case arg == "-j" || arg == "--json":
			if seenJSON {
				return nil, g, contract.NewError(contract.ErrInvalidArgument, "Duplicate flag --json.", nil)
			}
			seenJSON = true
			g.json = true
			args = args[1:]
		case arg == "-c" || arg == "--config":
			if g.sessionBound {
				return nil, g, contract.NewError(contract.ErrInvalidArgument,
					"Interactive sessions cannot use --config after startup.", nil)
			}
			if seenConfig {
				return nil, g, contract.NewError(contract.ErrInvalidArgument, "Duplicate flag --config.", nil)
			}
			if len(args) < 2 {
				return nil, g, contract.NewError(contract.ErrInvalidArgument, "Flag --config requires a value.", nil)
			}
			seenConfig = true
			g.configPath, g.configExplicit = args[1], true
			args = args[2:]
		case arg == "--scope":
			if g.sessionBound {
				return nil, g, contract.NewError(contract.ErrInvalidArgument,
					"Interactive sessions cannot use --scope after startup.", nil)
			}
			if seenScope {
				return nil, g, contract.NewError(contract.ErrInvalidArgument, "Duplicate flag --scope.", nil)
			}
			if len(args) < 2 {
				return nil, g, contract.NewError(contract.ErrInvalidArgument, "Flag --scope requires a value.", nil)
			}
			seenScope = true
			g.scopeName, g.scopeExplicit = args[1], true
			args = args[2:]
		case strings.HasPrefix(arg, "--config="):
			if g.sessionBound {
				return nil, g, contract.NewError(contract.ErrInvalidArgument,
					"Interactive sessions cannot use --config after startup.", nil)
			}
			if seenConfig {
				return nil, g, contract.NewError(contract.ErrInvalidArgument, "Duplicate flag --config.", nil)
			}
			seenConfig = true
			g.configPath, g.configExplicit = strings.TrimPrefix(arg, "--config="), true
			args = args[1:]
		case strings.HasPrefix(arg, "--scope="):
			if g.sessionBound {
				return nil, g, contract.NewError(contract.ErrInvalidArgument,
					"Interactive sessions cannot use --scope after startup.", nil)
			}
			if seenScope {
				return nil, g, contract.NewError(contract.ErrInvalidArgument, "Duplicate flag --scope.", nil)
			}
			seenScope = true
			g.scopeName, g.scopeExplicit = strings.TrimPrefix(arg, "--scope="), true
			args = args[1:]
		default:
			return args, g, nil
		}
	}
	return args, g, nil
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
		fmt.Fprintf(ctx.stdout, "ticket %s (api %d, storage %d)\n", safeSingleLine(Version), APIVersion, StorageVersion)
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
