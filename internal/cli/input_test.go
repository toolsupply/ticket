package cli

import (
	"errors"
	"testing"

	"github.com/toolsupply/ticket/internal/contract"
)

type failingInput struct{}

func (failingInput) Read([]byte) (int, error) {
	return 0, errors.New("test input failure")
}

func TestReadSourceClassifiesStdinReadFailureAsIOError(t *testing.T) {
	_, err := readSource(failingInput{}, "-", maxInvocationInputBytes)
	if err == nil {
		t.Fatal("readSource accepted a failing stdin reader")
	}
	ce, ok := err.(*contract.Error)
	if !ok || ce.Code != contract.ErrIOError {
		t.Fatalf("readSource error=%T %v, want io_error", err, err)
	}
}
