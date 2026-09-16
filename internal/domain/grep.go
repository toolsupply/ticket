package domain

import (
	"regexp"

	"ticket/internal/contract"
	"ticket/internal/store"
)

// Grep finds tickets for which every expression matches either the ticket ID
// or complete TASK.md content. Plain text expressions therefore match
// substrings, while regular expression syntax can be used when a more
// precise match is needed.
func Grep(st *store.Store, expressions ...string) (*ListResult, error) {
	if len(expressions) == 0 {
		return nil, contract.NewError(contract.ErrInvalidArgument,
			"Grep requires at least one expression.", nil)
	}
	patterns := make([]*regexp.Regexp, 0, len(expressions))
	for _, expression := range expressions {
		re, err := regexp.Compile(expression)
		if err != nil {
			return nil, contract.NewError(contract.ErrInvalidArgument,
				"Invalid grep expression: "+err.Error(), nil)
		}
		patterns = append(patterns, re)
	}
	tickets, diags, err := scanWith(st)
	if err != nil {
		return nil, err
	}
	if len(diags) > 0 {
		return nil, collectionError(diags)
	}
	matches := make([]*Ticket, 0)
	for _, ticket := range tickets {
		matched := true
		for _, re := range patterns {
			if !re.MatchString(ticket.ID) && !re.Match(ticket.FileBytes) {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, ticket)
		}
	}
	sortTickets(matches)
	result := &ListResult{Items: make([]Summary, 0, len(matches))}
	fields := []string{"id", "title", "state", "priority", "assignee"}
	for _, ticket := range matches {
		result.Items = append(result.Items, project(ticket, fields))
	}
	return result, nil
}
