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
	t.Setenv("ENC_PASS", "enc-pass")
	cfgYAML := `
instances:
  - id: hetzner-s3
    s3:
      endpoint: fsn1.example.com
      bucket: marina-backups
      accessKeyId: ${AWS_KEY}
      secretAccessKey: $AWS_SECRET
    encryption:
      password: ${ENC_PASS}
    schedule: "0 2 * * *"
    targets:
      - volume: app-data
        paths: ["/"]
  - id: other-bucket
    s3:
      endpoint: fsn1.example.com
      bucket: other-bucket
      accessKeyId: key
      secretAccessKey: secret
    schedule: "0 3 * * *"
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
	if d.S3 == nil {
		t.Fatalf("expected s3 config to be set")
	}
	if d.S3.AccessKeyID != "key123" || d.S3.SecretAccessKey != "sec456" {
		t.Fatalf("s3 credentials not expanded: %#v", d.S3)
	}
	if d.Encryption == nil || d.Encryption.Password != "enc-pass" {
		t.Fatalf("encryption password not expanded: %#v", d.Encryption)
	}
	if d.S3.Endpoint != "fsn1.example.com" {
		t.Fatalf("unexpected endpoint: %q", d.S3.Endpoint)
	}
	// Check targets
	if len(d.Targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(d.Targets))
	}
	if d.Targets[0].Volume != "app-data" {
		t.Fatalf("unexpected volume name: %q", d.Targets[0].Volume)
	}
	// Also check pointer identity behavior by mutating via returned pointer
	d.S3.Region = "test-mutate"
	d2, _ := cfg.GetDestination("hetzner-s3")
	if d2.S3.Region != "test-mutate" {
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
    s3:
      endpoint: example.com
      bucket: bucket
      accessKeyId: key
      secretAccessKey: secret
    schedule: "0 2 * * *"
    targets:
      - volume: ${VOL_NAME}
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

func TestLoad_HookCommandEnvExpansion(t *testing.T) {
	// Regression test: the old field-by-field expandEnv never touched
	// HookCommand fields at all. The reflection-based walk must.
	t.Setenv("HOOK_CONTAINER", "app-postgres")
	t.Setenv("HOOK_IMAGE", "ubuntu:latest")
	t.Setenv("HOOK_CMD", "echo hi")
	cfgYAML := `
instances:
  - id: test
    s3:
      endpoint: example.com
      bucket: bucket
      accessKeyId: key
      secretAccessKey: secret
    schedule: "0 2 * * *"
    targets:
      - volume: data
    beforeBackup:
      - container: ${HOOK_CONTAINER}
        command: ${HOOK_CMD}
    afterBackup:
      - image: ${HOOK_IMAGE}
        command: ${HOOK_CMD}
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
	if len(d.BeforeBackup) != 1 || d.BeforeBackup[0].Container != "app-postgres" {
		t.Fatalf("beforeBackup container not expanded: %#v", d.BeforeBackup)
	}
	if d.BeforeBackup[0].Command != "echo hi" {
		t.Fatalf("beforeBackup command not expanded: %#v", d.BeforeBackup)
	}
	if len(d.AfterBackup) != 1 || d.AfterBackup[0].Image != "ubuntu:latest" {
		t.Fatalf("afterBackup image not expanded: %#v", d.AfterBackup)
	}
}

