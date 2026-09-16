// --input reading from stdin, bounded.
package cli

import (
	"io"
	"os"

	"ticket/internal/contract"
)

// maxInputBytes is the spec limit for structured input.
const maxInputBytes = 2 << 20

// readStdin reads all of stdin, failing if it exceeds limit.
func readStdin(limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, int64(limit)+1))
	if err != nil {
		return nil, contract.NewError(contract.ErrInvalidJSON,
			"Input stream could not be read: "+err.Error(), nil)
	}
	if len(data) > limit {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Input exceeds the 2 MiB limit.", nil)
	}
	return data, nil
}
