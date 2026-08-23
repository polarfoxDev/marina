package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/polarfoxDev/marina/internal/model"
)

// startMinio launches a throwaway MinIO container for the duration of the
// test and returns its endpoint (host:port) plus root credentials. Tests
// that need it skip cleanly if the docker CLI isn't available, rather than
// failing -- these are the only backend tests that touch a real (if local)
// object store.
func startMinio(t *testing.T) (endpoint, accessKey, secretKey string) {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available, skipping S3Backend integration test")
	}

	accessKey = "minioadmin"
	secretKey = "minioadmin"
	containerName := fmt.Sprintf("marina-s3-test-%d", time.Now().UnixNano())

	cmd := exec.Command("docker", "run", "-d", "--rm",
		"-P", // publish all exposed ports to random host ports
		"-e", "MINIO_ROOT_USER="+accessKey,
		"-e", "MINIO_ROOT_PASSWORD="+secretKey,
		"--name", containerName,
		"minio/minio", "server", "/data")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("failed to start minio container (docker not usable here?): %v\n%s", err, out)
	}

	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
	})

	portOut, err := exec.Command("docker", "port", containerName, "9000/tcp").Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	// Output looks like "0.0.0.0:54321\n[::]:54321\n" -- take the first line's port.
	firstLine := strings.SplitN(strings.TrimSpace(string(portOut)), "\n", 2)[0]
	parts := strings.Split(firstLine, ":")
	hostPort := parts[len(parts)-1]
	endpoint = "127.0.0.1:" + hostPort

	// Wait for MinIO to become reachable.
	deadline := time.Now().Add(30 * time.Second)
	for {
		cli, err := minio.New(endpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
			Secure: false,
		})
		if err == nil {
			if _, err := cli.ListBuckets(context.Background()); err == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("minio did not become ready in time")
		}
		time.Sleep(300 * time.Millisecond)
	}

	return endpoint, accessKey, secretKey
}

func newTestS3Backend(t *testing.T, bucket, password string) *S3Backend {
	t.Helper()
	endpoint, accessKey, secretKey := startMinio(t)

	rawCli, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("create raw minio client: %v", err)
	}
	if err := rawCli.MakeBucket(context.Background(), bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	return &S3Backend{
		ID:              "test-instance",
		Node:            "test-node",
		Endpoint:        endpoint,
		Bucket:          bucket,
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		UseSSL:          false,
		ForcePathStyle:  true,
		Password:        password,
		MarinaVersion:   "test",
	}
}

func writeStagedFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func TestS3Backend_InterfaceCompliance(t *testing.T) {
	var _ Backend = (*S3Backend)(nil)
}

func TestS3Backend_InitFailsOnMissingBucket(t *testing.T) {
	endpoint, accessKey, secretKey := startMinio(t)
	b := &S3Backend{
		ID:              "test",
		Endpoint:        endpoint,
		Bucket:          "does-not-exist",
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		ForcePathStyle:  true,
	}
	if err := b.Init(context.Background()); err == nil {
		t.Fatal("expected Init to fail for a bucket that doesn't exist")
	}
}

