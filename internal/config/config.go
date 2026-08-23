package config

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"time"

	"github.com/polarfoxDev/marina/internal/model"
	"gopkg.in/yaml.v3"
)

// Config represents the complete configuration file
type Config struct {
	Instances    []BackupInstance `yaml:"instances"`
	Retention    string           `yaml:"retention,omitempty"`    // Global default retention
	StopAttached *bool            `yaml:"stopAttached,omitempty"` // Global default stopAttached
	Timeout      string           `yaml:"timeout,omitempty"`      // Global default backend timeout (e.g., "5m", "30s")
	DBPath       string           `yaml:"dbPath,omitempty"`       // Database path (default: "/var/lib/marina/marina.db")
	APIPort      string           `yaml:"apiPort,omitempty"`      // API server port (default: "8080")
	CorsOrigins  []string         `yaml:"corsOrigins,omitempty"`  // Additional CORS origins for API server
	NodeName     string           `yaml:"nodeName,omitempty"`     // Optional custom node name (defaults to hostname)
	AuthPassword string           `yaml:"authPassword,omitempty"` // Optional authentication password for API access
	Peers        []string         `yaml:"peers,omitempty"`        // Optional peer API URLs for federation (e.g., "http://marina-node2:8080")
}

// S3Config configures the S3-compatible destination for a backup instance.
// This is the only supported storage backend other than a custom image.
type S3Config struct {
	Endpoint        string `yaml:"endpoint"`         // host[:port], no scheme, e.g. "fsn1.your-objectstorage.com"
	Region          string `yaml:"region,omitempty"` // e.g. "fsn1", "us-east-1"
	Bucket          string `yaml:"bucket"`           // bucket name (must already exist)
	Prefix          string `yaml:"prefix,omitempty"` // optional key prefix within the bucket
	AccessKeyID     string `yaml:"accessKeyId"`
	SecretAccessKey string `yaml:"secretAccessKey"`
	UseSSL          *bool  `yaml:"useSSL,omitempty"`         // default true
	ForcePathStyle  bool   `yaml:"forcePathStyle,omitempty"` // default false; set true for MinIO and most non-AWS S3-compatible services
}

// EncryptionConfig configures optional end-to-end encryption of backup
// archives. If Password is empty, archives are stored unencrypted.
type EncryptionConfig struct {
	Password string `yaml:"password,omitempty"`
}

// BackupInstance represents a backup instance configuration
type BackupInstance struct {
	ID           string              `yaml:"id"`
	S3           *S3Config           `yaml:"s3,omitempty"`           // S3-compatible destination (mutually exclusive with customImage)
	CustomImage  string              `yaml:"customImage,omitempty"`  // Custom Docker image for backup (alternative to S3)
	Encryption   *EncryptionConfig   `yaml:"encryption,omitempty"`   // Optional end-to-end encryption (S3 destinations only)
	Schedule     string              `yaml:"schedule"`               // Cron schedule for this instance's backups
	Retention    string              `yaml:"retention,omitempty"`    // Optional: instance-specific retention (overrides global)
	Timeout      string              `yaml:"timeout,omitempty"`      // Optional: instance-specific backend timeout (overrides global)
	Env          map[string]string   `yaml:"env,omitempty"`          // Environment variables passed to the custom-image backend
	Targets      []TargetConfig      `yaml:"targets,omitempty"`      // List of backup targets (volumes and databases)
	BeforeBackup []model.HookCommand `yaml:"beforeBackup,omitempty"` // Commands to run before backup starts
	AfterBackup  []model.HookCommand `yaml:"afterBackup,omitempty"`  // Commands to run after backup completes
}

// TargetConfig represents a backup target configuration
type TargetConfig struct {
	Volume       string   `yaml:"volume,omitempty"`       // Volume name (mutually exclusive with DB)
	DB           string   `yaml:"db,omitempty"`           // Container name for database (mutually exclusive with Volume)
	Paths        []string `yaml:"paths,omitempty"`        // Paths to backup (for volumes, default: ["/"])
	StopAttached *bool    `yaml:"stopAttached,omitempty"` // Stop containers using volume (for volumes)
	DBKind       string   `yaml:"dbKind,omitempty"`       // Database type: postgres, mysql, mariadb, mongo, redis (auto-detected if not provided)
	DumpArgs     []string `yaml:"dumpArgs,omitempty"`     // Arguments for database dump command
}