func TestLoad_Targets(t *testing.T) {
	cfgYAML := `
instances:
  - id: test
    s3:
      endpoint: example.com
      bucket: bucket
      accessKeyId: key
      secretAccessKey: secret
    schedule: "0 2 * * *"
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
    s3:
      endpoint: example.com
      bucket: bucket
      accessKeyId: key
      secretAccessKey: secret
    schedule: "0 2 * * *"
    targets:
      - volume: app-data
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
	if cfg.NodeName != "test-node" {
		t.Fatalf("nodeName not expanded: %q", cfg.NodeName)
	}
	if cfg.AuthPassword != "secret123" {
		t.Fatalf("authPassword not expanded: %q", cfg.AuthPassword)
	}
	if len(cfg.Peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(cfg.Peers))
	}
	if cfg.Peers[0] != "http://peer1:8080" {
		t.Fatalf("peer 1 not expanded: %q", cfg.Peers[0])
	}
	if cfg.Peers[1] != "http://peer2:8080" {
		t.Fatalf("peer 2 not expanded: %q", cfg.Peers[1])
	}
}

func TestLoad_GlobalRetention(t *testing.T) {
	cfgYAML := `
instances:
  - id: with-retention
    s3:
      endpoint: example.com
      bucket: bucket1
      accessKeyId: key
      secretAccessKey: secret
    schedule: "0 2 * * *"
    retention: "30d:12w:24m"
    targets:
      - volume: app-data
  - id: without-retention
    s3:
      endpoint: example.com
      bucket: bucket2
      accessKeyId: key
      secretAccessKey: secret
    schedule: "0 3 * * *"
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
    s3:
      endpoint: example.com
      bucket: bucket
      accessKeyId: key
      secretAccessKey: secret
    schedule: "0 2 * * *"
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

func TestValidate_RejectsBothS3AndCustomImage(t *testing.T) {
	cfg := &Config{Instances: []BackupInstance{{
		ID:          "test",
		S3:          &S3Config{Endpoint: "e", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s"},
		CustomImage: "image:latest",
		Schedule:    "0 2 * * *",
	}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when both s3 and customImage are set")
	}
}

func TestValidate_RejectsNeitherS3NorCustomImage(t *testing.T) {
	cfg := &Config{Instances: []BackupInstance{{ID: "test", Schedule: "0 2 * * *"}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when neither s3 nor customImage is set")
	}
}

func TestValidate_RejectsIncompleteS3Config(t *testing.T) {
	cases := []S3Config{
		{Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s"},   // missing endpoint
		{Endpoint: "e", AccessKeyID: "k", SecretAccessKey: "s"}, // missing bucket
		{Endpoint: "e", Bucket: "b", SecretAccessKey: "s"},      // missing accessKeyId
		{Endpoint: "e", Bucket: "b", AccessKeyID: "k"},          // missing secretAccessKey
	}
	for i, s3 := range cases {
		cfg := &Config{Instances: []BackupInstance{{ID: "test", S3: &s3, Schedule: "0 2 * * *"}}}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("case %d: expected error for incomplete s3 config %#v", i, s3)
		}
	}
}

func TestValidate_RejectsBadTimeout(t *testing.T) {
	s3 := S3Config{Endpoint: "e", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s"}
	cfg := &Config{Instances: []BackupInstance{{ID: "test", S3: &s3, Schedule: "0 2 * * *", Timeout: "not-a-duration"}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for malformed instance timeout")
	}

	cfg2 := &Config{Timeout: "not-a-duration"}
	if err := cfg2.Validate(); err == nil {
		t.Fatal("expected error for malformed global timeout")
	}
}

func TestValidate_RejectsDuplicateInstanceIDs(t *testing.T) {
	s3a := S3Config{Endpoint: "e", Bucket: "a", AccessKeyID: "k", SecretAccessKey: "s"}
	s3b := S3Config{Endpoint: "e", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s"}
	cfg := &Config{Instances: []BackupInstance{
		{ID: "dup", S3: &s3a, Schedule: "0 2 * * *"},
		{ID: "dup", S3: &s3b, Schedule: "0 3 * * *"},
	}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for duplicate instance ids")
	}
}

func TestValidate_RejectsMissingID(t *testing.T) {
	s3 := S3Config{Endpoint: "e", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s"}
	cfg := &Config{Instances: []BackupInstance{{S3: &s3, Schedule: "0 2 * * *"}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing instance id")
	}
}

func TestValidate_RejectsEncryptionOnCustomImage(t *testing.T) {
	cfg := &Config{Instances: []BackupInstance{{
		ID:          "test",
		CustomImage: "image:latest",
		Encryption:  &EncryptionConfig{Password: "pw"},
		Schedule:    "0 2 * * *",
	}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for encryption set on a custom-image instance")
	}
}

func TestValidate_AcceptsValidS3Config(t *testing.T) {
	s3 := S3Config{Endpoint: "e", Bucket: "b", AccessKeyID: "k", SecretAccessKey: "s"}
	cfg := &Config{Instances: []BackupInstance{{
		ID:       "test",
		S3:       &s3,
		Schedule: "0 2 * * *",
		Targets:  []TargetConfig{{Volume: "data"}},
	}}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
