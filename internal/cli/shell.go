package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
	"github.com/toolsupply/ticket/internal/store"
	"github.com/toolsupply/ticket/internal/terminaltitle"
)

var setInteractiveTerminalTitle = terminaltitle.Set

func interactiveRequested(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return false
		}
		if arg == "-i" || arg == "--interactive" {
			return true
		}
		switch {
		case arg == "-j" || arg == "--json" || arg == "--debug":
		case arg == "-c" || arg == "--config" || arg == "--scope":
			i++
		case strings.HasPrefix(arg, "--config=") || strings.HasPrefix(arg, "--scope="):
		default:
			return false
		}
	}
	return false
}

func runInteractive(args []string, out io.Writer) int {
	return runInteractiveIO(args, out, os.Stdin, os.Stderr)
}

func runInteractiveIO(args []string, out io.Writer, stdin io.Reader, stderr io.Writer) int {
	jsonMode := jsonRequested(args)
	args, err := removeInteractiveFlags(args)
	if err != nil {
		return emitInteractiveStartupError(err, jsonMode, out, stderr)
	}
	g := globalOpts{scopeName: strings.TrimSpace(os.Getenv("TICKET_SCOPE"))}
	args, g, err = consumeLeadingGlobals(args, g)
	if err != nil {
		return emitInteractiveStartupError(err, jsonMode, out, stderr)
	}
	if len(args) != 0 {
		message := "Interactive mode accepts startup options only; provide commands at the prompt."
		if g.json {
			message = "JSON streaming mode accepts startup options only; provide commands as framed stdin requests."
		}
		return emitInteractiveStartupError(contract.NewError(contract.ErrInvalidArgument,
			message, nil), g.json, out, stderr)
	}
	g.machineTransport = g.json

	executor := newSessionExecutor()
	if err := executor.bind(cwd(), g); err != nil {
		return emitInteractiveStartupError(err, g.json, out, stderr)
	}
	if g.json {
		return jsonStreamLoop(executor, bufio.NewReader(stdin), out)
	}
	input, ok := stdin.(*os.File)
	if !ok {
		return emitInteractiveStartupError(contract.NewError(contract.ErrInvalidArgument,
			"Interactive input must be a file-backed stream.", nil), false, out, stderr)
	}
	reader := bufio.NewReader(input)
	seedInteractiveCurrent(executor, stderr)
	return interactiveLoopWithStdin(executor, reader, out, stderr, input)
}

func setInteractiveTitle(executor *sessionExecutor) {
	setInteractiveTerminalTitle(interactiveTerminalTitle(executor))
}

func interactiveTerminalTitle(executor *sessionExecutor) string {
	if executor.state.current == "" {
		return "ticket : idle"
	}
	st, err := executor.globalOpts.openStore(executor.cwd)
	if err == nil {
		defer st.Close()
		if full, resolveErr := st.ResolveID(executor.state.current, true); resolveErr == nil {
			if ticket, readErr := domain.ReadTicket(st, full); readErr == nil {
				return "ticket : " + safeSingleLine(full) + " : " + safeSingleLine(ticket.Title)
			}
		}
	}
	return "ticket : " + safeSingleLine(executor.state.current)
}

func removeInteractiveFlags(args []string) ([]string, error) {
	result := make([]string, 0, len(args))
	seen := false
	for i, arg := range args {
		if arg == "--" {
			result = append(result, args[i:]...)
			break
		}
		if arg == "-i" || arg == "--interactive" {
			if seen {
				return nil, contract.NewError(contract.ErrInvalidArgument,
					"Duplicate flag --interactive.", nil)
			}
			seen = true
			continue
		}
		result = append(result, arg)
	}
	return result, nil
}

func emitInteractiveStartupError(err error, jsonMode bool, out, stderr io.Writer) int {
	ce := asContractError(err)
	if jsonMode {
		_ = emitJSON(out, struct {
			Error *contract.Error `json:"error"`
		}{Error: ce})
	} else {
		fmt.Fprintln(stderr, "error: "+humanErrorMessage(ce))
	}
	return contract.ExitCode(ce.Code)
}

func asContractError(err error) *contract.Error {
	if err == nil {
		return contract.NewError(contract.ErrInternalError, "Internal error.", nil)
	}
	var ce *contract.Error
	if errors.As(err, &ce) {
		return ce
	}
	return contract.NewError(contract.ErrInternalError, "Internal error: "+err.Error(), nil)
}

func seedInteractiveCurrent(executor *sessionExecutor, stderr io.Writer) {
	ref, source, ok := interactiveSeedReference(executor.globalOpts.rootOverride)
	if !ok {
		return
	}
	st, err := executor.globalOpts.openStore(executor.cwd)
	if err == nil {
		defer st.Close()
		full, resolveErr := st.ResolveID(ref, true)
		if resolveErr == nil {
			if _, readErr := domain.ReadTicket(st, full); readErr == nil {
				executor.state.current = full
				return
			} else {
				resolveErr = readErr
			}
		}
		if resolveErr != nil {
			fmt.Fprintf(stderr, "warning: %s is not a resolvable ticket; no current ticket selected.\n", source)
			return
		}
	}
	fmt.Fprintf(stderr, "warning: %s could not be read; no current ticket selected.\n", source)
}

func interactiveSeedReference(root string) (string, string, bool) {
	if value, set := os.LookupEnv("TICKET_CURRENT"); set {
		return strings.TrimSpace(value), "TICKET_CURRENT", strings.TrimSpace(value) != ""
	}
	path := filepath.Join(root, ".local", "current")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", "", false
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", ".local/current", true
	}
	data, err := store.ReadBoundedFile(path, 128)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return "", ".local/current", true
	}
	return strings.TrimSpace(string(data)), ".local/current", true
}

