package cli

import (
	"errors"
	"io"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/jsonx"
)

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func decodeInput(input io.Reader, path string, out any) error {
	data, err := readInputData(input, path)
	if err != nil {
		return err
	}
	if err := jsonx.Decode(data, out); err != nil {
		if errors.Is(err, jsonx.ErrUnknownField) {
			return contract.NewError(contract.ErrInvalidArgument,
				"Unknown key in --input JSON: "+err.Error(), nil)
		}
		return contract.NewError(contract.ErrInvalidJSON,
			"Invalid --input JSON: "+err.Error(), nil)
	}
	return nil
}

func readInputData(input io.Reader, path string) ([]byte, error) {
	return readSource(input, path, maxInvocationInputBytes)
}

func sessionStdinError(command string) error {
	return contract.NewError(contract.ErrInvalidArgument,
		"Command "+command+" cannot consume input from the session stream; provide its input explicitly.", nil)
}
