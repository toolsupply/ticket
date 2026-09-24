package cli

import (
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
)

var tqlFields = map[string]bool{
	"state": true, "tag": true, "assignee": true, "priority": true, "parent": true,
}

var tqlBarePredicates = map[string]bool{
	"terminal": true, "ready": true, "blocked": true, "claimed": true,
	"unclaimed": true, "archived": true,
}

var tqlComparisons = map[string]domain.Comparison{
	"eq": domain.ComparisonEQ, "ne": domain.ComparisonNE,
	"lt": domain.ComparisonLT, "le": domain.ComparisonLE,
	"gt": domain.ComparisonGT, "ge": domain.ComparisonGE,
}

type tqlParser struct {
	tokens []string
	pos    int
}

func parseTQL(tokens []string) (domain.Expr, error) {
	parser := &tqlParser{tokens: tokens}
	if len(tokens) == 0 {
		return nil, nil
	}
	expr, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if parser.pos != len(parser.tokens) {
		return nil, tqlError("unexpected token " + parser.tokens[parser.pos])
	}
	return expr, nil
}

func (p *tqlParser) parseOr() (domain.Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	terms := []domain.Expr{left}
	for p.peekWord("or") {
		p.pos++
		if p.pos == len(p.tokens) {
			return nil, tqlError("or requires an expression after it")
		}
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		terms = append(terms, right)
	}
	if len(terms) == 1 {
		return left, nil
	}
	return domain.Or(terms...), nil
}

func (p *tqlParser) parseAnd() (domain.Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	terms := []domain.Expr{left}
	for p.pos < len(p.tokens) && !p.peekWord("or") {
		if p.peekWord("and") {
			p.pos++
			if p.pos == len(p.tokens) || p.peekWord("or") {
				return nil, tqlError("and requires an expression after it")
			}
		}
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		terms = append(terms, right)
	}
	if len(terms) == 1 {
		return left, nil
	}
	return domain.And(terms...), nil
}

func (p *tqlParser) parseUnary() (domain.Expr, error) {
	if p.peekWord("not") {
		p.pos++
		if p.pos == len(p.tokens) {
			return nil, tqlError("not requires an expression after it")
		}
		expr, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return domain.Not(expr), nil
	}
	if p.pos >= len(p.tokens) {
		return nil, tqlError("expected an expression")
	}
	return p.parsePredicate()
}

func (p *tqlParser) parsePredicate() (domain.Expr, error) {
	token := p.tokens[p.pos]
	p.pos++
	if strings.Contains(token, ":") {
		parts := strings.SplitN(token, ":", 2)
		field, value := strings.ToLower(parts[0]), parts[1]
		if !tqlFields[field] {
			return nil, tqlError("unknown query field " + parts[0])
		}
		if value == "" {
			return nil, tqlError("query field " + field + " requires a value")
		}
		return domain.Field(field, value), nil
	}
	word := strings.ToLower(token)
	if tqlBarePredicates[word] {
		return domain.Bare(word), nil
	}
	if _, ok := tqlComparisons[word]; ok {
		return nil, tqlError("comparison " + word + " requires a field and value")
	}
	if !tqlFields[word] {
		return nil, tqlError("unknown query token " + token)
	}
	if p.pos >= len(p.tokens) {
		return nil, tqlError("query field " + word + " requires a value or comparison")
	}
	op, ok := tqlComparisons[strings.ToLower(p.tokens[p.pos])]
	if !ok {
		return nil, tqlError("query field " + word + " requires a value or comparison")
	}
	p.pos++
	if p.pos >= len(p.tokens) || p.peekWord("and") || p.peekWord("or") {
		return nil, tqlError("comparison " + string(op) + " requires a value")
	}
	value := p.tokens[p.pos]
	p.pos++
	return domain.Compare(word, op, value), nil
}

func (p *tqlParser) peekWord(want string) bool {
	return p.pos < len(p.tokens) && strings.EqualFold(p.tokens[p.pos], want)
}

func tqlError(message string) error {
	return contract.NewError(contract.ErrInvalidArgument, "Invalid query: "+message+".", nil)
}