// Load reads and parses the config file, expanding environment variables
// and validating the result.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	expandEnvInPlace(&cfg)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// Validate checks the config for the mistakes that would otherwise only
// surface as a confusing runtime error: a destination that's neither S3 nor
// a custom image (or is both), an S3 destination missing required fields, a
// malformed timeout, or a duplicate/missing instance id.
func (c *Config) Validate() error {
	if c.Timeout != "" {
		if _, err := time.ParseDuration(c.Timeout); err != nil {
			return fmt.Errorf("global timeout %q: %w", c.Timeout, err)
		}
	}

	seenIDs := make(map[string]bool, len(c.Instances))
	for i := range c.Instances {
		inst := &c.Instances[i]

		if inst.ID == "" {
			return fmt.Errorf("instance #%d: id is required", i+1)
		}
		if seenIDs[inst.ID] {
			return fmt.Errorf("instance %q: duplicate id", inst.ID)
		}
		seenIDs[inst.ID] = true

		hasS3 := inst.S3 != nil
		hasCustomImage := inst.CustomImage != ""
		if hasS3 && hasCustomImage {
			return fmt.Errorf("instance %q: cannot specify both 's3' and 'customImage'", inst.ID)
		}
		if !hasS3 && !hasCustomImage {
			return fmt.Errorf("instance %q: must specify either 's3' or 'customImage'", inst.ID)
		}

		if hasS3 {
			if inst.S3.Endpoint == "" {
				return fmt.Errorf("instance %q: s3.endpoint is required", inst.ID)
			}
			if inst.S3.Bucket == "" {
				return fmt.Errorf("instance %q: s3.bucket is required", inst.ID)
			}
			if inst.S3.AccessKeyID == "" {
				return fmt.Errorf("instance %q: s3.accessKeyId is required", inst.ID)
			}
			if inst.S3.SecretAccessKey == "" {
				return fmt.Errorf("instance %q: s3.secretAccessKey is required", inst.ID)
			}
		} else if inst.Encryption != nil {
			return fmt.Errorf("instance %q: encryption is only supported for s3 destinations", inst.ID)
		}

		if inst.Timeout != "" {
			if _, err := time.ParseDuration(inst.Timeout); err != nil {
				return fmt.Errorf("instance %q: timeout %q: %w", inst.ID, inst.Timeout, err)
			}
		}

		for j, target := range inst.Targets {
			hasVolume := target.Volume != ""
			hasDB := target.DB != ""
			if hasVolume && hasDB {
				return fmt.Errorf("instance %q target #%d: cannot specify both 'volume' and 'db' in the same target", inst.ID, j+1)
			}
			if !hasVolume && !hasDB {
				return fmt.Errorf("instance %q target #%d: must specify either 'volume' or 'db'", inst.ID, j+1)
			}
		}
	}

	return nil
}

// expandEnvInPlace walks every string, []string, and map[string]string
// field reachable from cfg and expands ${VAR}/$VAR references in place.
// Using reflection here (rather than a hand-maintained field-by-field list)
// means every current and future config field gets env expansion "for
// free" -- the previous field-by-field approach had already drifted out of
// sync with the schema (see HookCommand fields, which it never touched).
func expandEnvInPlace(cfg *Config) {
	walkExpand(reflect.ValueOf(cfg))
}

func walkExpand(v reflect.Value) {
	if !v.IsValid() {
		return
	}

	switch v.Kind() {
	case reflect.Ptr:
		if !v.IsNil() {
			walkExpand(v.Elem())
		}

	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).PkgPath != "" {
				continue // unexported field
			}
			walkExpand(v.Field(i))
		}

	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkExpand(v.Index(i))
		}

	case reflect.Map:
		if v.IsNil() {
			return
		}
		for _, key := range v.MapKeys() {
			val := v.MapIndex(key)
			if val.Kind() == reflect.String {
				v.SetMapIndex(key, reflect.ValueOf(expandEnv(val.String())))
			} else {
				// Rare (no map[string]struct{...} fields today), but walk
				// it anyway rather than silently skipping.
				walkExpand(val)
			}
		}

	case reflect.String:
		if v.CanSet() {
			v.SetString(expandEnv(v.String()))
		}
	}
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
