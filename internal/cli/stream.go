package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/jsonx"
)

// A JSON string can represent each decoded input byte as a six-byte \u00XX
// escape. Leave additional room for the request envelope and argv.
const maxJSONStreamFrameBytes = maxInvocationInputBytes*6 + 1<<20

var errJSONStreamFrameTooLarge = errors.New("JSON streaming request exceeds the maximum encoded frame size")

type jsonStreamRequest struct {
	Args  *[]string      `json:"args"`
	Stdin optionalString `json:"stdin"`
}

type optionalString struct {
	value string
	set   bool
}

func (s *optionalString) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("stdin must be a string")
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	s.value = value
	s.set = true
	return nil
}

// jsonStreamLoop implements an NDJSON request/response transport. Each
// nonblank input line is one {"args":[...]} request and produces exactly one
// ordinary JSON success or error response. EOF is the only transport-level
// termination signal.
func jsonStreamLoop(executor *sessionExecutor, input *bufio.Reader, out io.Writer) int {
	for {
		frame, eof, err := readJSONStreamFrame(input)
		if eof {
			return 0
		}
		if err != nil {
			ce := contract.NewError(contract.ErrFileTooLarge, "JSON streaming request exceeds the maximum encoded frame size.", nil)
			if !errors.Is(err, errJSONStreamFrameTooLarge) {
				ce = contract.NewError(contract.ErrIOError, "Cannot read JSON streaming request: "+err.Error(), nil)
			}
			if emitJSONStreamError(out, ce) != nil {
				return contract.ExitCode(contract.ErrIOError)
			}
			if !errors.Is(err, errJSONStreamFrameTooLarge) {
				return contract.ExitCode(ce.Code)
			}
			continue
		}
		if len(bytes.TrimSpace(frame)) == 0 {
			continue
		}

		args, stdin, err := decodeJSONStreamRequest(frame)
		if err != nil {
			ce := contract.NewError(contract.ErrInvalidJSON, "Invalid JSON streaming request: "+err.Error(), nil)
			if emitJSONStreamError(out, ce) != nil {
				return contract.ExitCode(contract.ErrIOError)
			}
			continue
		}

		var response bytes.Buffer
		commandArgs := append([]string{"-j"}, args...)
		var invocationInput io.Reader
		if stdin != nil {
			invocationInput = bytes.NewReader([]byte(*stdin))
		}
		if err := executor.dispatchWithInput(commandArgs, &response, invocationInput); err != nil {
			if emitJSONStreamError(out, asContractError(err)) != nil {
				return contract.ExitCode(contract.ErrIOError)
			}
			continue
		}
		if _, err := io.Copy(out, &response); err != nil {
			return contract.ExitCode(contract.ErrIOError)
		}
	}
}

func decodeJSONStreamRequest(frame []byte) ([]string, *string, error) {
	var request jsonStreamRequest
	if err := jsonx.Decode(frame, &request); err != nil {
		return nil, nil, err
	}
	if request.Args == nil {
		return nil, nil, errors.New(`request requires an "args" array`)
	}
	if !request.Stdin.set {
		return *request.Args, nil, nil
	}
	return *request.Args, &request.Stdin.value, nil
}

func emitJSONStreamError(out io.Writer, ce *contract.Error) error {
	return emitJSON(out, struct {
		Error *contract.Error `json:"error"`
	}{Error: ce})
}

func readJSONStreamFrame(input *bufio.Reader) ([]byte, bool, error) {
	var frame []byte
	tooLarge := false
	for {
		fragment, err := input.ReadSlice('\n')
		payload := fragment
		if err == nil {
			payload = bytes.TrimSuffix(fragment, []byte{'\n'})
		}
		if !tooLarge {
			if len(frame)+len(payload) > maxJSONStreamFrameBytes {
				tooLarge = true
				frame = nil
			} else {
				frame = append(frame, payload...)
			}
		}

		switch {
		case err == nil:
			if tooLarge {
				return nil, false, errJSONStreamFrameTooLarge
			}
			return frame, false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if tooLarge {
				return nil, false, errJSONStreamFrameTooLarge
			}
			if len(frame) == 0 {
				return nil, true, nil
			}
			return frame, false, nil
		default:
			return nil, false, err
		}
	}
}
