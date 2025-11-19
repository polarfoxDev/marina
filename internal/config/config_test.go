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
     targets:
       - volume: app-data
         paths: ["/"]
   - id: local
     repository: /mnt/backup/restic
     schedule: "0 3 * * *"
     env:
       RESTIC_PASSWORD: direct
     targets:
       - db: postgres
         dbKind: postgres
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
	// Check targets
	if len(d.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(d.Targets))
	}
	if d.Targets[0].Volume != "app-data" {
		t.Fatalf("unexpected volume name: %q", d.Targets[0].Volume)
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
     targets:
       - volume: custom-data
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
	// Check targets
	if len(d.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(d.Targets))
	}
	if d.Targets[0].Volume != "custom-data" {
		t.Fatalf("unexpected volume name: %q", d.Targets[0].Volume)
	}
}

func TestLoad_TargetEnvExpansion(t *testing.T) {
	t.Setenv("DB_NAME", "my-postgres")
	t.Setenv("VOL_NAME", "my-volume")
	t.Setenv("DB_PASSWORD", "secret123")
	cfgYAML := `
 instances:
   - id: test
     repository: /tmp/backup
     schedule: "0 2 * * *"
     env:
       RESTIC_PASSWORD: test
     targets:
       - volume: ${VOL_NAME}
         preHook: "echo starting"
       - db: ${DB_NAME}
         dbKind: postgres
         dumpArgs: ["-U", "postgres", "-p${DB_PASSWORD}"]
`
	p := writeTempConfig(t, cfgYAML)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	d, err := cfg.GetDestination("test")
	if err != nil {
		t.Fatalf("GetDestination error: %v", err)
	}
	if len(d.Targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(d.Targets))
	}
	// Check volume target
	if d.Targets[0].Volume != "my-volume" {
		t.Fatalf("volume name not expanded: %q", d.Targets[0].Volume)
	}
	if d.Targets[0].PreHook != "echo starting" {
		t.Fatalf("preHook not expanded: %q", d.Targets[0].PreHook)
	}
	// Check DB target
	if d.Targets[1].DB != "my-postgres" {
		t.Fatalf("db name not expanded: %q", d.Targets[1].DB)
	}
	if len(d.Targets[1].DumpArgs) != 3 {
		t.Fatalf("expected 3 dump args, got %d", len(d.Targets[1].DumpArgs))
	}
	if d.Targets[1].DumpArgs[2] != "-psecret123" {
		t.Fatalf("dumpArgs not expanded: %q", d.Targets[1].DumpArgs[2])
	}
}

func TestLoad_Targets(t *testing.T) {
	cfgYAML := `
 instances:
   - id: test
     repository: /tmp/backup
     schedule: "0 2 * * *"
     env:
       RESTIC_PASSWORD: test
     targets:
       - volume: app-data
       - db: postgres
       - volume: full-config-volume
         paths: ["/data"]
       - db: full-config-db
         dbKind: mysql
         dumpArgs: ["-uroot"]
`
	p := writeTempConfig(t, cfgYAML)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	d, err := cfg.GetDestination("test")
	if err != nil {
		t.Fatalf("GetDestination error: %v", err)
	}
	if len(d.Targets) != 4 {
		t.Fatalf("expected 4 targets, got %d", len(d.Targets))
	}
	// Check volume target
	if d.Targets[0].Volume != "app-data" {
		t.Fatalf("volume not parsed: %q", d.Targets[0].Volume)
	}
	if d.Targets[0].DB != "" {
		t.Fatalf("volume should not have DB set")
	}
	// Check DB target
	if d.Targets[1].DB != "postgres" {
		t.Fatalf("db not parsed: %q", d.Targets[1].DB)
	}
	if d.Targets[1].Volume != "" {
		t.Fatalf("db should not have Volume set")
	}
	if d.Targets[1].DBKind != "" {
		t.Fatalf("db should not have DBKind set initially (auto-detected later), got %q", d.Targets[1].DBKind)
	}
	// Check full config volume target
	if d.Targets[2].Volume != "full-config-volume" {
		t.Fatalf("full config volume not parsed: %q", d.Targets[2].Volume)
	}
	if len(d.Targets[2].Paths) != 1 || d.Targets[2].Paths[0] != "/data" {
		t.Fatalf("full config volume paths not parsed: %v", d.Targets[2].Paths)
	}
	// Check full config DB target
	if d.Targets[3].DB != "full-config-db" {
		t.Fatalf("full config db not parsed: %q", d.Targets[3].DB)
	}
	if d.Targets[3].DBKind != "mysql" {
		t.Fatalf("full config db dbKind not parsed: %q", d.Targets[3].DBKind)
	}
	if len(d.Targets[3].DumpArgs) != 1 || d.Targets[3].DumpArgs[0] != "-uroot" {
		t.Fatalf("full config db dumpArgs not parsed: %v", d.Targets[3].DumpArgs)
	}
}

