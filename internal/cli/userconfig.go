package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ticket/internal/contract"
	"ticket/internal/domain"
	"ticket/internal/jsonx"
	"ticket/internal/scm"
	"ticket/internal/store"
)

type userConfig struct {
	Editor    string                 `json:"editor"`
	Decorator string                 `json:"decorator"`
	Scopes    map[string]scopeConfig `json:"scopes"`
}

type scopeConfig struct {
	Repository string   `json:"repository"`
	CreateTags []string `json:"create_tags"`
	WorkTags   []string `json:"work_tags"`
	SCM        string   `json:"scm"`
	SCMMode    string   `json:"scm_mode"`
}

func (g *globalOpts) loadConfig(cwd string) error {
	if g.configLoaded {
		return nil
	}
	path, explicit, err := g.configFile(cwd)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			if g.scopeName != "" {
				return contract.NewError(contract.ErrInvalidArgument,
					"Configuration scope not found: "+g.scopeName+".", nil)
			}
			g.configLoaded = true
			return nil
		}
		if os.IsNotExist(err) {
			return contract.NewError(contract.ErrNotFound,
				"Config file not found: "+path+".", nil)
		}
		return contract.NewError(contract.ErrIOError,
			"Cannot read config file "+path+": "+err.Error(), nil)
	}
	var cfg userConfig
	if err := jsonx.Decode(data, &cfg); err != nil {
		return contract.NewError(contract.ErrInvalidJSON,
			fmt.Sprintf("Config file %s rejected: %v.", path, err), nil)
	}
	for name, scope := range cfg.Scopes {
		var err error
		if scope.CreateTags, err = domain.NormalizeTags(scope.CreateTags); err != nil {
			return contract.NewError(contract.ErrInvalidArgument,
				fmt.Sprintf("Scope %q has invalid create_tags: %s", name, err), nil)
		}
		if scope.WorkTags, err = domain.NormalizeTags(scope.WorkTags); err != nil {
			return contract.NewError(contract.ErrInvalidArgument,
				fmt.Sprintf("Scope %q has invalid work_tags: %s", name, err), nil)
		}
		if err := scm.ValidateValues(scope.SCM, scope.SCMMode); err != nil {
			return contract.NewError(contract.ErrInvalidArgument,
				fmt.Sprintf("Scope %q has invalid SCM settings: %s", name, err), nil)
		}
		if scope.Repository != "" {
			root := scope.Repository
			if !filepath.IsAbs(root) {
				root = filepath.Join(filepath.Dir(path), root)
			}
			root, err = filepath.Abs(root)
			if err != nil {
				return contract.NewError(contract.ErrInvalidArgument,
					fmt.Sprintf("Scope %q has an invalid repository.", name), nil)
			}
			scope.Repository = filepath.Clean(root)
		}
		cfg.Scopes[name] = scope
	}
	if g.scopeName != "" {
		scope, ok := cfg.Scopes[g.scopeName]
		if !ok {
			return contract.NewError(contract.ErrInvalidArgument,
				"Configuration scope not found: "+g.scopeName+".", nil)
		}
		g.scope = &scope
	}
	g.config = cfg
	g.configLoaded = true
	return nil
}

func (g *globalOpts) configFile(cwd string) (string, bool, error) {
	if g.configExplicit {
		path := g.configPath
		if path == "" {
			return "", true, contract.NewError(contract.ErrInvalidArgument,
				"--config requires a file path.", nil)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", true, contract.NewError(contract.ErrInvalidArgument,
				"Cannot resolve config file path.", nil)
		}
		return absolute, true, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, contract.NewError(contract.ErrIOError,
			"Cannot determine the user config directory: "+err.Error(), nil)
	}
	return filepath.Join(home, ".ticket", "config.json"), false, nil
}

func (g *globalOpts) selectedRoot() string {
	if store.ConfiguredRoot() == "" && g.scope != nil {
		return g.scope.Repository
	}
	return ""
}

func (g *globalOpts) effectiveSCM() (string, string) {
	kind, mode := os.Getenv("TICKET_SCM"), os.Getenv("TICKET_SCM_MODE")
	if g.scope != nil {
		if strings.TrimSpace(kind) == "" {
			kind = g.scope.SCM
		}
		if strings.TrimSpace(mode) == "" {
			mode = g.scope.SCMMode
		}
	}
	return kind, mode
}

func (g *globalOpts) createTags(explicit []string) []string {
	defaults := []string(nil)
	if g.scope != nil {
		defaults = append(defaults, g.scope.CreateTags...)
	}
	return mergeTagValues(defaults, environmentTags("TICKET_CREATE_TAGS"), explicit)
}

func (g *globalOpts) workTags(explicit []string) []string {
	defaults := []string(nil)
	if g.scope != nil {
		defaults = append(defaults, g.scope.WorkTags...)
	}
	return mergeTagValues(defaults, environmentTags("TICKET_WORK_TAGS"), explicit)
}

func (g *globalOpts) decorator() string {
	if value := strings.TrimSpace(os.Getenv("TICKET_DECORATOR")); value != "" {
		return value
	}
	if value := strings.TrimSpace(g.config.Decorator); value != "" {
		return value
	}
	return ""
}
