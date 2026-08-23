package backend

import (
	"context"

	"github.com/polarfoxDev/marina/internal/logging"
	"github.com/polarfoxDev/marina/internal/model"
)

type BackendType string

const (
	BackendTypeS3          BackendType = "s3"
	BackendTypeCustomImage BackendType = "custom"
)

// Artifact is a single staged tree (one volume or one database dump) that
// should be archived and uploaded as part of a Snapshot.
type Artifact struct {
	Type model.TargetType // volume|db
	Name string           // volume name or DB container name
	Root string           // host path to the staged tree, e.g. /backup/<instance>/<ts>/volume/<name>
}

// Snapshot describes one backup run: everything staged for a single
// instance at a single point in time.
type Snapshot struct {
	InstanceID string
	Node       string
	Timestamp  string // 20060102-150405, shared with the staging directory name
	Artifacts  []Artifact
}

// Backend defines the interface for backup backends (S3, custom Docker image).
type Backend interface {
	// Init initializes the backend (e.g., verify the bucket/repository is reachable)
	Init(ctx context.Context) error

	// Backup performs the backup operation for the given snapshot.
	// Returns output logs from the backup operation.
	Backup(ctx context.Context, snap Snapshot) (string, error)

	// ApplyRetention removes old backups according to the retention policy.
	// Returns output logs from the cleanup operation.
	ApplyRetention(ctx context.Context, retention model.Retention) (string, error)

	// SetLogger attaches a per-job logger used for streaming progress/log output.
	SetLogger(logger *logging.JobLogger)

	// Close cleans up any resources used by the backend
	Close() error

	GetType() BackendType
}