func TestLoad_MeshConfigEnvExpansion(t *testing.T) {
	t.Setenv("NODE_NAME", "test-node")
	t.Setenv("MARINA_AUTH_PASSWORD", "secret123")
	t.Setenv("PEER_1", "http://peer1:8080")
	t.Setenv("PEER_2", "http://peer2:8080")
	cfgYAML := `
 instances:
   - id: test
     repository: /tmp/backup
     schedule: "0 2 * * *"
     env:
       RESTIC_PASSWORD: test
     targets:
       - volume: app-data
 mesh:
   nodeName: ${NODE_NAME}
   authPassword: ${MARINA_AUTH_PASSWORD}
   peers:
     - ${PEER_1}
     - ${PEER_2}
`
	p := writeTempConfig(t, cfgYAML)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Mesh == nil {
		t.Fatalf("mesh config not parsed")
	}
	if cfg.Mesh.NodeName != "test-node" {
		t.Fatalf("mesh nodeName not expanded: %q", cfg.Mesh.NodeName)
	}
	if cfg.Mesh.AuthPassword != "secret123" {
		t.Fatalf("mesh authPassword not expanded: %q", cfg.Mesh.AuthPassword)
	}
	if len(cfg.Mesh.Peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(cfg.Mesh.Peers))
	}
	if cfg.Mesh.Peers[0] != "http://peer1:8080" {
		t.Fatalf("peer 1 not expanded: %q", cfg.Mesh.Peers[0])
	}
	if cfg.Mesh.Peers[1] != "http://peer2:8080" {
		t.Fatalf("peer 2 not expanded: %q", cfg.Mesh.Peers[1])
	}
}

func TestLoad_GlobalRetention(t *testing.T) {
	cfgYAML := `
 instances:
   - id: with-retention
     repository: /tmp/backup1
     schedule: "0 2 * * *"
     retention: "30d:12w:24m"
     env:
       RESTIC_PASSWORD: test
     targets:
       - volume: app-data
   - id: without-retention
     repository: /tmp/backup2
     schedule: "0 3 * * *"
     env:
       RESTIC_PASSWORD: test
     targets:
       - volume: other-data
 retention: "14d:8w:12m"
`
	p := writeTempConfig(t, cfgYAML)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	// Check global retention is set
	if cfg.Retention != "14d:8w:12m" {
		t.Fatalf("global retention not parsed: %q", cfg.Retention)
	}
	// Check instance with retention keeps its value
	d1, err := cfg.GetDestination("with-retention")
	if err != nil {
		t.Fatalf("GetDestination error: %v", err)
	}
	if d1.Retention != "30d:12w:24m" {
		t.Fatalf("instance retention not preserved: %q", d1.Retention)
	}
	// Check instance without retention has empty string (global is applied in scheduler)
	d2, err := cfg.GetDestination("without-retention")
	if err != nil {
		t.Fatalf("GetDestination error: %v", err)
	}
	if d2.Retention != "" {
		t.Fatalf("instance without retention should be empty string, got: %q", d2.Retention)
	}
}

func TestLoad_RuntimeConfigEnvExpansion(t *testing.T) {
	t.Setenv("DB_PATH", "/custom/marina.db")
	t.Setenv("API_PORT", "9090")
	t.Setenv("CORS_ORIGIN_1", "https://app1.example.com")
	t.Setenv("CORS_ORIGIN_2", "https://app2.example.com")
	cfgYAML := `
 instances:
   - id: test
     repository: /tmp/backup
     schedule: "0 2 * * *"
     env:
       RESTIC_PASSWORD: test
     targets:
       - volume: app-data
 dbPath: ${DB_PATH}
 apiPort: ${API_PORT}
 corsOrigins:
   - ${CORS_ORIGIN_1}
   - ${CORS_ORIGIN_2}
`
	p := writeTempConfig(t, cfgYAML)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.DBPath != "/custom/marina.db" {
		t.Fatalf("dbPath not expanded: %q", cfg.DBPath)
	}
	if cfg.APIPort != "9090" {
		t.Fatalf("apiPort not expanded: %q", cfg.APIPort)
	}
	if len(cfg.CorsOrigins) != 2 {
		t.Fatalf("expected 2 CORS origins, got %d", len(cfg.CorsOrigins))
	}
	if cfg.CorsOrigins[0] != "https://app1.example.com" {
		t.Fatalf("CORS origin 1 not expanded: %q", cfg.CorsOrigins[0])
	}
	if cfg.CorsOrigins[1] != "https://app2.example.com" {
		t.Fatalf("CORS origin 2 not expanded: %q", cfg.CorsOrigins[1])
	}
}
