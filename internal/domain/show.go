// Show operation: focused ticket view with section budgets, and the
// path operation.
package domain

import (
	"path/filepath"
	"time"

	"ticket/internal/contract"
	"ticket/internal/identity"
	"ticket/internal/markdown"
	"ticket/internal/store"
)

// DefaultSectionBudget is the default budget for returned section text.
const DefaultSectionBudget = 16 << 10 // 16 KiB

// MaxBudget is the maximum accepted budget.
const MaxBudget = 1 << 20 // 1 MiB

// ShowOptions are show flags.
type ShowOptions struct {
	Sections  []string // known keys; empty = default selection
	Full      bool
	MaxBytes  int
	Readiness bool
}

// SectionView is one returned section.
type SectionView struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// CustomSectionView exposes a custom section's position and heading.
type CustomSectionView struct {
	Position int    `json:"position"`
	Heading  string `json:"heading"`
}

// Prerequisite is a direct dependency summary.
type Prerequisite struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	State   string `json:"state,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

// ShowView is the show response.
type ShowView struct {
	ID                string                 `json:"id"`
	Path              string                 `json:"path"`
	Title             string                 `json:"title"`
	State             string                 `json:"state"`
	Priority          int                    `json:"priority"`
	Assignee          string                 `json:"assignee,omitempty"`
	BlockedReason     string                 `json:"blocked_reason,omitempty"`
	Parent            string                 `json:"parent,omitempty"`
	Tags              []string               `json:"tags"`
	DependsOn         []string               `json:"depends_on"`
	Sections          map[string]SectionView `json:"sections,omitempty"`
	AvailableSections []string               `json:"available_sections,omitempty"`
	CustomSections    []CustomSectionView    `json:"custom_sections,omitempty"`
	Prerequisites     []Prerequisite         `json:"prerequisites,omitempty"`
	PrerequisitesMore bool                   `json:"prerequisites_more,omitempty"`
	Readiness         *ReadinessView         `json:"readiness,omitempty"`
	Truncated         bool                   `json:"truncated,omitempty"`
	Body              string                 `json:"body,omitempty"`
}

// StatusView is the compact operational view returned by status. Eligibility
// blockers are kept separate from the ticket's lifecycle state and ownership.
type StatusView struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	State     string             `json:"state"`
	Priority  int                `json:"priority"`
	Assignee  string             `json:"assignee,omitempty"`
	Created   string             `json:"created,omitempty"`
	Modified  string             `json:"modified,omitempty"`
	Blockers  []ReadinessBlocker `json:"blockers,omitempty"`
	Objective string             `json:"-"`
}

// Show runs the show operation for a full or unambiguous shorthand ID.
func Show(st *store.Store, ref string, opts ShowOptions) (*ShowView, error) {
	if opts.Full {
		return showFull(st, ref, opts)
	}
	if len(opts.Sections) > 0 {
		seen := map[string]bool{}
		for _, k := range opts.Sections {
			if seen[k] {
				continue
			}
			seen[k] = true
			if !isKnownSection(k) {
				return nil, contract.NewError(contract.ErrInvalidArgument,
					"Unknown section key "+k+". v1 section selection accepts known keys only.", nil)
			}
		}
	}
	full, err := resolveShowID(st, ref)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	stv, err := buildShow(st, t, opts, false)
	return stv, err
}

// Status returns the concise operational view of one ticket. It reuses the
// normal ticket and readiness projections while omitting Markdown content.
func Status(st *store.Store, ref string) (*StatusView, error) {
	full, err := resolveShowID(st, ref)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	modified, err := st.TaskModTime(full)
	if err != nil {
		return nil, err
	}
	status := &StatusView{
		ID: t.ID, Title: t.Title, State: t.State, Priority: t.Priority,
		Created: creationDate(t.ID), Modified: modified.Format(time.RFC3339),
		Objective: objectivePreview(t),
	}
	if t.Assignee != "" {
		status.Assignee = t.Assignee
	}
	if t.State == "open" {
		eligibility, err := Readiness(st, full)
		if err != nil {
			return nil, err
		}
		for _, blocker := range eligibility.Blockers {
			if blocker.Code != "assigned" && blocker.Code != "state_blocked" {
				status.Blockers = append(status.Blockers, blocker)
			}
		}
	}
	return status, nil
}

func creationDate(id string) string {
	if !identity.ValidID(id) {
		return ""
	}
	date, err := time.Parse("20060102", id[:8])
	if err != nil {
		return ""
	}
	return date.Format("2006-01-02")
}

func isKnownSection(key string) bool {
	for _, ks := range markdown.KnownSections {
		if ks.Key == key {
			return true
		}
	}
	return false
}

// resolveShowID accepts a full ID or a unique shorthand.
func resolveShowID(st *store.Store, ref string) (string, error) {
	if _, _, ok := store.ParseID(ref); ok {
		return ref, nil
	}
	if len(ref) < identity.TimestampIDLen {
		return st.ResolveID(ref, false)
	}
	return "", contract.NewError(contract.ErrInvalidArgument, "Not a valid ticket ID.", map[string]any{"id": ref})
}

func buildShow(st *store.Store, t *Ticket, opts ShowOptions, full bool) (*ShowView, error) {
	v := &ShowView{
		ID: t.ID, Path: t.TaskRelPath,
		Title: t.Title, State: t.State, Priority: t.Priority,
		Tags: t.Tags, DependsOn: t.DependsOn,
	}
	if t.Tags == nil {
		v.Tags = []string{}
	}
	if t.DependsOn == nil {
		v.DependsOn = []string{}
	}
	switch {
	case t.State == "open":
		if t.Assignee != "" {
			v.Assignee = t.Assignee
		}
		if t.BlockedReason != "" {
			v.BlockedReason = t.BlockedReason
		}
		if t.Parent != "" {
			v.Parent = t.Parent
		}
	default:
		if t.Parent != "" {
			v.Parent = t.Parent
		}
	}
	if full {
		v.Body = string(t.Body)
		if opts.Readiness {
			readiness, err := Readiness(st, t.ID)
			if err != nil {
				return nil, err
			}
			v.Readiness = readiness
		}
		return v, nil
	}
	// Section selection.
	selection := opts.Sections
	if len(selection) == 0 {
		selection = []string{"objective", "acceptance", "handoff", "work_log"}
		if t.IsTerminal() {
			selection = append(selection, "outcome")
		}
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultSectionBudget
	}
	remaining := maxBytes
	v.Sections = map[string]SectionView{}
	v.AvailableSections = []string{}
	for _, key := range selection {
		s, ok := t.Sections[key]
		if !ok {
			continue
		}
		text := s.DisplayContent(t.Body)
		if len([]byte(text)) > remaining {
			cut := utf8Cut(text, remaining)
			v.Sections[key] = SectionView{Text: cut, Truncated: true}
			v.Truncated = true
			remaining = 0
		} else {
			v.Sections[key] = SectionView{Text: text}
			remaining -= len([]byte(text))
		}
	}
	for _, ks := range markdown.KnownSections {
		if _, present := t.Sections[ks.Key]; present {
			v.AvailableSections = append(v.AvailableSections, ks.Key)
		}
	}
	for i, cs := range t.CustomSections {
		v.CustomSections = append(v.CustomSections, CustomSectionView{Position: i + 1, Heading: cs.Heading})
	}
	prereqs, prereqMore, perr := buildPrerequisites(st, t)
	if perr != nil {
		return nil, perr
	}
	v.Prerequisites, v.PrerequisitesMore = prereqs, prereqMore
	if opts.Readiness {
		readiness, err := Readiness(st, t.ID)
		if err != nil {
			return nil, err
		}
		v.Readiness = readiness
	}
	return v, nil
}

func buildPrerequisites(st *store.Store, t *Ticket) ([]Prerequisite, bool, error) {
	if len(t.DependsOn) == 0 {
		return nil, false, nil
	}
	more := len(t.DependsOn) > 20
	limit := len(t.DependsOn)
	if more {
		limit = 20
	}
	out := make([]Prerequisite, 0, limit)
	for _, dep := range t.DependsOn[:limit] {
		p := Prerequisite{ID: dep}
		other, err := ReadTicket(st, dep)
		if err != nil {
			if cErr, ok := err.(*contract.Error); ok && cErr.Code == contract.ErrNotFound {
				p.Missing = true
			} else {
				// Invalid referenced metadata is a data error.
				return nil, false, err
			}
		} else {
			p.Title = other.Title
			p.State = other.State
		}
		out = append(out, p)
	}
	return out, more, nil
}

func utf8Cut(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !isUTF8Boundary(s, cut) {
		cut--
	}
	return s[:cut]
}

func isUTF8Boundary(s string, cut int) bool {
	if cut >= len(s) {
		return true
	}
	if cut == 0 {
		return true
	}
	c := s[cut]
	if c < 0x80 {
		return true
	}
	// A cut point is valid only just after a complete UTF-8 sequence:
	// the cut byte must be a leading byte (not a continuation byte).
	return c&0xC0 != 0x80
}

// showFull returns the full body view.
func showFull(st *store.Store, ref string, opts ShowOptions) (*ShowView, error) {
	full, err := resolveShowID(st, ref)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	v, err := buildShow(st, t, opts, true)
	if err != nil {
		return nil, err
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = MaxBudget
	}
	if len([]byte(v.Body)) > maxBytes {
		cut := utf8Cut(v.Body, maxBytes)
		v.Body = cut
		v.Truncated = true
	}
	return v, nil
}

// Path resolves a ticket reference to its TASK.md path.
func Path(st *store.Store, ref string, absolute bool) (map[string]string, error) {
	full, err := resolveShowID(st, ref)
	if err != nil {
		return nil, err
	}
	t, err := ReadTicket(st, full)
	if err != nil {
		return nil, err
	}
	rel := t.TaskRelPath
	p := rel
	if absolute {
		p = filepath.Join(st.Root, rel)
	}
	return map[string]string{"id": full, "path": p}, nil
}
