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
	if path != "-" {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"Structured input must be provided with --input -.", nil)
	}
	data, err := readStdin(input, 2<<20)
	if err != nil {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Input could not be read: "+err.Error(), nil)
	}
	if len(data) > 2<<20 {
		return nil, contract.NewError(contract.ErrFileTooLarge,
			"Input exceeds the 2 MiB limit.", nil)
	}
	return data, nil
}

func sessionStdinError(command string) error {
	return contract.NewError(contract.ErrInvalidArgument,
		"Command "+command+" cannot read structured input from stdin inside an interactive session; supply invocation input explicitly.", nil)
}
