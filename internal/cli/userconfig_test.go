package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/toolsupply/ticket/internal/store"
)

func writeJSONConfig(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func writeUserConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNamedScopeUsesRepositoryAndCombinesTagDefaults(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Chdir(dir)
	setTestHome(t, home)
	t.Setenv("TICKET_SCOPE", "")
	t.Setenv("TICKET_ROOT", "")
	t.Setenv("TICKET_CREATE_TAGS", "urgent")
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	config := filepath.Join(home, "scopes.json")
	writeUserConfig(t, config, writeJSONConfig(t, map[string]any{
		"scopes": map[string]any{
			"windows": map[string]any{"repository": filepath.Join(dir, "tickets"), "create_tags": []string{"windows"}},
		},
	}))
	created := exactlyOneJSONObject(t, mustCLI(t, "--config", config, "--scope", "windows", "create", "Scoped", "Ready."))
	shown := exactlyOneJSONObject(t, mustCLI(t, "--config", config, "--scope", "windows", "show", created["id"].(string)))
	tags := shown["tags"].([]any)
	if len(tags) != 2 || tags[0] != "urgent" || tags[1] != "windows" {
		t.Fatalf("scope and environment tags: %v", tags)
	}
}

func TestScopeRootKeyIsRejected(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Chdir(dir)
	setTestHome(t, home)
	t.Setenv("TICKET_SCOPE", "legacy")
	t.Setenv("TICKET_ROOT", "")
	config := filepath.Join(home, "config.json")
	writeUserConfig(t, config, writeJSONConfig(t, map[string]any{
		"scopes": map[string]any{
			"legacy": map[string]any{"root": filepath.Join(dir, "tickets")},
		},
	}))
	if out, code := runCLI(t, "--config", config, "list"); code == 0 || errCode(t, out) != "invalid_json" {
		t.Fatalf("legacy scope root accepted: exit=%d out=%q", code, out)
	}
}

func TestScopedInitUsesRelativeRootFromConfigFile(t *testing.T) {
	workDir := t.TempDir()
	configDir := t.TempDir()
	t.Chdir(workDir)
	setTestHome(t, t.TempDir())
	t.Setenv("TICKET_SCOPE", "")
	t.Setenv("TICKET_ROOT", "")
	config := filepath.Join(configDir, "config.json")
	writeUserConfig(t, config, `{"scopes":{"windows":{"repository":"repository/tickets"}}}`)
	root := filepath.Join(configDir, "repository", "tickets")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("scope root unexpectedly exists: %v", err)
	}
	if out, code := runCLI(t, "--config", config, "--scope", "windows", "init"); code != 0 {
		t.Fatalf("scoped init: exit=%d out=%q", code, out)
	}
	if _, err := store.LoadConfig(root); err != nil {
		t.Fatalf("relative scope root was not initialized: %v", err)
	}
	if out, code := runCLI(t, "--config", config, "--scope", "windows", "list"); code != 0 {
		t.Fatalf("list from relative scope root: exit=%d out=%q", code, out)
	}
	environmentRoot := filepath.Join(t.TempDir(), "tickets")
	t.Setenv("TICKET_ROOT", environmentRoot)
	if out, code := runCLI(t, "--config", config, "--scope", "windows", "init"); code != 0 {
		t.Fatalf("environment-root init: exit=%d out=%q", code, out)
	}
	if _, err := store.LoadConfig(environmentRoot); err != nil {
		t.Fatalf("environment root was not preferred over scope root: %v", err)
	}
}

func TestGlobalConfigAndScopeFlagsRejectCrossBoundaryDuplicates(t *testing.T) {
	if out, code := runCLI(t, "--config", "one.json", "list", "--config", "two.json"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("duplicate config flags: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "--scope", "windows", "list", "--scope", "security"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("duplicate scope flags: exit=%d out=%q", code, out)
	}
}

func TestScopeSelectionAndEnvironmentPrecedence(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Chdir(dir)
	setTestHome(t, home)
	t.Setenv("TICKET_SCOPE", "")
	t.Setenv("TICKET_ROOT", "")
	t.Setenv("TICKET_CREATE_TAGS", "")
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	t.Setenv("TICKET_SCOPE", "windows")
	config := filepath.Join(home, ".ticket", "config.json")
	writeUserConfig(t, config, writeJSONConfig(t, map[string]any{
		"scopes": map[string]any{
			"windows":  map[string]any{"repository": filepath.Join(dir, "tickets"), "create_tags": []string{"windows"}},
			"security": map[string]any{"repository": filepath.Join(dir, "tickets"), "create_tags": []string{"security"}},
		},
	}))
	ambient := exactlyOneJSONObject(t, mustCLI(t, "create", "Ambient", "Ready."))
	ambientView := exactlyOneJSONObject(t, mustCLI(t, "show", ambient["id"].(string)))
	if got := ambientView["tags"].([]any); len(got) != 1 || got[0] != "windows" {
		t.Fatalf("ambient scope tags: %v", got)
	}
	explicit := exactlyOneJSONObject(t, mustCLI(t, "--scope", "security", "create", "Explicit", "Ready."))
	explicitView := exactlyOneJSONObject(t, mustCLI(t, "show", explicit["id"].(string)))
	if got := explicitView["tags"].([]any); len(got) != 1 || got[0] != "security" {
		t.Fatalf("explicit scope override tags: %v", got)
	}

	otherRoot := filepath.Join(t.TempDir(), "tickets")
	t.Setenv("TICKET_ROOT", otherRoot)
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("other root init: exit=%d out=%q", code, out)
	}
	other := exactlyOneJSONObject(t, mustCLI(t, "create", "Environment root", "Ready."))
	otherView := exactlyOneJSONObject(t, mustCLI(t, "show", other["id"].(string)))
	if got := otherView["tags"].([]any); len(got) != 1 || got[0] != "windows" {
		t.Fatalf("environment root scope tags: %v", got)
	}
}

