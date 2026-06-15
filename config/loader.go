// Package config loads lens.yaml configuration with ${ENV_VAR} substitution.
// When no config file is present, the agent falls back to LENS_* env vars;
// the LoadConfig function in the agent package handles that path.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the top-level structure of lens.yaml.
type File struct {
	APIVersion  string        `yaml:"apiVersion"`
	Kind        string        `yaml:"kind"`
	Transport   ProviderBlock `yaml:"transport"`
	Persistence ProviderBlock `yaml:"persistence"`
	Discovery   ProviderBlock `yaml:"discovery"`
	Target      ProviderBlock `yaml:"target"`
	Observer    ObserverBlock `yaml:"observer"`
	Agent       AgentBlock    `yaml:"agent"`
}

// ProviderBlock names a provider and passes arbitrary configuration to it.
// Top-level blocks (transport, persistence, discovery) use the "provider" key.
// Observer provider list entries use the "name" key — both are supported.
type ProviderBlock struct {
	Provider string         `yaml:"provider"`
	Name     string         `yaml:"name"`
	Config   map[string]any `yaml:"config"`
}

// ProviderName returns the non-empty value between Provider and Name,
// preferring Provider so top-level blocks and observer entries both work.
func (p ProviderBlock) ProviderName() string {
	if p.Provider != "" {
		return p.Provider
	}
	return p.Name
}

// ObserverBlock controls the observability subsystem.
type ObserverBlock struct {
	Enabled   bool            `yaml:"enabled"`
	Providers []ProviderBlock `yaml:"providers"`
}

// AgentBlock holds agent-level settings that do not belong to a specific provider.
type AgentBlock struct {
	TargetURL     string         `yaml:"targetURL"`
	Port          string         `yaml:"port"`
	BindAddr      string         `yaml:"bindAddr"`
	AdvertiseAddr string         `yaml:"advertiseAddr"`
	Token         string         `yaml:"token"`
	CooldownMs    int            `yaml:"cooldownMs"`
	Cooldowns     map[string]int `yaml:"cooldowns"`
	LogLevel      string         `yaml:"logLevel"`
	Replay        ReplayBlock    `yaml:"replay"`
}

// ReplayBlock controls the missed-invalidation replay feature.
type ReplayBlock struct {
	Enabled     bool `yaml:"enabled"`
	WindowHours int  `yaml:"windowHours"`
}

// envVarRe matches ${VAR_NAME} substitution markers in YAML values.
var envVarRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// Load reads and parses the lens.yaml file at path, expanding ${ENV_VAR}
// references using the current process environment. Returns an empty File
// with zero values when path is empty.
func Load(path string) (File, error) {
	if path == "" {
		return File{}, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("config: read %s: %w", path, err)
	}

	expanded := envVarRe.ReplaceAllStringFunc(string(raw), func(match string) string {
		key := strings.TrimPrefix(strings.TrimSuffix(match, "}"), "${")
		val := os.Getenv(key)
		if val != "" {
			return val
		}
		return match
	})

	var f File
	if err := yaml.Unmarshal([]byte(expanded), &f); err != nil {
		return File{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return f, nil
}
