package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/store"
)

// sessionState is intentionally only an in-memory ticket identity. It does
// not retain a store, lock, SCM backend, or parsed ticket between commands.
type sessionState struct {
	current string
}

var errSessionInterrupted = errors.New("interactive command interrupted")

// sessionExecutor is the transport-independent execution seam used by
// interactive transports. Each command still goes through the ordinary
// dispatcher and opens and releases its repository resources independently.
type sessionExecutor struct {
	state sessionState
	cwd   string
	done  <-chan struct{}
	globalOpts
	bound bool
}

func newSessionExecutor() *sessionExecutor {
	return &sessionExecutor{}
}

func (s *sessionExecutor) dispatch(args []string, stdout *bytes.Buffer) error {
	return s.dispatchWithInput(args, stdout, nil)
}

func (s *sessionExecutor) dispatchWithInput(args []string, stdout *bytes.Buffer, input io.Reader) error {
	if !s.bound {
		g := globalOpts{scopeName: strings.TrimSpace(os.Getenv("TICKET_SCOPE"))}
		var err error
		args, g, err = consumeLeadingGlobals(args, g)
		if err != nil {
			return err
		}
		startupJSON := g.json
		g.json = false
		if err := s.bind(cwd(), g); err != nil {
			return err
		}
		if startupJSON {
			args = append([]string{"-j"}, args...)
		}
	}
	return dispatchWithSessionInput(args, stdout, s, input, input != nil)
}

func (s *sessionExecutor) currentTicket() string {
	return s.state.current
}

func (s *sessionExecutor) bind(startCWD string, g globalOpts) error {
	if s.bound {
		return contract.NewError(contract.ErrInvalidArgument,
			"Session repository is already bound.", nil)
	}
	if err := g.loadConfig(startCWD); err != nil {
		return err
	}
	root, err := store.DiscoverWithRoot(startCWD, g.selectedRoot())
	if err != nil {
		return err
	}
	g.rootOverride = root
	s.cwd = startCWD
	s.globalOpts = g
	s.bound = true
	return nil
}

func (s *sessionExecutor) setDone(done <-chan struct{}) {
	s.done = done
}