func TestScopeWorkTagsCombineAndDoNotAffectList(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Chdir(dir)
	setTestHome(t, home)
	t.Setenv("TICKET_SCOPE", "")
	t.Setenv("TICKET_ROOT", "")
	t.Setenv("TICKET_CREATE_TAGS", "")
	t.Setenv("TICKET_WORK_TAGS", "networking")
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	t.Setenv("TICKET_SCOPE", "workers")
	config := filepath.Join(home, ".ticket", "config.json")
	writeUserConfig(t, config, writeJSONConfig(t, map[string]any{
		"scopes": map[string]any{
			"workers": map[string]any{"repository": filepath.Join(dir, "tickets"), "work_tags": []string{"windows"}},
		},
	}))
	both := exactlyOneJSONObject(t, mustCLI(t, "create", "Both", "Ready.", "--tag", "windows", "--tag", "networking"))["id"].(string)
	mustCLI(t, "create", "Windows", "Ready.", "--tag", "windows")
	listed := exactlyOneJSONObject(t, mustCLI(t, "list", "--tag", "networking"))
	if items := listed["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != both {
		t.Fatalf("list should ignore work defaults: %v", listed)
	}
	next := exactlyOneJSONObject(t, mustCLI(t, "next"))
	if got := next["item"].(map[string]any)["id"]; got != both {
		t.Fatalf("scope work tags: got %v want %s", got, both)
	}
	wait := exactlyOneJSONObject(t, mustCLI(t, "wait", "--tag", "networking"))
	if got := wait["item"].(map[string]any)["id"]; got != both {
		t.Fatalf("scope work tags and explicit filter: got %v want %s", got, both)
	}
	reviewBoth := exactlyOneJSONObject(t, mustCLI(t, "create", "Review both", "Ready.", "--tag", "windows", "--tag", "networking"))["id"].(string)
	reviewWindows := exactlyOneJSONObject(t, mustCLI(t, "create", "Review windows", "Ready.", "--tag", "windows"))["id"].(string)
	for _, id := range []string{reviewBoth, reviewWindows} {
		if out, code := runCLI(t, "claim", id, "--actor", "coder"); code != 0 {
			t.Fatalf("claim %s: exit=%d out=%q", id, code, out)
		}
		if out, code := runCLI(t, "submit", id, "--actor", "coder"); code != 0 {
			t.Fatalf("submit %s: exit=%d out=%q", id, code, out)
		}
	}
	review := exactlyOneJSONObject(t, mustCLI(t, "next", "review"))
	if got := review["item"].(map[string]any)["id"]; got != reviewBoth {
		t.Fatalf("scope review tags: got %v want %s", got, reviewBoth)
	}
}

func TestConfigErrorsAndMissingDefaultBehavior(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	missingHome := t.TempDir()
	setTestHome(t, missingHome)
	t.Setenv("TICKET_SCOPE", "")
	if out, code := runCLI(t, "init"); code != 0 {
		t.Fatalf("init: exit=%d out=%q", code, out)
	}
	if out, code := runCLI(t, "list"); code != 0 {
		t.Fatalf("missing default config should be allowed: exit=%d out=%q", code, out)
	}
	missing := filepath.Join(t.TempDir(), "missing.json")
	if out, code := runCLI(t, "--config", missing, "list"); code == 0 || errCode(t, out) != "not_found" {
		t.Fatalf("missing explicit config: exit=%d out=%q", code, out)
	}
	writeUserConfig(t, filepath.Join(missingHome, ".ticket", "config.json"), `{ "scopes": {`)
	if out, code := runCLI(t, "list"); code == 0 || errCode(t, out) != "invalid_json" {
		t.Fatalf("malformed default config: exit=%d out=%q", code, out)
	}
	writeUserConfig(t, filepath.Join(missingHome, ".ticket", "config.json"), `{"scopes":{}}`)
	t.Setenv("TICKET_SCOPE", "missing")
	if out, code := runCLI(t, "list"); code == 0 || errCode(t, out) != "invalid_argument" {
		t.Fatalf("unknown scope: exit=%d out=%q", code, out)
	}
}

func TestConfigPreferencesYieldToEnvironment(t *testing.T) {
	t.Setenv("TICKET_EDITOR", "")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	t.Setenv("TICKET_DECORATOR", "")
	g := &globalOpts{config: userConfig{Editor: "config-editor", Decorator: "config-decorator"}}
	if got := selectedEditor(g); got != "config-editor" {
		t.Fatalf("config editor: got %q", got)
	}
	if got := g.decorator(); got != "config-decorator" {
		t.Fatalf("config decorator: got %q", got)
	}
	t.Setenv("TICKET_EDITOR", "environment-editor")
	t.Setenv("TICKET_DECORATOR", "environment-decorator")
	if got := selectedEditor(g); got != "environment-editor" {
		t.Fatalf("environment editor: got %q", got)
	}
	if got := g.decorator(); got != "environment-decorator" {
		t.Fatalf("environment decorator: got %q", got)
	}
}
