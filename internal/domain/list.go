// List operation and complete ticket reads.
package domain

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ticket/internal/contract"
	"ticket/internal/identity"
	"ticket/internal/store"
)

const (
	DefaultLimit = 20
)

type ListOptions struct {
	IDs               []string
	State             string
	States            []string
	Tags, WithoutTags []string
	Assignee, Parent  string
	Unassigned        bool
	Priority          *int
	Limit             int
	LimitSet          bool
	Offset            int
	NonterminalOnly   bool
	Unlimited         bool
	Fields            []string
	Sort              string
}

type Summary struct {
	ID        string   `json:"id"`
	Title     *string  `json:"title,omitempty"`
	State     *string  `json:"state,omitempty"`
	Priority  *int     `json:"priority,omitempty"`
	Assignee  *string  `json:"assignee,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Parent    *string  `json:"parent,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Path      *string  `json:"path,omitempty"`
}

type ListResult struct {
	Items []Summary `json:"items"`
	More  bool      `json:"more"`
}

func List(st *store.Store, opts ListOptions) (*ListResult, error) {
	if opts.LimitSet && opts.Limit <= 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Limit must be greater than zero.", nil)
	}
	if opts.Offset < 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Offset must not be negative.", nil)
	}
	if opts.Limit <= 0 && !opts.Unlimited {
		opts.Limit = DefaultLimit
	}
	if opts.State != "" {
		opts.States = append(opts.States, opts.State)
		opts.State = ""
	}
	if len(opts.States) == 0 {
		if len(opts.IDs) > 0 {
			opts.States = []string{"all"}
		} else {
			opts.States = []string{"open"}
		}
	}
	for _, state := range opts.States {
		if state != "open" && state != "hold" && state != "review" && state != "signoff" && state != "completed" && state != "rejected" && state != "all" {
			return nil, contract.NewError(contract.ErrInvalidArgument, "State must be open, hold, review, signoff, completed, rejected, or all.", nil)
		}
	}
	for _, state := range opts.States {
		if state == "all" && len(opts.States) != 1 {
			return nil, contract.NewError(contract.ErrInvalidArgument, "The all state cannot be combined with other states.", nil)
		}
	}
	if opts.Assignee != "" && opts.Unassigned {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Assignee and unassigned conflict.", nil)
	}
	if opts.Priority != nil && (*opts.Priority < 0 || *opts.Priority > 4) {
		return nil, contract.NewError(contract.ErrInvalidArgument, "Priority must be an integer 0-4.", nil)
	}
	var err error
	if opts.Tags, err = normalizeTags(opts.Tags); err != nil {
		return nil, err
	}
	if opts.WithoutTags, err = normalizeTags(opts.WithoutTags); err != nil {
		return nil, err
	}
	fields := opts.Fields
	if len(fields) == 0 {
		fields = []string{"id", "title", "state", "priority", "assignee"}
	}
	if fields[0] != "id" {
		fields = append([]string{"id"}, fields...)
	}
	for _, field := range fields {
		if !isListField(field) {
			return nil, contract.NewError(contract.ErrInvalidArgument, "Invalid field "+field+" in fields.", nil)
		}
	}
	parent := opts.Parent
	if parent != "" {
		full, err := st.ResolveID(parent, true)
		if err != nil {
			return nil, err
		}
		parent = full
	}
	requested := make([]string, 0, len(opts.IDs))
	for _, id := range opts.IDs {
		if _, _, ok := store.ParseID(id); !ok && !identity.ValidShorthand(id) {
			return nil, contract.NewError(contract.ErrInvalidArgument,
				"Reference is not a valid ticket ID.", map[string]any{"id": id})
		}
		requested = append(requested, id)
	}
	tickets, diags, err := scanWith(st)
	if err != nil {
		return nil, err
	}
	if len(diags) > 0 {
		return nil, collectionError(diags)
	}
	var matches []*Ticket
	for _, ticket := range tickets {
		if len(requested) > 0 && !matchesIDPrefix(ticket.ID, requested) {
			continue
		}
		if matchesFilter(ticket, opts, parent) {
			matches = append(matches, ticket)
		}
	}
	switch opts.Sort {
	case "":
		sortTickets(matches)
	case "id_desc":
		sort.Slice(matches, func(i, j int) bool { return matches[i].ID > matches[j].ID })
	case "modified_desc":
		modified := make(map[string]time.Time, len(matches))
		for _, ticket := range matches {
			info, err := os.Stat(filepath.Join(st.Root, ticket.TaskRelPath))
			if err != nil {
				return nil, wrapStoreError("ticket file", err)
			}
			modified[ticket.ID] = info.ModTime()
		}
		sort.Slice(matches, func(i, j int) bool {
			if !modified[matches[i].ID].Equal(modified[matches[j].ID]) {
				return modified[matches[i].ID].After(modified[matches[j].ID])
			}
			return matches[i].ID > matches[j].ID
		})
	default:
		return nil, contract.NewError(contract.ErrInvalidArgument, "Invalid list sort.", nil)
	}
	if opts.Offset >= len(matches) {
		matches = nil
	} else if opts.Offset > 0 {
		matches = matches[opts.Offset:]
	}
	more := false
	if !opts.Unlimited {
		more = len(matches) > opts.Limit
		if more {
			matches = matches[:opts.Limit]
		}
	}
	result := &ListResult{Items: make([]Summary, 0, len(matches)), More: more}
	for _, ticket := range matches {
		result.Items = append(result.Items, project(ticket, fields))
	}
	return result, nil
}

