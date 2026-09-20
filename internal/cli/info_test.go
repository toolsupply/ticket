package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInfoReportsUnnamedRepositoryMetadata(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	result := exactlyOneJSONObject(t, mustCLI(t, "info"))
	if result["path"] != filepath.Join(dir, "tickets") || result["name"] != nil || result["scope"] != nil || result["format_version"] != float64(1) || result["storage_version"] != float64(1) {
		t.Fatalf("unnamed info: %v", result)
	}
	human, code := runCLIHuman(t, "info")
	if code != 0 || !strings.Contains(human, "name:            (unnamed)") || !strings.Contains(human, "scope:            (none)") {
		t.Fatalf("unnamed human info: exit=%d out=%q", code, human)
	}
}

func TestInfoReportsNameAndSelectedScope(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	configPath := filepath.Join(dir, "scopes.json")
	config := map[string]any{"scopes": map[string]any{"named": map[string]any{"repository": "named-tickets"}}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := runCLI(t, "--config", configPath, "--scope", "named", "init"); code != 0 {
		t.Fatalf("scoped init: exit=%d out=%q", code, out)
	}
	root := filepath.Join(dir, "named-tickets")
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"format_version":1,"name":"Named repository"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result := exactlyOneJSONObject(t, mustCLI(t, "--config", configPath, "--scope", "named", "info"))
	if result["path"] != root || result["name"] != "Named repository" || result["scope"] != "named" {
		t.Fatalf("named scoped info: %v", result)
	}
}
