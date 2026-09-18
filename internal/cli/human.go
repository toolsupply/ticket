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

	"ticket/internal/contract"
	"ticket/internal/domain"
)

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
		fmt.Fprintf(stdout, "%s  %s  %s\n", r.ID, r.State, r.Title)
	case *domain.CreateResult:
		fmt.Fprintf(stdout, "created %s %s\n", r.ID, r.Path)
		if r.State != "" {
			fmt.Fprintf(stdout, "state:      %s\npriority:   P%d\n", r.State, r.Priority)
			if r.Objective != "" {
				fmt.Fprintf(stdout, "objective:  %s\n", r.Objective)
			}
		}
	case *domain.DeleteResult:
		fmt.Fprintf(stdout, "deleted %s\n", r.ID)
	case *domain.BatchDeleteResult:
		fmt.Fprintf(stdout, "deleted %d tickets\n", len(r.Items))
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
		fmt.Fprintf(stdout, "%s  %s  %s\n", priority, r.Item.ID, title)
	case *EditResult:
		if r.Changed {
			fmt.Fprintf(stdout, "edited %s\n", r.ID)
		} else {
			fmt.Fprintf(stdout, "unchanged %s\n", r.ID)
		}
	case *domain.ListResult:
		fmt.Fprintf(stdout, "%-10s %-4s %-14s %-42s %s\n", "STATE", "PRI", "ID", "TITLE", "ASSIGNEE")
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
			fmt.Fprintf(stdout, "%-10s %-4s %-14s %-42s %s\n",
				truncateHuman(state, 10), truncateHuman(pri, 4), it.ID,
				truncateHuman(title, 42), truncateHuman(assignee, 20))
		}
	case *domain.StatusView:
		renderHumanStatus(stdout, r)
	case *domain.ShowView:
		fmt.Fprintf(stdout, "%s %s\n\n", r.ID, r.Title)
		fmt.Fprintf(stdout, "state:      %s\npriority:   P%d\n", r.State, r.Priority)
		if r.AttachmentPath != "" {
			fmt.Fprintf(stdout, "attachment_path: %s\n", r.AttachmentPath)
		}
		if r.Assignee != "" {
			fmt.Fprintf(stdout, "assignee:   %s\n", r.Assignee)
		}
		if r.Readiness != nil {
			if r.Readiness.Ready {
				fmt.Fprintln(stdout, "readiness=ready")
			} else {
				fmt.Fprintln(stdout, "readiness=blocked")
				for _, blocker := range r.Readiness.Blockers {
					if blocker.ID != "" {
						fmt.Fprintf(stdout, "blocker=%s id=%s: %s\n", blocker.Code, blocker.ID, blocker.Message)
					} else {
						fmt.Fprintf(stdout, "blocker=%s: %s\n", blocker.Code, blocker.Message)
					}
				}
			}
		}
		fmt.Fprintln(stdout)
		if r.Body != "" {
			fmt.Fprint(stdout, r.Body)
		} else {
			for _, key := range []string{"objective", "acceptance", "handoff", "work_log", "outcome"} {
				text, ok := r.Sections[key]
				if !ok {
					continue
				}
				fmt.Fprintf(stdout, "## %s\n\n", titleCaseSection(key))
				content := strings.TrimRight(text.Text, "\r\n")
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
		fmt.Fprintf(stdout, "%s %s\n", r["id"], r["path"])
	case *domain.UpdateResult:
		if r.Changed {
			fmt.Fprintf(stdout, "updated %s (changed: %s)\n", r.ID, strings.Join(r.ChangedFields, ", "))
		} else {
			fmt.Fprintf(stdout, "no changes to %s\n", r.ID)
		}
	case *domain.ClaimResult:
		if r.Changed {
			fmt.Fprintf(stdout, "claimed %s (assignee=%s)\n", r.ID, r.Assignee)
		} else {
			fmt.Fprintf(stdout, "already claimed %s (assignee=%s)\n", r.ID, r.Assignee)
		}
	case *domain.ReleaseResult:
		if r.Changed {
			fmt.Fprintf(stdout, "released %s\n", r.ID)
		} else {
			fmt.Fprintf(stdout, "already released %s\n", r.ID)
		}
	case *domain.TransitionResult:
		renderTransition(stdout, r)
	case *domain.BatchTransitionResult:
		for i := range r.Items {
			renderTransition(stdout, &r.Items[i])
		}
	default:
		fmt.Fprintf(stdout, "ok (%s)\n", cmd)
	}
	return nil
}