func TestS3Backend_BackupKeyNamingAndManifest_Unencrypted(t *testing.T) {
	b := newTestS3Backend(t, "unencrypted-bucket", "")
	ctx := context.Background()

	if err := b.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	stagingRoot := t.TempDir()
	volRoot := filepath.Join(stagingRoot, "volume", "app-uploads")
	writeStagedFile(t, volRoot, "file.txt", "hello volume")
	dbRoot := filepath.Join(stagingRoot, "db", "app-postgres")
	writeStagedFile(t, dbRoot, "dump.sql", "-- dump")

	snap := Snapshot{
		InstanceID: b.ID,
		Node:       "test-node",
		Timestamp:  "20260823-020000",
		Artifacts: []Artifact{
			{Type: model.TargetVolume, Name: "app-uploads", Root: volRoot},
			{Type: model.TargetDB, Name: "app-postgres", Root: dbRoot},
		},
	}

	if _, err := b.Backup(ctx, snap); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	rawCli, err := minio.New(b.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(b.AccessKeyID, b.SecretAccessKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("create raw client: %v", err)
	}

	expectedKeys := []string{
		"test-instance/20260823-020000/volume-app-uploads.tar.zst",
		"test-instance/20260823-020000/db-app-postgres.tar.zst",
		"test-instance/20260823-020000/manifest.json",
	}
	for _, key := range expectedKeys {
		if _, err := rawCli.StatObject(ctx, b.Bucket, key, minio.StatObjectOptions{}); err != nil {
			t.Errorf("expected object %q to exist: %v", key, err)
		}
	}

	// Manifest should be plain, valid JSON when unencrypted, and list both artifacts.
	obj, err := rawCli.GetObject(ctx, b.Bucket, "test-instance/20260823-020000/manifest.json", minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	data, err := io.ReadAll(obj)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var man Manifest
	if err := json.Unmarshal(data, &man); err != nil {
		t.Fatalf("manifest is not valid JSON when unencrypted: %v\n%s", err, data)
	}
	if man.Encrypted {
		t.Error("manifest.Encrypted should be false")
	}
	if len(man.Artifacts) != 2 {
		t.Fatalf("expected 2 artifacts in manifest, got %d", len(man.Artifacts))
	}

	// GetManifest (the exported, decrypt-aware accessor used by marinactl)
	// should agree.
	gotMan, err := b.GetManifest(ctx, "20260823-020000")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if gotMan.InstanceID != b.ID || len(gotMan.Artifacts) != 2 {
		t.Fatalf("unexpected manifest via GetManifest: %#v", gotMan)
	}
}

func TestS3Backend_BackupIsEncryptedWhenPasswordSet(t *testing.T) {
	b := newTestS3Backend(t, "encrypted-bucket", "s3cr3t-password")
	ctx := context.Background()

	if err := b.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	stagingRoot := t.TempDir()
	volRoot := filepath.Join(stagingRoot, "volume", "app-uploads")
	writeStagedFile(t, volRoot, "file.txt", "hello volume")

	snap := Snapshot{
		InstanceID: b.ID,
		Timestamp:  "20260823-030000",
		Artifacts: []Artifact{
			{Type: model.TargetVolume, Name: "app-uploads", Root: volRoot},
		},
	}
	if _, err := b.Backup(ctx, snap); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	rawCli, err := minio.New(b.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(b.AccessKeyID, b.SecretAccessKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("create raw client: %v", err)
	}

	// .age suffix, and the manifest object must not be plain JSON.
	if _, err := rawCli.StatObject(ctx, b.Bucket, "test-instance/20260823-030000/volume-app-uploads.tar.zst.age", minio.StatObjectOptions{}); err != nil {
		t.Fatalf("expected encrypted artifact object: %v", err)
	}
	obj, err := rawCli.GetObject(ctx, b.Bucket, "test-instance/20260823-030000/manifest.json.age", minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("get encrypted manifest: %v", err)
	}
	data, err := io.ReadAll(obj)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var probe Manifest
	if json.Unmarshal(data, &probe) == nil {
		t.Fatal("encrypted manifest should not parse as plain JSON")
	}

	// But GetManifest with the right password decrypts it fine.
	man, err := b.GetManifest(ctx, "20260823-030000")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if !man.Encrypted || len(man.Artifacts) != 1 {
		t.Fatalf("unexpected decrypted manifest: %#v", man)
	}

	// Restoring the artifact round-trips the original content.
	restoreDir := t.TempDir()
	if err := b.RestoreArtifact(ctx, "20260823-030000", man.Artifacts[0], restoreDir); err != nil {
		t.Fatalf("RestoreArtifact: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(restoreDir, "file.txt"))
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(got) != "hello volume" {
		t.Fatalf("restored content = %q, want %q", got, "hello volume")
	}
}

func TestS3Backend_IncompleteRunHasNoManifestAndIsInvisible(t *testing.T) {
	b := newTestS3Backend(t, "incomplete-bucket", "")
	ctx := context.Background()

	if err := b.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Simulate an aborted run: upload one artifact object directly,
	// without ever writing a manifest (as if Backup failed partway
	// through, e.g. on a later artifact).
	rawCli, err := minio.New(b.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(b.AccessKeyID, b.SecretAccessKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("create raw client: %v", err)
	}
	body := bytes.NewReader([]byte("partial"))
	_, err = rawCli.PutObject(ctx, b.Bucket, "test-instance/20260101-000000/volume-x.tar.zst", body, int64(body.Len()), minio.PutObjectOptions{})
	if err != nil {
		t.Fatalf("seed partial object: %v", err)
	}

	refs, err := b.ListSnapshots(ctx)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected the manifest-less run to be invisible, got %v", refs)
	}
}

func TestS3Backend_ApplyRetention_NeverDeletesNewestOrIncomplete(t *testing.T) {
	b := newTestS3Backend(t, "retention-bucket", "")
	ctx := context.Background()
	if err := b.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	stagingRoot := t.TempDir()

	backupAt := func(ts string) {
		volRoot := filepath.Join(stagingRoot, ts, "volume", "v")
		writeStagedFile(t, volRoot, "f.txt", "data-"+ts)
		snap := Snapshot{
			InstanceID: b.ID,
			Timestamp:  ts,
			Artifacts: []Artifact{
				{Type: model.TargetVolume, Name: "v", Root: volRoot},
			},
		}
		if _, err := b.Backup(ctx, snap); err != nil {
			t.Fatalf("Backup(%s): %v", ts, err)
		}
	}

	// Three complete daily snapshots, oldest to newest.
	backupAt("20260101-020000")
	backupAt("20260102-020000")
	backupAt("20260103-020000")

	// One incomplete run, older than everything -- must survive retention
	// untouched since it was never "seen" as a snapshot at all.
	rawCli, err := minio.New(b.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(b.AccessKeyID, b.SecretAccessKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("create raw client: %v", err)
	}
	body := bytes.NewReader([]byte("partial"))
	incompleteKey := "test-instance/20251231-020000/volume-v.tar.zst"
	if _, err := rawCli.PutObject(ctx, b.Bucket, incompleteKey, body, int64(body.Len()), minio.PutObjectOptions{}); err != nil {
		t.Fatalf("seed incomplete object: %v", err)
	}

	// KeepDaily: 1 should expire the two older complete snapshots and
	// keep only the newest.
	if _, err := b.ApplyRetention(ctx, model.Retention{KeepDaily: 1}); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}

	refs, err := b.ListSnapshots(ctx)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(refs) != 1 || refs[0].Timestamp != "20260103-020000" {
		t.Fatalf("expected only the newest snapshot to remain, got %v", refs)
	}

	// The incomplete run's object must still be there -- retention only
	// ever acts on complete (manifest-having) snapshots.
	if _, err := rawCli.StatObject(ctx, b.Bucket, incompleteKey, minio.StatObjectOptions{}); err != nil {
		t.Fatalf("expected incomplete run's object to survive retention: %v", err)
	}

	// And a zero-policy retention must still never delete the last snapshot.
	if _, err := b.ApplyRetention(ctx, model.Retention{}); err != nil {
		t.Fatalf("ApplyRetention with zero policy: %v", err)
	}
	refs, err = b.ListSnapshots(ctx)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("zero-policy retention deleted the last remaining snapshot: %v", refs)
	}
}
