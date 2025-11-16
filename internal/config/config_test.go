package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoad_EnvExpansionAndGetDestination(t *testing.T) {
	t.Setenv("AWS_KEY", "key123")
	t.Setenv("AWS_SECRET", "sec456")
	t.Setenv("RESTIC_PASS", "restic-pass")
	cfgYAML := `
 instances:
   - id: hetzner-s3
     repository: s3:https://fsn1.example.com/bucket
     schedule: "0 2 * * *"
     env:
       AWS_ACCESS_KEY_ID: ${AWS_KEY}
       AWS_SECRET_ACCESS_KEY: $AWS_SECRET
       RESTIC_PASSWORD: ${RESTIC_PASS}
   - id: local
     repository: /mnt/backup/restic
     schedule: "0 3 * * *"
     env:
       RESTIC_PASSWORD: direct
 retention: "14d:8w:12m"
 stopAttached: true
`
	p := writeTempConfig(t, cfgYAML)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Instances) != 2 {
		t.Fatalf("expected 2 destinations, got %d", len(cfg.Instances))
	}
	d, err := cfg.GetDestination("hetzner-s3")
	if err != nil {
		t.Fatalf("GetDestination error: %v", err)
	}
	if d.Env["AWS_ACCESS_KEY_ID"] != "key123" || d.Env["AWS_SECRET_ACCESS_KEY"] != "sec456" || d.Env["RESTIC_PASSWORD"] != "restic-pass" {
		t.Fatalf("env not expanded: %#v", d.Env)
	}
	if d.Repository != "s3:https://fsn1.example.com/bucket" {
		t.Fatalf("unexpected repository: %q", d.Repository)
	}
	// Also check pointer identity behavior by mutating via returned pointer
	d.Env["TEST_MUTATE"] = "ok"
	d2, _ := cfg.GetDestination("hetzner-s3")
	if d2.Env["TEST_MUTATE"] != "ok" {
		t.Fatalf("GetDestination should return pointer to slice element")
	}
}

func TestLoad_CustomImageBackend(t *testing.T) {
	t.Setenv("CUSTOM_IMAGE", "myrepo/backup:latest")
	t.Setenv("BACKUP_TOKEN", "token456")
	cfgYAML := `
 instances:
   - id: custom-backup
     customImage: ${CUSTOM_IMAGE}
     schedule: "0 4 * * *"
     env:
       BACKUP_TOKEN: ${BACKUP_TOKEN}
       BACKUP_ENDPOINT: https://backup.example.com
 retention: "7d:4w:6m"
`
	p := writeTempConfig(t, cfgYAML)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(cfg.Instances))
	}
	d, err := cfg.GetDestination("custom-backup")
	if err != nil {
		t.Fatalf("GetDestination error: %v", err)
	}
	if d.CustomImage != "myrepo/backup:latest" {
		t.Fatalf("unexpected customImage: %q", d.CustomImage)
	}
	if d.Env["BACKUP_TOKEN"] != "token456" {
		t.Fatalf("env not expanded: %#v", d.Env)
	}
	if d.Env["BACKUP_ENDPOINT"] != "https://backup.example.com" {
		t.Fatalf("unexpected env: %#v", d.Env)
	}
}
