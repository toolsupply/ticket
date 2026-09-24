package cli

import (
	"os"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/domain"
)

func environmentTags(name string) []string {
	var values []string
	if raw := os.Getenv(name); raw != "" {
		for _, tag := range strings.Split(raw, ",") {
			values = append(values, strings.TrimSpace(tag))
		}
	}
	return values
}

func mergeTagValues(groups ...[]string) []string {
	var values []string
	for _, group := range groups {
		values = append(values, group...)
	}
	seen := make(map[string]bool, len(values))
	merged := make([]string, 0, len(values))
	for _, tag := range values {
		key := strings.TrimSpace(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, tag)
	}
	return merged
}

// extractTrailingTitleTags recognizes the human-only title shorthand. It
// leaves a title untouched unless the final whitespace-delimited tokens are
// all valid hashtag forms, so interior hash text remains ordinary title text.
func extractTrailingTitleTags(title string) (string, []string, error) {
	words := strings.Fields(title)
	start := len(words)
	var extracted []string
	for start > 0 {
		word := words[start-1]
		if !strings.HasPrefix(word, "#") || len(word) == 1 {
			break
		}
		tag := word[1:]
		if _, err := domain.NormalizeTags([]string{tag}); err != nil {
			break
		}
		extracted = append(extracted, tag)
		start--
	}
	if len(extracted) == 0 {
		return title, nil, nil
	}
	if start == 0 {
		return "", nil, contract.NewError(contract.ErrInvalidArgument,
			"Title must contain text before trailing #tags.", nil)
	}
	for left, right := 0, len(extracted)-1; left < right; left, right = left+1, right-1 {
		extracted[left], extracted[right] = extracted[right], extracted[left]
	}
	return strings.Join(words[:start], " "), extracted, nil
}
