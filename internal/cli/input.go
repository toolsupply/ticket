// CLI invocation input is bounded across stdin and file sources.
package cli

import (
	"io"
	"os"

	"github.com/toolsupply/ticket/internal/contract"
)

// maxInvocationInputBytes bounds CLI invocation input from stdin and files.
const maxInvocationInputBytes = 2 << 20

// readStdin reads bounded CLI invocation input from stdin.
func readStdin(input io.Reader, limit int) ([]byte, error) {
	if input == nil {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Input stream is unavailable.", nil)
	}
	data, err := io.ReadAll(io.LimitReader(input, int64(limit)+1))
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError,
			"Input stream could not be read: "+err.Error(), nil)
	}
	if len(data) > limit {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Input exceeds the 2 MiB limit.", nil)
	}
	return data, nil
}

// readSource reads bounded CLI invocation input from FILE or stdin. It never
// probes content to guess a mode.
func readSource(input io.Reader, path string, limit int) ([]byte, error) {
	if path == "-" {
		return readStdin(input, limit)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError,
			"Could not read input file: "+err.Error(), map[string]any{"path": path})
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError,
			"Input file could not be read: "+err.Error(), map[string]any{"path": path})
	}
	if len(data) > limit {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Input file exceeds the 2 MiB limit.", map[string]any{"path": path})
	}
	return data, nil
}