func matchesIDPrefix(id string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

func isListField(field string) bool {
	switch field {
	case "id", "title", "state", "priority", "assignee", "tags", "parent", "depends_on", "path":
		return true
	}
	return false
}

func project(ticket *Ticket, fields []string) Summary {
	set := make(map[string]bool, len(fields))
	for _, field := range fields {
		set[field] = true
	}
	s := Summary{ID: ticket.ID}
	if set["title"] {
		s.Title = &ticket.Title
	}
	if set["state"] {
		s.State = &ticket.State
	}
	if set["priority"] {
		s.Priority = &ticket.Priority
	}
	if set["assignee"] && ticket.Assignee != "" {
		s.Assignee = &ticket.Assignee
	}
	if set["tags"] && len(ticket.Tags) > 0 {
		s.Tags = ticket.Tags
	}
	if set["parent"] && ticket.Parent != "" {
		s.Parent = &ticket.Parent
	}
	if set["depends_on"] && len(ticket.DependsOn) > 0 {
		s.DependsOn = ticket.DependsOn
	}
	if set["path"] {
		s.Path = &ticket.TaskRelPath
	}
	return s
}

func matchesFilter(ticket *Ticket, opts ListOptions, parent string) bool {
	states := opts.States
	if len(states) == 0 {
		if opts.State == "" {
			states = []string{"open"}
		} else {
			states = []string{opts.State}
		}
	}
	matchedState := false
	for _, state := range states {
		if state == "all" || ticket.State == state {
			matchedState = true
			break
		}
	}
	if !matchedState {
		return false
	}
	if opts.NonterminalOnly && ticket.IsTerminal() {
		return false
	}
	have := map[string]bool{}
	for _, tag := range ticket.Tags {
		have[tag] = true
	}
	for _, tag := range opts.Tags {
		if !have[tag] {
			return false
		}
	}
	for _, tag := range opts.WithoutTags {
		if have[tag] {
			return false
		}
	}
	if opts.Assignee != "" && ticket.Assignee != opts.Assignee {
		return false
	}
	if opts.Unassigned && ticket.Assignee != "" {
		return false
	}
	if parent != "" && ticket.Parent != parent {
		return false
	}
	if opts.Priority != nil && ticket.Priority != *opts.Priority {
		return false
	}
	return true
}

func sortTickets(tickets []*Ticket) {
	sort.Slice(tickets, func(i, j int) bool {
		a, b := tickets[i], tickets[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	})
}
func scanWith(st *store.Store) ([]*Ticket, []DiagnosticSummary, error) {
	entries, err := os.ReadDir(st.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, wrapStoreError("ticket root", err)
	}
	var tickets []*Ticket
	var diags []DiagnosticSummary
	for _, entry := range entries {
		name := entry.Name()
		if name == ".local" || entry.Type().IsRegular() {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			diags = append(diags, DiagnosticSummary{Path: name, Message: "ticket directory must not be a symlink"})
			continue
		}
		if !entry.IsDir() {
			continue
		}
		if !identity.ValidID(name) && looksLikeTicketID(name) {
			diags = append(diags, DiagnosticSummary{Path: name, Message: "ticket directory name is not a canonical ticket ID"})
			continue
		}
		if !identity.ValidID(name) {
			continue
		}
		dir := filepath.Join(st.Root, name)
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			diags = append(diags, DiagnosticSummary{Path: name, Message: "ticket path is not a directory"})
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			diags = append(diags, DiagnosticSummary{Path: name, Message: "ticket directory must not be a symlink"})
			continue
		}
		ticket, err := ReadTicket(st, name)
		if err != nil {
			if ce, ok := err.(*contract.Error); ok {
				d := DiagnosticSummary{Path: name, Message: ce.Message}
				if ce.Details != nil {
					if values, ok := ce.Details["diagnostics"].([]map[string]any); ok {
						d.Diagnostics = values
					}
				}
				diags = append(diags, d)
			} else {
				diags = append(diags, DiagnosticSummary{Path: name, Message: err.Error()})
			}
			continue
		}
		tickets = append(tickets, ticket)
	}
	return tickets, diags, nil
}

func looksLikeTicketID(name string) bool {
	if len(name) < 9 {
		return false
	}
	for i := 0; i < 8; i++ {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}
	return name[8] == '-'
}

type DiagnosticSummary struct {
	Path        string
	Message     string
	Diagnostics []map[string]any
}

func collectionError(diags []DiagnosticSummary) *contract.Error {
	summaries := make([]map[string]any, 0, 20)
	total := 0
	for _, d := range diags {
		n := len(d.Diagnostics)
		if n == 0 {
			n = 1
		}
		total += n
		for _, item := range d.Diagnostics {
			if len(summaries) >= 20 {
				break
			}
			copyItem := map[string]any{}
			for key, value := range item {
				copyItem[key] = value
			}
			if copyItem["path"] == nil {
				copyItem["path"] = d.Path
			}
			summaries = append(summaries, copyItem)
		}
		if len(d.Diagnostics) == 0 && len(summaries) < 20 {
			summaries = append(summaries, map[string]any{"severity": "error", "code": "invalid_ticket", "path": d.Path, "message": d.Message})
		}
		if len(summaries) >= 20 {
			break
		}
	}
	details := map[string]any{"diagnostics": summaries}
	if total > 20 {
		details["diagnostics_truncated"] = true
	}
	return contract.NewError(contract.ErrInvalidTicket, "Collection scan failed on malformed ticket metadata.", details)
}

func ReadTicket(st *store.Store, id string) (*Ticket, error) {
	data, err := readTaskFile(st, id)
	if err != nil {
		return nil, err
	}
	return ParseTicketFile(id, data)
}
func taskPath(st *store.Store, id string) (string, error) {
	if _, _, ok := store.ParseID(id); !ok {
		return "", contract.NewError(contract.ErrInvalidArgument, "Reference is not a valid ticket ID.", map[string]any{"id": id})
	}
	dir := filepath.Join(st.Root, id)
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", contract.NewError(contract.ErrNotFound, "Ticket not found.", map[string]any{"id": id})
		}
		return "", contract.NewError(contract.ErrIOError, "Ticket lookup failed: "+err.Error(), map[string]any{"id": id})
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", contract.NewError(contract.ErrInvalidRepository, "Ticket directory must not be a symlink.", map[string]any{"id": id})
	}
	if !info.IsDir() {
		return "", contract.NewError(contract.ErrInvalidRepository, "Ticket path is not a directory.", map[string]any{"id": id})
	}
	task := filepath.Join(dir, "TASK.md")
	taskInfo, err := os.Lstat(task)
	if err != nil {
		if os.IsNotExist(err) {
			return "", contract.NewError(contract.ErrInvalidRepository, "TASK.md is missing.", map[string]any{"id": id})
		}
		return "", contract.NewError(contract.ErrIOError, "TASK.md lookup failed: "+err.Error(), map[string]any{"id": id})
	}
	if taskInfo.Mode()&os.ModeSymlink != 0 {
		return "", contract.NewError(contract.ErrInvalidRepository, "TASK.md must not be a symlink.", map[string]any{"id": id})
	}
	if !taskInfo.Mode().IsRegular() {
		return "", contract.NewError(contract.ErrInvalidRepository, "TASK.md is not a regular file.", map[string]any{"id": id})
	}
	return task, nil
}

func readTaskFile(st *store.Store, id string) ([]byte, error) {
	task, err := taskPath(st, id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(task)
	if err != nil {
		return nil, contract.NewError(contract.ErrIOError, "TASK.md read failed: "+err.Error(), map[string]any{"id": id})
	}
	return data, nil
}

func wrapStoreError(what string, err error) error {
	if os.IsNotExist(err) {
		return contract.NewError(contract.ErrRepoNotFound, what+" not found.", nil)
	}
	return contract.NewError(contract.ErrIOError, what+": "+err.Error(), nil)
}
