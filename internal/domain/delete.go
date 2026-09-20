package domain

import (
	"sort"

	"github.com/toolsupply/ticket/internal/store"
)

type DeleteResult struct {
	ID      string `json:"id"`
	Changed bool   `json:"changed"`
}

type BatchDeleteResult struct {
	Items []DeleteResult `json:"items"`
}

// Delete removes one ticket directory after resolving its full ID.
func Delete(st *store.Store, ref string) (*DeleteResult, error) {
	full, err := mustResolve(st, ref)
	if err != nil {
		return nil, err
	}
	if err := st.DeleteTicket(full); err != nil {
		return nil, err
	}
	return &DeleteResult{ID: full, Changed: true}, nil
}

// DeleteMany resolves every target before removing any directory, so a
// missing or ambiguous target cannot leave a partially deleted batch.
func DeleteMany(st *store.Store, refs []string) (*BatchDeleteResult, error) {
	resolved := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		full, err := mustResolve(st, ref)
		if err != nil {
			return nil, err
		}
		if !seen[full] {
			seen[full] = true
			resolved = append(resolved, full)
		}
	}
	sort.Strings(resolved)
	result := &BatchDeleteResult{Items: make([]DeleteResult, 0, len(resolved))}
	for _, id := range resolved {
		if err := st.DeleteTicket(id); err != nil {
			return nil, err
		}
		result.Items = append(result.Items, DeleteResult{ID: id, Changed: true})
	}
	return result, nil
}
