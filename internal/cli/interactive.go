package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
)

// createWithEditor keeps the draft outside the managed ticket collection
// until editing and validation have completed.
func createWithEditor(ctx *commandContext, opts domain.CreateOptions) error {
	st, err := ctx.globalOpts.openStore(ctx.cwd)
	if err != nil {
		return err
	}
	starter := domain.RenderNewForEditor(opts.Title, opts.Priority, opts.Tags, opts.Parent, opts.DependsOn, opts.Sections)
	starterTicket, err := domain.ParseTicketFile("", starter)
	if err != nil {
		st.Close()
		return err
	}
	draftPath, err := createDraft(st.Root, starter)
	if err != nil {
		st.Close()
		return err
	}
	if ctx.session == nil {
		if err := st.ClearCurrent(); err != nil {
			st.Close()
			return preserveCreateDraftError(ctx, err, draftPath)
		}
	}
	st.Close()

	draft, err := editDraft(draftPath, "", objectiveEditLine(starterTicket), ctx.session == nil, &ctx.globalOpts)
	if err != nil {
		return preserveCreateDraftError(ctx, err, draftPath)
	}

	st, backend, err := openSynchronizedStore(&ctx.globalOpts, ctx.cwd)
	if err != nil {
		return preserveCreateDraftError(ctx, err, draftPath)
	}
	defer st.Close()
	res, err := domain.CreateFromDraft(st, draft)
	if err != nil {
		return preserveCreateDraftError(ctx, err, draftPath)
	}
	if err := persistMutation(backend, st, "create", res); err != nil {
		return preserveCreateDraftError(ctx, err, draftPath)
	}
	_ = st.SignalChange()
	_ = os.Remove(draftPath)
	rememberCurrentTicket(ctx, st, res)
	return renderHumanTo(ctx.stdout, "create", res, false, ctx.decorator())
}

// editWithEditor snapshots an existing ticket under the lock, releases it for
// all editor interaction, and publishes only after an optimistic byte check.
func editWithEditor(ctx *commandContext, ref string) error {
	st, backend, err := openSynchronizedStore(&ctx.globalOpts, ctx.cwd)
	if err != nil {
		return err
	}
	full, err := resolveTicketRef(st, ctx, ref, "edit")
	if err != nil {
		st.Close()
		return err
	}
	beforeTicket, err := domain.ReadTicket(st, full)
	if err != nil {
		st.Close()
		return err
	}
	actor, _ := ctx.effectiveActor()
	if actor == "" {
		actor = "user"
	}
	shouldClaim := beforeTicket.Assignee == "" && beforeTicket.State == "review"
	if beforeTicket.Assignee == "" && beforeTicket.State == "open" {
		readiness, err := domain.Readiness(st, full)
		if err != nil {
			st.Close()
			return err
		}
		shouldClaim = readiness.Ready
	}
	if shouldClaim {
		claim, err := domain.Claim(st, full, domain.ClaimOptions{Actor: actor})
		if err != nil {
			st.Close()
			return err
		}
		if claim.Changed {
			if err := persistMutation(backend, st, "claim", claim); err != nil {
				st.Close()
				return err
			}
		}
		beforeTicket, err = domain.ReadTicket(st, full)
		if err != nil {
			st.Close()
			return err
		}
	} else if beforeTicket.Assignee != "" && beforeTicket.Assignee != actor {
		warnEditOwnership(beforeTicket, actor)
	}
	original := append([]byte(nil), beforeTicket.FileBytes...)
	draftPath, err := createDraft(st.Root, original)
	st.Close()
	if err != nil {
		return err
	}

	draft, err := editDraft(draftPath, full, objectiveEditLine(beforeTicket), ctx.session == nil, &ctx.globalOpts)
	if err != nil {
		return err
	}

	st, backend, err = openSynchronizedStore(&ctx.globalOpts, ctx.cwd)
	if err != nil {
		return preserveDraftError(err, draftPath)
	}
	defer st.Close()
	current, err := domain.ReadTicket(st, full)
	if err != nil {
		return preserveDraftError(err, draftPath)
	}
	if !bytes.Equal(original, current.FileBytes) {
		return preserveDraftError(contract.NewError(contract.ErrConflict,
			"Ticket changed while it was being edited; refusing to overwrite newer changes.",
			map[string]any{"id": full}), draftPath)
	}
	if _, err := domain.PublishEditedTicket(st, draft); err != nil {
		return preserveDraftError(err, draftPath)
	}
	res := &EditResult{ID: full, Path: full + "/TASK.md", Changed: !bytes.Equal(original, draft.FileBytes)}
	if err := persistMutation(backend, st, "edit", res); err != nil {
		return preserveDraftError(err, draftPath)
	}
	_ = st.SignalChange()
	_ = os.Remove(draftPath)
	rememberCurrentTicket(ctx, st, res)
	return renderHumanTo(ctx.stdout, "edit", res, false, ctx.decorator())
}