func renderTransition(stdout *bytes.Buffer, r *domain.TransitionResult) {
	if !r.Changed {
		fmt.Fprintf(stdout, "%s: %s unchanged\n", r.ID, r.State)
		return
	}
	fmt.Fprintf(stdout, "%s: %s -> %s", r.ID, r.FromState, r.State)
	if r.Assignee != "" {
		fmt.Fprintf(stdout, ", claimed by %s", r.Assignee)
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

func decoratorCommand(cmd string) bool {
	switch cmd {
	case "list", "ready", "next", "status", "show":
		return true
	default:
		return false
	}
}

func runMarkdownDecorator(stdout *bytes.Buffer, executable string, markdown []byte) error {
	args, err := splitDecoratorCommand(executable)
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

// splitDecoratorCommand separates an executable and its arguments without a
// shell. Quotes group arguments; backslashes remain unchanged so Windows
// paths survive parsing.
func splitDecoratorCommand(command string) ([]string, error) {
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
		fmt.Fprintln(stdout, "| State | Pri | ID | Title | Assignee |")
		fmt.Fprintln(stdout, "|---|---:|---|---|---|")
		for _, it := range r.Items {
			fmt.Fprintf(stdout, "| %s | %s | %s | %s | %s |\n",
				markdownCell(pointerString(it.State)), markdownCell(pointerPriority(it.Priority)),
				markdownCell(it.ID), markdownCell(pointerString(it.Title)), markdownCell(pointerAssignee(it.Assignee)))
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
		fmt.Fprintf(stdout, "| %s | %s | %s | %s |\n", markdownCell(pointerPriority(r.Item.Priority)),
			markdownCell(r.Item.ID), markdownCell(pointerString(r.Item.Title)), markdownCell(pointerAssignee(r.Item.Assignee)))
	case *domain.StatusView:
		fmt.Fprintf(stdout, "## %s\n\n", r.ID)
		fmt.Fprintf(stdout, "- Title: %s\n- State: %s\n- Priority: P%d\n", markdownCell(r.Title), r.State, r.Priority)
		if r.Assignee != "" {
			fmt.Fprintf(stdout, "- Assignee: %s\n", markdownCell(r.Assignee))
		}
		if r.Created != "" {
			fmt.Fprintf(stdout, "- Created: %s\n", r.Created)
		}
		if r.Modified != "" {
			fmt.Fprintf(stdout, "- Modified: %s\n", r.Modified)
		}
		if len(r.Blockers) > 0 {
			fmt.Fprintln(stdout, "\n### Blockers")
			for _, blocker := range r.Blockers {
				if blocker.ID != "" {
					fmt.Fprintf(stdout, "- `%s` `%s`: %s\n", blocker.Code, blocker.ID, markdownCell(blocker.Message))
				} else {
					fmt.Fprintf(stdout, "- `%s`: %s\n", blocker.Code, markdownCell(blocker.Message))
				}
			}
		}
	case *domain.ShowView:
		if r.AttachmentPath != "" {
			fmt.Fprintf(stdout, "Attachment path: `%s`\n\n", markdownCell(r.AttachmentPath))
		}
		if r.Body != "" {
			fmt.Fprint(stdout, r.Body)
			if !strings.HasSuffix(r.Body, "\n") {
				fmt.Fprintln(stdout)
			}
		} else {
			fmt.Fprintf(stdout, "## %s\n\n", r.Title)
			for _, key := range []string{"objective", "acceptance", "handoff", "work_log", "outcome"} {
				section, ok := r.Sections[key]
				if !ok {
					continue
				}
				fmt.Fprintf(stdout, "### %s\n\n%s", titleCaseSection(key), section.Text)
				if section.Text == "" || !strings.HasSuffix(section.Text, "\n") {
					fmt.Fprintln(stdout)
				}
			}
		}
		if r.Readiness != nil {
			fmt.Fprintf(stdout, "\n### Readiness\n\n- Ready: %t\n", r.Readiness.Ready)
			for _, blocker := range r.Readiness.Blockers {
				if blocker.ID != "" {
					fmt.Fprintf(stdout, "- `%s` `%s`: %s\n", blocker.Code, blocker.ID, markdownCell(blocker.Message))
				} else {
					fmt.Fprintf(stdout, "- `%s`: %s\n", blocker.Code, markdownCell(blocker.Message))
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
	fmt.Fprintf(stdout, "%s  %s\n\n", r.ID, r.Title)
	fmt.Fprintf(stdout, "state:      %s\n", r.State)
	fmt.Fprintf(stdout, "priority:   P%d\n", r.Priority)
	if r.Objective != "" {
		fmt.Fprintf(stdout, "objective:  %s\n", r.Objective)
	}
	if r.Assignee != "" {
		fmt.Fprintf(stdout, "assignee:   %s\n", r.Assignee)
	}
	if r.Created != "" {
		fmt.Fprintf(stdout, "created:    %s\n", r.Created)
	}
	if r.Modified != "" {
		modified := r.Modified
		if parsed, err := time.Parse(time.RFC3339, r.Modified); err == nil {
			modified = parsed.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(stdout, "modified:   %s\n", modified)
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
			fmt.Fprintf(stdout, "  %s %s  %s\n", blocker.Code, blocker.ID, blocker.Message)
		} else {
			fmt.Fprintf(stdout, "  %s  %s\n", blocker.Code, blocker.Message)
		}
	}
}
