package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/polarfoxDev/marina/internal/archive"
	cryptoutil "github.com/polarfoxDev/marina/internal/crypto"
	"github.com/polarfoxDev/marina/internal/helpers"
	"github.com/polarfoxDev/marina/internal/logging"
	"github.com/polarfoxDev/marina/internal/model"
)

// ManifestArtifact mirrors one entry of Snapshot.Artifacts as recorded in
// the run's manifest.json.
type ManifestArtifact struct {
	Key       string `json:"key"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
}

// Manifest is the completion marker for a snapshot: its presence (as
// manifest.json[.age]) means every artifact for that run finished
// uploading. It is written last, and is itself encrypted whenever the
// instance has a password configured, since it names every volume/DB
// backed up. Exported so marinactl (a separate binary) can read it back.
type Manifest struct {
	InstanceID    string             `json:"instanceId"`
	Node          string             `json:"node"`
	Timestamp     string             `json:"timestamp"`
	MarinaVersion string             `json:"marinaVersion"`
	Encrypted     bool               `json:"encrypted"`
	Compression   string             `json:"compression"`
	Artifacts     []ManifestArtifact `json:"artifacts"`
}

const manifestObjectName = "manifest.json"

// SnapshotRef identifies one complete snapshot (a timestamp directory whose
// manifest finished uploading).
type SnapshotRef struct {
	Timestamp string
	Time      time.Time
}

// S3Backend implements the Backend interface by archiving each artifact
// (tar+zstd, optionally age-encrypted) and streaming it directly to an S3
// compatible bucket. Its read-side methods (ListSnapshots, GetManifest,
// RestoreArtifact) are exported and reused by cmd/marinactl, so restore
// exercises exactly the same code path as backup.
type S3Backend struct {
	ID              string
	Node            string
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	UseSSL          bool
	ForcePathStyle  bool
	Password        string // empty => artifacts are stored unencrypted
	Timeout         time.Duration
	MarinaVersion   string

	client *minio.Client
	logger *logging.JobLogger
}

func (b *S3Backend) GetType() BackendType {
	return BackendTypeS3
}

func (b *S3Backend) SetLogger(logger *logging.JobLogger) {
	b.logger = logger
}

func (b *S3Backend) Close() error { return nil }

func (b *S3Backend) logf(format string, args ...any) {
	if b.logger != nil {
		b.logger.Info(format, args...)
	}
}

// EnsureClient lazily creates the underlying S3 client. Exported so
// marinactl can force connection errors to surface before doing anything
// else; Init and every other method call it automatically too.
func (b *S3Backend) EnsureClient() error {
	if b.client != nil {
		return nil
	}

	lookup := minio.BucketLookupAuto
	if b.ForcePathStyle {
		lookup = minio.BucketLookupPath
	}

	cli, err := minio.New(b.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(b.AccessKeyID, b.SecretAccessKey, ""),
		Secure:       b.UseSSL,
		Region:       b.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return fmt.Errorf("create S3 client: %w", err)
	}
	b.client = cli
	return nil
}

// Init verifies the configured bucket exists and is reachable with the
// given credentials.
func (b *S3Backend) Init(ctx context.Context) error {
	if err := b.EnsureClient(); err != nil {
		return err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, b.timeout())
	defer cancel()

	exists, err := b.client.BucketExists(timeoutCtx, b.Bucket)
	if err != nil {
		return fmt.Errorf("check bucket %q on %q: %w", b.Bucket, b.Endpoint, err)
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist (or is not visible with the configured credentials) on %q", b.Bucket, b.Endpoint)
	}
	return nil
}

func (b *S3Backend) timeout() time.Duration {
	if b.Timeout > 0 {
		return b.Timeout
	}
	return 60 * time.Minute
}

func (b *S3Backend) instancePrefix() string {
	if b.Prefix != "" {
		return path.Join(b.Prefix, b.ID)
	}
	return b.ID
}

func (b *S3Backend) snapshotPrefix(timestamp string) string {
	return b.instancePrefix() + "/" + timestamp + "/"
}

func (b *S3Backend) objectKey(timestamp, name string) string {
	return b.snapshotPrefix(timestamp) + name
}

func (b *S3Backend) encExt() string {
	if b.Password != "" {
		return ".age"
	}
	return ""
}

// Backup archives and uploads every artifact in snap, then writes the
// manifest last -- the manifest's presence is what marks the run complete
// for both ApplyRetention and marinactl.
func (b *S3Backend) Backup(ctx context.Context, snap Snapshot) (string, error) {
	if err := b.EnsureClient(); err != nil {
		return "", err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, b.timeout())
	defer cancel()

	var logLines []string
	logLine := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		logLines = append(logLines, line)
		b.logf("%s", line)
	}

	man := Manifest{
		InstanceID:    snap.InstanceID,
		Node:          snap.Node,
		Timestamp:     snap.Timestamp,
		MarinaVersion: b.MarinaVersion,
		Encrypted:     b.Password != "",
		Compression:   "zstd",
	}

	for _, art := range snap.Artifacts {
		objectName := fmt.Sprintf("%s-%s.tar.zst%s", art.Type, art.Name, b.encExt())
		key := b.objectKey(snap.Timestamp, objectName)

		logLine("uploading %s (%s) to s3://%s/%s", art.Name, art.Type, b.Bucket, key)

		size, sum, err := b.uploadArtifact(timeoutCtx, key, art)
		if err != nil {
			return strings.Join(logLines, "\n"), fmt.Errorf("upload %s %s: %w", art.Type, art.Name, err)
		}

		man.Artifacts = append(man.Artifacts, ManifestArtifact{
			Key:       objectName,
			Type:      string(art.Type),
			Name:      art.Name,
			SizeBytes: size,
			SHA256:    sum,
		})
		logLine("uploaded %s (%d bytes)", art.Name, size)
	}

	if err := b.uploadManifest(timeoutCtx, snap.Timestamp, man); err != nil {
		return strings.Join(logLines, "\n"), fmt.Errorf("upload manifest: %w", err)
	}
	logLine("manifest written; snapshot %s complete", snap.Timestamp)

	return strings.Join(logLines, "\n"), nil
}

// uploadArtifact streams art.Root through tar -> zstd -> (age) -> PutObject
// without ever buffering the whole archive on disk or in memory, and
// returns the uploaded size and a SHA-256 of the ciphertext (or plaintext,
// if unencrypted) as it appeared in the bucket.
func (b *S3Backend) uploadArtifact(ctx context.Context, key string, art Artifact) (int64, string, error) {
	pr, pw := io.Pipe()

	hasher := sha256.New()
	countingReader := io.TeeReader(pr, hasher)

	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()

		enc, err := cryptoutil.Encrypt(pw, b.Password)
		if err != nil {
			errCh <- fmt.Errorf("start encryption: %w", err)
			return
		}

		if err := archive.WriteTree(enc, art.Root); err != nil {
			enc.Close()
			errCh <- fmt.Errorf("archive %s: %w", art.Root, err)
			return
		}

		errCh <- enc.Close()
	}()

	info, putErr := b.client.PutObject(ctx, b.Bucket, key, countingReader, -1, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
		PartSize:    64 * 1024 * 1024,
	})

	// Always drain the writer goroutine's result: if PutObject failed
	// first, pr will still be closed by the writer's own error path once
	// it notices pw is gone; if the writer failed first, PutObject will
	// see the resulting pipe error. Either way, prefer the writer's error
	// since it's usually the root cause.
	writeErr := <-errCh
	if writeErr != nil {
		return 0, "", writeErr
	}
	if putErr != nil {
		return 0, "", putErr
	}

	return info.Size, hex.EncodeToString(hasher.Sum(nil)), nil
}

func (b *S3Backend) uploadManifest(ctx context.Context, timestamp string, man Manifest) error {
	data, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	var buf strings.Builder
	enc, err := cryptoutil.Encrypt(&buf, b.Password)
	if err != nil {
		return fmt.Errorf("start manifest encryption: %w", err)
	}
	if _, err := enc.Write(data); err != nil {
		enc.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("close manifest encryption: %w", err)
	}

	key := b.objectKey(timestamp, manifestObjectName+b.encExt())
	content := buf.String()
	_, err = b.client.PutObject(ctx, b.Bucket, key, strings.NewReader(content), int64(len(content)), minio.PutObjectOptions{
		ContentType: "application/json",
	})
	return err
}

// ApplyRetention lists completed snapshots for this instance, decides which
// are expired per retention, and removes every object under each expired
// snapshot's prefix. It never deletes anything if listing fails, and never
// deletes the newest complete snapshot regardless of policy (retention
// selection itself guarantees this -- see helpers.SelectExpiredSnapshots).
func (b *S3Backend) ApplyRetention(ctx context.Context, retention model.Retention) (string, error) {
	if err := b.EnsureClient(); err != nil {
		return "", err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, b.timeout())
	defer cancel()

	snapshots, err := b.ListSnapshots(timeoutCtx)
	if err != nil {
		return "", fmt.Errorf("list snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		return "no snapshots found", nil
	}

	times := make([]time.Time, 0, len(snapshots))
	byTime := map[time.Time]string{}
	for _, s := range snapshots {
		times = append(times, s.Time)
		byTime[s.Time] = s.Timestamp
	}

	expired := helpers.SelectExpiredSnapshots(times, retention)

	var logLines []string
	for _, t := range expired {
		ts := byTime[t]
		if err := b.DeleteSnapshot(timeoutCtx, ts); err != nil {
			return strings.Join(logLines, "\n"), fmt.Errorf("delete snapshot %s: %w", ts, err)
		}
		line := fmt.Sprintf("removed expired snapshot %s", ts)
		logLines = append(logLines, line)
		b.logf("%s", line)
	}

	if len(expired) == 0 {
		return "no snapshots expired", nil
	}
	return strings.Join(logLines, "\n"), nil
}

// ListSnapshots returns every complete snapshot for this instance (a
// timestamp directory whose manifest finished uploading), newest first.
// Incomplete/aborted runs -- a timestamp prefix with no manifest object --
// are silently excluded, both here and therefore from retention and
// marinactl's `list`.
func (b *S3Backend) ListSnapshots(ctx context.Context) ([]SnapshotRef, error) {
	if err := b.EnsureClient(); err != nil {
		return nil, err
	}

	prefix := b.instancePrefix() + "/"

	var refs []SnapshotRef
	seen := map[string]bool{}
	for obj := range b.client.ListObjects(ctx, b.Bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		rest := strings.TrimPrefix(obj.Key, prefix)
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 {
			continue
		}
		ts, objName := parts[0], parts[1]
		if objName != manifestObjectName && objName != manifestObjectName+".age" {
			continue
		}
		if seen[ts] {
			continue
		}
		parsed, err := time.Parse("20060102-150405", ts)
		if err != nil {
			continue
		}
		seen[ts] = true
		refs = append(refs, SnapshotRef{Timestamp: ts, Time: parsed})
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].Time.After(refs[j].Time) })
	return refs, nil
}

// GetManifest fetches and decrypts (if needed) the manifest for a
// snapshot.
func (b *S3Backend) GetManifest(ctx context.Context, timestamp string) (*Manifest, error) {
	if err := b.EnsureClient(); err != nil {
		return nil, err
	}

	key := b.objectKey(timestamp, manifestObjectName+b.encExt())
	obj, err := b.client.GetObject(ctx, b.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer obj.Close()

	plain, err := cryptoutil.Decrypt(obj, b.Password)
	if err != nil {
		return nil, fmt.Errorf("decrypt manifest: %w", err)
	}

	data, err := io.ReadAll(plain)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var man Manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &man, nil
}

// RestoreArtifact downloads, decrypts, decompresses, and extracts one
// artifact into destRoot, verifying the manifest's SHA-256 as it streams.
// destRoot should already be scoped to the artifact (e.g.
// <out>/<type>/<name>) by the caller.
//
// The checksum is computed over the object exactly as it was uploaded
// (i.e. post-encryption when the instance has a password), matching how
// uploadArtifact computed art.SHA256 in the first place -- both sides hash
// the same bytes.
//
// archive.ExtractTree only reads as much of the decompressed tar stream as
// it needs to reach the end-of-archive marker, which can stop short of the
// object's true end (trailing zstd frame metadata, etc.). checkedReader
// tees directly off the raw object body, one layer below decryption and
// decompression, so after extraction we explicitly drain whatever it
// hasn't been asked for yet -- that's what makes the hash cover the whole
// object rather than just the prefix the tar reader happened to consume.
func (b *S3Backend) RestoreArtifact(ctx context.Context, timestamp string, art ManifestArtifact, destRoot string) error {
	if err := b.EnsureClient(); err != nil {
		return err
	}

	key := b.objectKey(timestamp, art.Key)
	obj, err := b.client.GetObject(ctx, b.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("fetch %s: %w", art.Key, err)
	}
	defer obj.Close()

	hasher := sha256.New()
	checkedReader := io.TeeReader(obj, hasher)

	plain, err := cryptoutil.Decrypt(checkedReader, b.Password)
	if err != nil {
		return fmt.Errorf("decrypt %s: %w", art.Key, err)
	}

	if err := archive.ExtractTree(plain, destRoot); err != nil {
		return fmt.Errorf("extract %s: %w", art.Key, err)
	}

	// Drain any bytes the decompression/decryption chain didn't need to
	// read, so the hash below covers the entire object.
	if _, err := io.Copy(io.Discard, checkedReader); err != nil {
		return fmt.Errorf("drain %s: %w", art.Key, err)
	}

	if art.SHA256 != "" {
		if got := hex.EncodeToString(hasher.Sum(nil)); got != art.SHA256 {
			return fmt.Errorf("checksum mismatch for %s: manifest says %s, got %s (object may be corrupted or tampered with)", art.Key, art.SHA256, got)
		}
	}
	return nil
}

// DeleteSnapshot removes every object under one snapshot's prefix.
func (b *S3Backend) DeleteSnapshot(ctx context.Context, timestamp string) error {
	if err := b.EnsureClient(); err != nil {
		return err
	}

	prefix := b.snapshotPrefix(timestamp)

	objectsCh := make(chan minio.ObjectInfo)
	go func() {
		defer close(objectsCh)
		for obj := range b.client.ListObjects(ctx, b.Bucket, minio.ListObjectsOptions{
			Prefix:    prefix,
			Recursive: true,
		}) {
			if obj.Err != nil {
				continue
			}
			objectsCh <- obj
		}
	}()

	var firstErr error
	for rmErr := range b.client.RemoveObjects(ctx, b.Bucket, objectsCh, minio.RemoveObjectsOptions{}) {
		if rmErr.Err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove %s: %w", rmErr.ObjectName, rmErr.Err)
		}
	}
	return firstErr
}

var _ Backend = (*S3Backend)(nil)
