// Human rendering: prose on stdout for success; the failure path
// renders prose to stderr (see Run).
package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
)

func sanitizeTerminalText(value string, multiline bool) string {
	var out strings.Builder
	for _, r := range value {
		switch {
		case r == '\n' && multiline:
			out.WriteRune(r)
		case r == '\n' || r == '\r' || r == '\t':
			out.WriteByte(' ')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// Drop C0/C1 controls, ESC, BEL, and DEL. Any remaining bytes
			// from an escape sequence are ordinary visible text.
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func safeSingleLine(value string) string { return sanitizeTerminalText(value, false) }

func safeMultiline(value string) string { return sanitizeTerminalText(value, true) }

// renderHumanTo writes compact human output for a successful command result.
func renderHumanTo(stdout *bytes.Buffer, cmd string, res any, markdown bool, decorators ...string) error {
	if markdown {
		return renderMarkdownTo(stdout, cmd, res)
	}
	decorator := os.Getenv("TICKET_DECORATOR")
	if len(decorators) > 0 {
		decorator = decorators[0]
	}
	if decorator != "" && decorator != "none" && decoratorCommand(cmd) {
		var rendered bytes.Buffer
		if err := renderMarkdownTo(&rendered, cmd, res); err != nil {
			return err
		}
		return runMarkdownDecorator(stdout, decorator, rendered.Bytes())
	}
	return renderPlainHumanTo(stdout, cmd, res)
}

func renderPlainHumanTo(stdout *bytes.Buffer, cmd string, res any) error {
	switch r := res.(type) {
	case *currentTicketSummary:
		fmt.Fprintf(stdout, "%s  %s  %s\n", safeSingleLine(r.ID), safeSingleLine(r.State), safeSingleLine(r.Title))
	case *domain.CreateResult:
		fmt.Fprintf(stdout, "created %s\n\n", safeSingleLine(r.ID))
		fmt.Fprintf(stdout, "title:      %s\n", truncateHuman(safeSingleLine(r.Title), 40))
		if r.State != "" {
			fmt.Fprintf(stdout, "state:      %s\npriority:   P%d\n", safeSingleLine(r.State), r.Priority)
			if r.Objective != "" {
				fmt.Fprintf(stdout, "\nobjective:  %s\n", truncateHuman(safeSingleLine(r.Objective), 40))
			}
		}
	case *domain.DeleteResult:
		fmt.Fprintf(stdout, "deleted %s\n", safeSingleLine(r.ID))
	case *domain.BatchDeleteResult:
		fmt.Fprintf(stdout, "deleted %d tickets\n", len(r.Items))
	case *domain.ArchiveResult:
		if r.Archived {
			fmt.Fprintf(stdout, "archived %s\n", safeSingleLine(r.ID))
		} else {
			fmt.Fprintf(stdout, "unarchived %s\n", safeSingleLine(r.ID))
		}
	case *domain.NextResult:
		if r.Item == nil {
			fmt.Fprintln(stdout, "no eligible work")
			break
		}
		priority := ""
		if r.Item.Priority != nil {
			priority = fmt.Sprintf("P%d", *r.Item.Priority)
		}
		title := ""
		if r.Item.Title != nil {
			title = *r.Item.Title
		}
		fmt.Fprintf(stdout, "%s  %s  %s\n", priority, safeSingleLine(r.Item.ID), safeSingleLine(title))
	case *EditResult:
		if r.Changed {
			fmt.Fprintf(stdout, "edited %s\n", safeSingleLine(r.ID))
		} else {
			fmt.Fprintf(stdout, "unchanged %s\n", safeSingleLine(r.ID))
		}
	case *domain.ListResult:
		if r.IDsOnly {
			for _, it := range r.Items {
				fmt.Fprintln(stdout, safeSingleLine(it.ID))
			}
			break
		}
		if r.ShowDependencies {
			fmt.Fprintf(stdout, "%-10s %-4s %-14s %-42s %-20s %s\n", "STATE", "PRI", "ID", "TITLE", "ASSIGNEE", "BLOCKED BY")
		} else {
			fmt.Fprintf(stdout, "%-10s %-4s %-14s %-42s %s\n", "STATE", "PRI", "ID", "TITLE", "ASSIGNEE")
		}
		for _, it := range r.Items {
			state := ""
			if it.State != nil {
				state = *it.State
			}
			pri := ""
			if it.Priority != nil {
				pri = fmt.Sprintf("P%d", *it.Priority)
			}
			title := ""
			if it.Title != nil {
				title = *it.Title
			}
			assignee := ""
			if it.Assignee != nil {
				assignee = "@" + *it.Assignee
			}
			if r.ShowDependencies {
				fmt.Fprintf(stdout, "%-10s %-4s %-14s %-42s %-20s %s\n",
					truncateHuman(safeSingleLine(state), 10), truncateHuman(safeSingleLine(pri), 4), safeSingleLine(it.ID),
					truncateHuman(safeSingleLine(title), 42), truncateHuman(safeSingleLine(assignee), 20), formatDependencies(it.Dependencies, 30))
			} else {
				fmt.Fprintf(stdout, "%-10s %-4s %-14s %-42s %s\n",
					truncateHuman(safeSingleLine(state), 10), truncateHuman(safeSingleLine(pri), 4), safeSingleLine(it.ID),
					truncateHuman(safeSingleLine(title), 42), truncateHuman(safeSingleLine(assignee), 20))
			}
		}
	case *domain.GraphResult:
		renderGraph(stdout, r)
	case *domain.StatusView:
		renderHumanStatus(stdout, r)
	case *domain.ShowView:
		fmt.Fprintf(stdout, "%s %s\n\n", safeSingleLine(r.ID), safeSingleLine(r.Title))
		fmt.Fprintf(stdout, "state:      %s\npriority:   P%d\n", safeSingleLine(r.State), r.Priority)
		if r.AttachmentPath != "" {
			fmt.Fprintf(stdout, "attachment_path: %s\n", safeSingleLine(r.AttachmentPath))
		}
		if r.Assignee != "" {
			fmt.Fprintf(stdout, "assignee:   %s\n", safeSingleLine(r.Assignee))
		}
		if r.Readiness != nil {
			if r.Readiness.Ready {
				fmt.Fprintln(stdout, "readiness=ready")
			} else {
				fmt.Fprintln(stdout, "readiness=blocked")
				for _, blocker := range r.Readiness.Blockers {
					if blocker.ID != "" {
						fmt.Fprintf(stdout, "blocker=%s id=%s: %s\n", safeSingleLine(blocker.Code), safeSingleLine(blocker.ID), safeMultiline(blocker.Message))
					} else {
						fmt.Fprintf(stdout, "blocker=%s: %s\n", safeSingleLine(blocker.Code), safeMultiline(blocker.Message))
					}
				}
			}
		}
		fmt.Fprintln(stdout)
		if r.Body != "" {
			fmt.Fprint(stdout, safeMultiline(r.Body))
		} else {
			for _, key := range []string{"objective", "acceptance", "handoff", "work_log", "outcome"} {
				text, ok := r.Sections[key]
				if !ok {
					continue
				}
				fmt.Fprintf(stdout, "## %s\n\n", titleCaseSection(key))
				content := strings.TrimRight(safeMultiline(text.Text), "\r\n")
				if content != "" {
					fmt.Fprintln(stdout, content)
				}
				if text.Truncated {
					fmt.Fprintln(stdout, "[truncated]")
				}
				fmt.Fprintln(stdout, "")
			}
			if r.Truncated {
				fmt.Fprintln(stdout, "truncated=true")
			}
		}
	case map[string]string:
		fmt.Fprintf(stdout, "%s %s\n", safeSingleLine(r["id"]), safeSingleLine(r["path"]))
	case *repositoryInfo:
		renderRepositoryInfoHuman(&commandContext{stdout: stdout}, r)
	case *domain.UpdateResult:
		if r.Changed {
			fmt.Fprintf(stdout, "updated %s (changed: %s)\n", safeSingleLine(r.ID), safeSingleLine(strings.Join(r.ChangedFields, ", ")))
		} else {
			fmt.Fprintf(stdout, "no changes to %s\n", safeSingleLine(r.ID))
		}
	case *domain.ClaimResult:
		if r.Changed {
			fmt.Fprintf(stdout, "claimed %s (assignee=%s)\n", safeSingleLine(r.ID), safeSingleLine(r.Assignee))
		} else {
			fmt.Fprintf(stdout, "already claimed %s (assignee=%s)\n", safeSingleLine(r.ID), safeSingleLine(r.Assignee))
		}
	case *domain.ReleaseResult:
		if r.Changed {
			fmt.Fprintf(stdout, "released %s\n", safeSingleLine(r.ID))
		} else {
			fmt.Fprintf(stdout, "already released %s\n", safeSingleLine(r.ID))
		}
	case *domain.ReassignResult:
		if r.Changed {
			fmt.Fprintf(stdout, "reassigned %s (assignee=%s)\n", safeSingleLine(r.ID), safeSingleLine(r.Assignee))
		} else {
			fmt.Fprintf(stdout, "already assigned %s (assignee=%s)\n", safeSingleLine(r.ID), safeSingleLine(r.Assignee))
		}
	case *domain.TransitionResult:
		renderTransition(stdout, r)
	case *domain.BatchTransitionResult:
		for i := range r.Items {
			renderTransition(stdout, &r.Items[i])
		}
	case *domain.BatchArchiveResult:
		for _, item := range r.Items {
			fmt.Fprintf(stdout, "%s: archived\n", safeSingleLine(item.ID))
		}
	default:
		fmt.Fprintf(stdout, "ok (%s)\n", cmd)
	}
	return nil
}

func renderTransition(stdout *bytes.Buffer, r *domain.TransitionResult) {
	if !r.Changed {
		fmt.Fprintf(stdout, "%s: %s unchanged\n", safeSingleLine(r.ID), safeSingleLine(r.State))
		return
	}
	fmt.Fprintf(stdout, "%s: %s -> %s", safeSingleLine(r.ID), safeSingleLine(r.FromState), safeSingleLine(r.State))
	if r.Assignee != "" {
		fmt.Fprintf(stdout, ", claimed by %s", safeSingleLine(r.Assignee))
	}
	fmt.Fprintln(stdout)
}

func truncateHuman(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

func formatDependencies(dependencies []domain.DependencySummary, width int) string {
	if len(dependencies) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(dependencies))
	for index, dependency := range dependencies {
		id := dependency.ID
		if len(id) > 5 {
			id = id[len(id)-5:]
		}
		if dependency.Missing {
			id += "(missing)"
		} else if dependency.Satisfied {
			id += "✓"
		}
		if dependency.Archived {
			id += "(arch)"
		}
		candidate := strings.Join(append(append([]string{}, parts...), id), ", ")
		if len([]rune(candidate)) > width {
			return strings.Join(parts, ", ") + fmt.Sprintf(", +%d", len(dependencies)-index)
		}
		parts = append(parts, id)
	}
	return strings.Join(parts, ", ")
}

func decoratorCommand(cmd string) bool {
	switch cmd {
	case "list", "ready", "next", "status", "show":
		return true
	default:
		return false
	}
}

func runMarkdownDecorator(stdout *bytes.Buffer, executable string, markdown []byte) error {
	args, err := splitCommandLine(executable)
	if err != nil {
		return contract.NewError(contract.ErrInvalidArgument,
			fmt.Sprintf("Invalid Markdown decorator %q: %v.", executable, err), nil)
	}
	command := exec.Command(args[0], args[1:]...)
	command.Stdin = bytes.NewReader(markdown)
	command.Stdout = stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return contract.NewError(contract.ErrIOError,
			fmt.Sprintf("Markdown decorator %q failed: %v.", executable, err), nil)
	}
	return nil
}

// splitCommandLine separates an executable or shell line into arguments
// without a shell. Quotes group arguments; backslashes remain unchanged so
// Windows paths survive parsing.
func splitCommandLine(command string) ([]string, error) {
	var parts []string
	var current strings.Builder
	var quote rune
	token := false
	for _, r := range command {
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			token = true
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			token = true
		case ' ', '\t', '\r', '\n':
			if token {
				parts = append(parts, current.String())
				current.Reset()
				token = false
			}
		default:
			current.WriteRune(r)
			token = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if token {
		parts = append(parts, current.String())
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("command is empty")
	}
	return parts, nil
}

func renderMarkdownTo(stdout *bytes.Buffer, cmd string, res any) error {
	switch r := res.(type) {
	case *domain.ListResult:
		if r.ShowDependencies {
			fmt.Fprintln(stdout, "| State | Pri | ID | Title | Assignee | Blocked by |")
			fmt.Fprintln(stdout, "|---|---:|---|---|---|---|")
		} else {
			fmt.Fprintln(stdout, "| State | Pri | ID | Title | Assignee |")
			fmt.Fprintln(stdout, "|---|---:|---|---|---|")
		}
		for _, it := range r.Items {
			if r.ShowDependencies {
				fmt.Fprintf(stdout, "| %s | %s | %s | %s | %s | %s |\n",
					markdownCell(safeSingleLine(pointerString(it.State))), markdownCell(safeSingleLine(pointerPriority(it.Priority))),
					markdownCell(safeSingleLine(it.ID)), markdownCell(safeSingleLine(pointerString(it.Title))), markdownCell(safeSingleLine(pointerAssignee(it.Assignee))),
					markdownCell(formatDependencies(it.Dependencies, 30)))
			} else {
				fmt.Fprintf(stdout, "| %s | %s | %s | %s | %s |\n",
					markdownCell(safeSingleLine(pointerString(it.State))), markdownCell(safeSingleLine(pointerPriority(it.Priority))),
					markdownCell(safeSingleLine(it.ID)), markdownCell(safeSingleLine(pointerString(it.Title))), markdownCell(safeSingleLine(pointerAssignee(it.Assignee))))
			}
		}
		if r.More {
			fmt.Fprintln(stdout, "\n_more: true_")
		}
	case *domain.NextResult:
		if r.Item == nil {
			fmt.Fprintln(stdout, "No eligible work.")
			return nil
		}
		fmt.Fprintln(stdout, "| Pri | ID | Title | Assignee |")
		fmt.Fprintln(stdout, "|---:|---|---|---|")
		fmt.Fprintf(stdout, "| %s | %s | %s | %s |\n", markdownCell(safeSingleLine(pointerPriority(r.Item.Priority))),
			markdownCell(safeSingleLine(r.Item.ID)), markdownCell(safeSingleLine(pointerString(r.Item.Title))), markdownCell(safeSingleLine(pointerAssignee(r.Item.Assignee))))
	case *domain.StatusView:
		fmt.Fprintf(stdout, "## %s\n\n", safeSingleLine(r.ID))
		fmt.Fprintf(stdout, "- Title: %s\n- State: %s\n- Priority: P%d\n", markdownCell(safeSingleLine(r.Title)), safeSingleLine(r.State), r.Priority)
		if r.Assignee != "" {
			fmt.Fprintf(stdout, "- Assignee: %s\n", markdownCell(safeSingleLine(r.Assignee)))
		}
		if r.Created != "" {
			fmt.Fprintf(stdout, "- Created: %s\n", safeSingleLine(r.Created))
		}
		if r.Modified != "" {
			fmt.Fprintf(stdout, "- Modified: %s\n", safeSingleLine(r.Modified))
		}
		if len(r.Blockers) > 0 {
			fmt.Fprintln(stdout, "\n### Blockers")
			for _, blocker := range r.Blockers {
				if blocker.ID != "" {
					fmt.Fprintf(stdout, "- `%s` `%s`: %s\n", safeSingleLine(blocker.Code), safeSingleLine(blocker.ID), markdownCell(safeMultiline(blocker.Message)))
				} else {
					fmt.Fprintf(stdout, "- `%s`: %s\n", safeSingleLine(blocker.Code), markdownCell(safeMultiline(blocker.Message)))
				}
			}
		}
	case *domain.ShowView:
		if r.AttachmentPath != "" {
			fmt.Fprintf(stdout, "Attachment path: `%s`\n\n", markdownCell(safeSingleLine(r.AttachmentPath)))
		}
		if r.Body != "" {
			fmt.Fprint(stdout, safeMultiline(r.Body))
			if !strings.HasSuffix(r.Body, "\n") {
				fmt.Fprintln(stdout)
			}
		} else {
			fmt.Fprintf(stdout, "## %s\n\n", safeSingleLine(r.Title))
			for _, key := range []string{"objective", "acceptance", "handoff", "work_log", "outcome"} {
				section, ok := r.Sections[key]
				if !ok {
					continue
				}
				fmt.Fprintf(stdout, "### %s\n\n%s", titleCaseSection(key), safeMultiline(section.Text))
				if section.Text == "" || !strings.HasSuffix(section.Text, "\n") {
					fmt.Fprintln(stdout)
				}
			}
		}
		if r.Readiness != nil {
			fmt.Fprintf(stdout, "\n### Readiness\n\n- Ready: %t\n", r.Readiness.Ready)
			for _, blocker := range r.Readiness.Blockers {
				if blocker.ID != "" {
					fmt.Fprintf(stdout, "- `%s` `%s`: %s\n", safeSingleLine(blocker.Code), safeSingleLine(blocker.ID), markdownCell(safeMultiline(blocker.Message)))
				} else {
					fmt.Fprintf(stdout, "- `%s`: %s\n", safeSingleLine(blocker.Code), markdownCell(safeMultiline(blocker.Message)))
				}
			}
		}
	default:
		return renderPlainHumanTo(stdout, cmd, res)
	}
	return nil
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func pointerPriority(value *int) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("P%d", *value)
}

func pointerAssignee(value *string) string {
	if value == nil {
		return ""
	}
	return "@" + *value
}

func markdownCell(value string) string {
	value = safeSingleLine(value)
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", "")
	return strings.ReplaceAll(value, "\n", " ")
}

func titleCaseSection(key string) string {
	key = strings.ReplaceAll(key, "_", " ")
	if key == "" {
		return key
	}
	return strings.ToUpper(key[:1]) + key[1:]
}

func renderHumanStatus(stdout *bytes.Buffer, r *domain.StatusView) {
	fmt.Fprintf(stdout, "%s  %s\n\n", safeSingleLine(r.ID), safeSingleLine(r.Title))
	fmt.Fprintf(stdout, "state:      %s\n", safeSingleLine(r.State))
	if r.Archived {
		fmt.Fprintln(stdout, "archived: true")
	}
	fmt.Fprintf(stdout, "priority:   P%d\n", r.Priority)
	if r.Objective != "" {
		fmt.Fprintf(stdout, "objective:  %s\n", safeMultiline(r.Objective))
	}
	if r.Assignee != "" {
		fmt.Fprintf(stdout, "assignee:   %s\n", safeSingleLine(r.Assignee))
	}
	if r.Created != "" {
		fmt.Fprintf(stdout, "created:    %s\n", safeSingleLine(r.Created))
	}
	if r.Modified != "" {
		modified := r.Modified
		if parsed, err := time.Parse(time.RFC3339, r.Modified); err == nil {
			modified = parsed.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(stdout, "modified:   %s\n", safeSingleLine(modified))
	}
	if r.State == "review" {
		fmt.Fprintln(stdout, "workflow:   awaiting review")
		return
	}
	if r.State == "signoff" {
		fmt.Fprintln(stdout, "workflow:   awaiting human acceptance")
		return
	}
	if r.State == "hold" {
		fmt.Fprintln(stdout, "workflow:   paused")
		return
	}
	if len(r.Blockers) == 0 {
		return
	}
	fmt.Fprintln(stdout, "\nblocked by:")
	for _, blocker := range r.Blockers {
		if blocker.ID != "" {
			fmt.Fprintf(stdout, "  %s %s  %s\n", safeSingleLine(blocker.Code), safeSingleLine(blocker.ID), safeMultiline(blocker.Message))
		} else {
			fmt.Fprintf(stdout, "  %s  %s\n", safeSingleLine(blocker.Code), safeMultiline(blocker.Message))
		}
	}
}
