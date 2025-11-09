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
 destinaTions:  # yaml is case-insensitive structurally? keep correct key
 destinations:
   - id: hetzner-s3
     repository: s3:https://fsn1.example.com/bucket
     env:
       AWS_ACCESS_KEY_ID: ${AWS_KEY}
       AWS_SECRET_ACCESS_KEY: $AWS_SECRET
       RESTIC_PASSWORD: ${RESTIC_PASS}
   - id: local
     repository: /mnt/backup/restic
     env:
       RESTIC_PASSWORD: direct
 defaultSchedule: "0 2 * * *"
 defaultRetention: "14d:8w:12m"
 defaultStopAttached: true
`
	p := writeTempConfig(t, cfgYAML)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Destinations) != 2 {
		t.Fatalf("expected 2 destinations, got %d", len(cfg.Destinations))
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