func warnEditOwnership(ticket *domain.Ticket, actor string) {
	fmt.Fprintf(os.Stderr, "warning: ticket %s is assigned to %s; editing without changing ownership (actor=%s)\n",
		ticket.ID, ticket.Assignee, actor)
}

func createDraft(root string, data []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Join(root, ".local"), "draft-*.md")
	if err != nil {
		return "", contract.NewError(contract.ErrIOError, "Cannot create editor draft: "+err.Error(), nil)
	}
	path := f.Name()
	keep := false
	defer func() {
		_ = f.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return "", contract.NewError(contract.ErrIOError, "Cannot write editor draft: "+err.Error(), nil)
	}
	if err := f.Close(); err != nil {
		return "", contract.NewError(contract.ErrIOError, "Cannot close editor draft: "+err.Error(), nil)
	}
	keep = true
	return path, nil
}

func editDraft(path, id string, line int, allowRetry bool, configs ...*globalOpts) (*domain.Ticket, error) {
	for {
		command, args, err := editorInvocation(selectedEditor(configs...), path, line)
		if err != nil {
			return nil, preserveDraftError(err, path)
		}
		editor := exec.Command(command, args...)
		editor.Stdin = os.Stdin
		editor.Stdout = os.Stdout
		editor.Stderr = os.Stderr
		if err := editor.Run(); err != nil {
			return nil, preserveDraftError(contract.NewError(contract.ErrIOError,
				"Editor failed: "+err.Error(), nil), path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, preserveDraftError(contract.NewError(contract.ErrIOError,
				"Cannot read editor draft: "+err.Error(), nil), path)
		}
		ticket, err := domain.ParseTicketFile(id, data)
		if err == nil {
			return ticket, nil
		}
		if allowRetry && retryEditor(err, nil) {
			continue
		}
		return nil, preserveDraftError(err, path)
	}
}

func retryEditor(err error, input *bufio.Reader) bool {
	message := safeSingleLine(err.Error())
	var ce *contract.Error
	if errors.As(err, &ce) {
		message = humanErrorMessage(ce)
	}
	fmt.Fprintf(os.Stderr, "error: %s\nContinue editing? [Y/n] ", message)
	if input == nil {
		input = bufio.NewReader(os.Stdin)
	}
	answer, readErr := input.ReadString('\n')
	fmt.Fprintln(os.Stderr)
	if readErr != nil && strings.TrimSpace(answer) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}

func preserveDraftError(err error, path string) error {
	if err == nil {
		return nil
	}
	var ce *contract.Error
	if errors.As(err, &ce) {
		return contract.NewError(ce.Code, ce.Message+" Edited draft preserved at: "+path, ce.Details)
	}
	return contract.NewError(contract.ErrIOError, err.Error()+" Edited draft preserved at: "+path, nil)
}

func preserveCreateDraftError(ctx *commandContext, err error, path string) error {
	wrapped := preserveDraftError(err, path)
	var ce *contract.Error
	if errors.As(wrapped, &ce) {
		if applied, _ := ce.Details["mutation_applied"].(bool); applied {
			id, _ := ce.Details["id"].(string)
			if ctx.session != nil && id != "" {
				ctx.session.current = id
			}
			return contract.NewError(ce.Code, ce.Message+" Reconcile SCM persistence before retrying; do not create another ticket."+createSelectionDiagnostic(ctx, true, id), ce.Details)
		}
		return contract.NewError(ce.Code, ce.Message+" No ticket was created."+createSelectionDiagnostic(ctx, false, ""), ce.Details)
	}
	return wrapped
}

func createSelectionDiagnostic(ctx *commandContext, applied bool, id string) string {
	if ctx.session == nil {
		return currentSelectionDiagnostic()
	}
	if applied && id != "" {
		return " Session current ticket is now " + id + "."
	}
	if ctx.session.current != "" {
		return " Session current ticket remains " + ctx.session.current + "."
	}
	return " Session current ticket remains unset."
}

func currentSelectionDiagnostic() string {
	if current := strings.TrimSpace(os.Getenv("TICKET_CURRENT")); current != "" {
		return " Local current ticket is unset; TICKET_CURRENT still selects " + current + "."
	}
	return " Current ticket is unset."
}
