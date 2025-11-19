package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createFakeRestic writes a fake 'restic' executable into a temp dir and
// prepends that dir to PATH so backend methods invoke it.
func createFakeRestic(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "restic")
	script := `#!/bin/sh
if [ "$1" = "forget" ]; then
  echo "forget"
  echo "--prune"
  echo "--keep-daily 7"
  echo "--keep-weekly 4"
  echo "--keep-monthly 6"
  exit 0
fi
if [ "$1" = "snapshots" ]; then
  exit 1
fi
if [ "$1" = "init" ]; then
  echo "init OK"
  exit 0
fi
echo "REPO=$RESTIC_REPOSITORY"
echo "PASS=$RESTIC_PASSWORD"
echo "CUSTOM=$CUSTOM"
echo "ARGS:$@"
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		// fatal stops test immediately
		t.Fatalf("write fake restic: %v", err)
	}
	// Prepend to PATH using t.Setenv for proper cleanup
	oldPath := os.Getenv("PATH")
	newPath := dir + string(os.PathListSeparator) + oldPath
	t.Setenv("PATH", newPath)
}

func TestBackupAndRetentionBuildArgsAndEnv(t *testing.T) {
	createFakeRestic(t)
	ctx := context.Background()
	b := &ResticBackend{ID: "test", Repository: "/repo/location", Env: map[string]string{"RESTIC_PASSWORD": "pw123", "CUSTOM": "abc"}}
	if err := b.Init(ctx); err != nil {
		// Should succeed after calling 'init' because 'snapshots' fails.
		t.Fatalf("Init failed: %v", err)
	}
	out, err := b.Backup(ctx, []string{"/data/path1"}, []string{"tag1"})
	if err != nil {
		t.Fatalf("Backup error: %v", err)
	}
	if !strings.Contains(out, "REPO=/repo/location") || !strings.Contains(out, "PASS=pw123") || !strings.Contains(out, "CUSTOM=abc") {
		// validate env propagation
		t.Fatalf("environment variables not passed correctly; output: %s", out)
	}
	if !strings.Contains(out, "ARGS:--cleanup-cache backup --verbose /data/path1 --tag tag1") {
		t.Fatalf("arguments not built correctly; output: %s", out)
	}
	out2, err := b.DeleteOldSnapshots(ctx, 7, 4, 6)
	if err != nil {
		t.Fatalf("DeleteOldSnapshots error: %v", err)
	}
	for _, want := range []string{"forget", "--prune", "--keep-daily 7", "--keep-weekly 4", "--keep-monthly 6"} {
		if !strings.Contains(out2, want) {
			t.Fatalf("expected %q in output: %s", want, out2)
		}
	}
}
