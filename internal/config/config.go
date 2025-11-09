package config

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Config represents the complete configuration file
type Config struct {
	Instances           []BackupInstance `yaml:"instances"`
	DefaultSchedule     string           `yaml:"defaultSchedule,omitempty"`
	DefaultRetention    string           `yaml:"defaultRetention,omitempty"`
	DefaultStopAttached *bool            `yaml:"defaultStopAttached,omitempty"`
}

// BackupInstance represents a backup instance configuration
type BackupInstance struct {
	ID         string            `yaml:"id"`
	Repository string            `yaml:"repository"`
	Env        map[string]string `yaml:"env"`
}

// Load reads and parses the config file, expanding environment variables
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Expand environment variables in all fields
	for i := range cfg.Instances {
		cfg.Instances[i].Repository = expandEnv(cfg.Instances[i].Repository)
		for k, v := range cfg.Instances[i].Env {
			cfg.Instances[i].Env[k] = expandEnv(v)
		}
	}

	return &cfg, nil
}

// expandEnv expands environment variable references in the format ${VAR} or $VAR
func expandEnv(s string) string {
	// Match ${VAR} or $VAR patterns
	re := regexp.MustCompile(`\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
	return re.ReplaceAllStringFunc(s, func(match string) string {
		// Extract variable name
		var varName string
		if match[1] == '{' {
			varName = match[2 : len(match)-1] // ${VAR}
		} else {
			varName = match[1:] // $VAR
		}
		// Return environment variable value or empty string if not set
		return os.Getenv(varName)
	})
}

// GetDestination returns a destination by ID
func (c *Config) GetDestination(id string) (*BackupInstance, error) {
	// IMPORTANT: must return pointer to slice element, not loop variable copy.
	for i := range c.Instances {
		if c.Instances[i].ID == id {
			return &c.Instances[i], nil
		}
	}
	return nil, fmt.Errorf("destination %q not found in config", id)
}
