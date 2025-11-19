package config

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Config represents the complete configuration file
type Config struct {
	Instances     []BackupInstance `yaml:"instances"`
	Retention     string           `yaml:"retention,omitempty"`     // Global default retention
	StopAttached  *bool            `yaml:"stopAttached,omitempty"`  // Global default stopAttached
	ResticTimeout string           `yaml:"resticTimeout,omitempty"` // Global default timeout (e.g., "5m", "30s")
	Mesh          *MeshConfig      `yaml:"mesh,omitempty"`          // Optional mesh configuration
}

// MeshConfig represents mesh networking configuration for connecting multiple Marina instances
type MeshConfig struct {
	NodeName     string   `yaml:"nodeName,omitempty"`     // Optional custom node name (defaults to hostname)
	Peers        []string `yaml:"peers,omitempty"`        // List of peer API URLs (e.g., "http://marina-node2:8080")
	AuthPassword string   `yaml:"authPassword,omitempty"` // Optional authentication password (can use env var)
}

// BackupInstance represents a backup instance configuration
type BackupInstance struct {
	ID            string            `yaml:"id"`
	Repository    string            `yaml:"repository,omitempty"`    // Restic repository (not used if customImage is set)
	CustomImage   string            `yaml:"customImage,omitempty"`   // Custom Docker image for backup (alternative to Restic)
	Schedule      string            `yaml:"schedule"`                // Cron schedule for this instance's backups
	Retention     string            `yaml:"retention,omitempty"`     // Optional: instance-specific retention (overrides global)
	ResticTimeout string            `yaml:"resticTimeout,omitempty"` // Optional: instance-specific timeout (overrides global)
	Env           map[string]string `yaml:"env,omitempty"`           // Environment variables passed to backend
	Targets       []TargetConfig    `yaml:"targets,omitempty"`       // List of backup targets (volumes and databases)
}

// TargetConfig represents a backup target configuration
type TargetConfig struct {
	Volume       string   `yaml:"volume,omitempty"`       // Volume name (mutually exclusive with DB)
	DB           string   `yaml:"db,omitempty"`           // Container name for database (mutually exclusive with Volume)
	Paths        []string `yaml:"paths,omitempty"`        // Paths to backup (for volumes, default: ["/"])
	StopAttached *bool    `yaml:"stopAttached,omitempty"` // Stop containers using volume (for volumes)
	PreHook      string   `yaml:"preHook,omitempty"`      // Command to run before backup
	PostHook     string   `yaml:"postHook,omitempty"`     // Command to run after backup
	DBKind       string   `yaml:"dbKind,omitempty"`       // Database type: postgres, mysql, mariadb, mongo, redis (auto-detected if not provided)
	DumpArgs     []string `yaml:"dumpArgs,omitempty"`     // Arguments for database dump command
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
		cfg.Instances[i].CustomImage = expandEnv(cfg.Instances[i].CustomImage)
		cfg.Instances[i].Schedule = expandEnv(cfg.Instances[i].Schedule)
		cfg.Instances[i].Retention = expandEnv(cfg.Instances[i].Retention)
		cfg.Instances[i].ResticTimeout = expandEnv(cfg.Instances[i].ResticTimeout)
		for k, v := range cfg.Instances[i].Env {
			cfg.Instances[i].Env[k] = expandEnv(v)
		}
		// Expand environment variables in target configurations
		for j := range cfg.Instances[i].Targets {
			cfg.Instances[i].Targets[j].Volume = expandEnv(cfg.Instances[i].Targets[j].Volume)
			cfg.Instances[i].Targets[j].DB = expandEnv(cfg.Instances[i].Targets[j].DB)
			cfg.Instances[i].Targets[j].PreHook = expandEnv(cfg.Instances[i].Targets[j].PreHook)
			cfg.Instances[i].Targets[j].PostHook = expandEnv(cfg.Instances[i].Targets[j].PostHook)
			cfg.Instances[i].Targets[j].DBKind = expandEnv(cfg.Instances[i].Targets[j].DBKind)
			for k := range cfg.Instances[i].Targets[j].Paths {
				cfg.Instances[i].Targets[j].Paths[k] = expandEnv(cfg.Instances[i].Targets[j].Paths[k])
			}
			for k := range cfg.Instances[i].Targets[j].DumpArgs {
				cfg.Instances[i].Targets[j].DumpArgs[k] = expandEnv(cfg.Instances[i].Targets[j].DumpArgs[k])
			}
		}
	}

	// Expand environment variables in mesh config
	if cfg.Mesh != nil {
		cfg.Mesh.NodeName = expandEnv(cfg.Mesh.NodeName)
		cfg.Mesh.AuthPassword = expandEnv(cfg.Mesh.AuthPassword)
		for i := range cfg.Mesh.Peers {
			cfg.Mesh.Peers[i] = expandEnv(cfg.Mesh.Peers[i])
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