func interactiveLoop(executor *sessionExecutor, input *bufio.Reader, out, stderr io.Writer) int {
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	return interactiveLoopWithStdinAndInterrupts(executor, input, out, stderr, os.Stdin, interrupts)
}

func interactiveLoopWithInterrupts(executor *sessionExecutor, input *bufio.Reader, out, stderr io.Writer, interrupts <-chan os.Signal) int {
	return interactiveLoopWithStdinAndInterrupts(executor, input, out, stderr, os.Stdin, interrupts)
}

func interactiveLoopWithStdin(executor *sessionExecutor, input *bufio.Reader, out, stderr io.Writer, commandStdin io.Reader) int {
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	return interactiveLoopWithStdinAndInterrupts(executor, input, out, stderr, commandStdin, interrupts)
}

func interactiveLoopWithStdinAndInterrupts(executor *sessionExecutor, input *bufio.Reader, out, stderr io.Writer, commandStdin io.Reader, interrupts <-chan os.Signal) int {
	lastTitle := ""
	for {
		title := interactiveTerminalTitle(executor)
		if title != lastTitle {
			setInteractiveTerminalTitle(title)
			lastTitle = title
		}
		if err := renderInteractivePrompt(executor, stderr); err != nil {
			fmt.Fprintln(stderr, "error: "+safeSingleLine(err.Error()))
		}
		line, err := readInteractiveLine(input, interrupts, executor, stderr)
		if err != nil {
			if errors.Is(err, io.EOF) && line == "" {
				return 0
			}
			if !errors.Is(err, io.EOF) {
				fmt.Fprintln(stderr, "error: "+safeSingleLine(err.Error()))
				continue
			}
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if command, ok := interactiveShellCommandLine(line); ok {
			if err := runInteractiveShellCommand(command, commandStdin, out, stderr); err != nil {
				fmt.Fprintln(stderr, "error: "+safeSingleLine(err.Error()))
			}
			continue
		}
		argv, err := splitCommandLine(line)
		if err != nil {
			fmt.Fprintln(stderr, "error: "+safeSingleLine(err.Error()))
			continue
		}
		if len(argv) == 0 {
			continue
		}
		if len(argv) == 1 && (argv[0] == "exit" || argv[0] == "quit") {
			return 0
		}

		var commandOutput bytes.Buffer
		done := make(chan struct{})
		executor.setDone(done)
		result := make(chan error, 1)
		go func() { result <- executor.dispatch(argv, &commandOutput) }()
		var commandErr error
		select {
		case commandErr = <-result:
		case <-interrupts:
			close(done)
			fmt.Fprintln(stderr, "^C")
			commandErr = <-result
		}
		executor.setDone(nil)
		if commandErr != nil {
			if errors.Is(commandErr, errSessionInterrupted) {
				continue
			}
			emitInteractiveCommandError(commandErr, argv, out, stderr)
			continue
		}
		_, _ = io.Copy(out, &commandOutput)
	}
}

func interactiveShellCommandLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "!") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "!")), true
}

func runInteractiveShellCommand(command string, stdin io.Reader, stdout, stderr io.Writer) error {
	if command == "" {
		return errors.New("shell command is empty")
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd.exe", "/d", "/s", "/c", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("shell command failed: %w", err)
	}
	return nil
}

type interactiveReadResult struct {
	line string
	err  error
}

// readInteractiveLine keeps prompt interrupts separate from command
// interrupts. Ctrl-C at an idle prompt prints a fresh prompt and continues
// waiting for a line; the signal is consumed here and cannot cancel the next
// command.
func readInteractiveLine(input *bufio.Reader, interrupts <-chan os.Signal, executor *sessionExecutor, stderr io.Writer) (string, error) {
	result := make(chan interactiveReadResult, 1)
	go func() {
		line, err := input.ReadString('\n')
		result <- interactiveReadResult{line: line, err: err}
	}()
	for {
		select {
		case read := <-result:
			// A signal delivered before the read completed belongs to the prompt,
			// even if both became ready before this select iteration.
			for {
				select {
				case <-interrupts:
					fmt.Fprintln(stderr, "^C")
				default:
					return read.line, read.err
				}
			}
		case <-interrupts:
			fmt.Fprintln(stderr, "^C")
			if err := renderInteractivePrompt(executor, stderr); err != nil {
				fmt.Fprintln(stderr, "error: "+safeSingleLine(err.Error()))
			}
		}
	}
}

func renderInteractivePrompt(executor *sessionExecutor, stderr io.Writer) error {
	label := "[no current]"
	if executor.state.current != "" {
		label = executor.state.current + " [missing]"
		st, err := executor.globalOpts.openStore(executor.cwd)
		if err != nil {
			label = "[no repository]"
		} else {
			full, resolveErr := st.ResolveID(executor.state.current, true)
			if resolveErr == nil {
				if ticket, readErr := domain.ReadTicket(st, full); readErr == nil {
					label = full + " [" + ticket.State + "]"
				}
			}
			st.Close()
		}
	}
	_, err := fmt.Fprintf(stderr, "ticket %s> ", safeSingleLine(label))
	return err
}

func emitInteractiveCommandError(err error, argv []string, out, stderr io.Writer) {
	ce := asContractError(err)
	if jsonRequested(argv) {
		_ = emitJSON(out, struct {
			Error *contract.Error `json:"error"`
		}{Error: ce})
		return
	}
	fmt.Fprintln(stderr, "error: "+humanErrorMessage(ce))
}
