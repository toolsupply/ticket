package cli

import (
	"os"
	"strings"
)

// mergeEnvironmentTags adds comma-separated defaults to explicit tags while
// preserving the tag validator's handling of invalid values. Empty entries
// remain in the result so malformed environment values are rejected by the
// domain layer rather than silently ignored.
func mergeEnvironmentTags(name string, explicit []string) []string {
	return mergeTagValues(environmentTags(name), explicit)
}

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
