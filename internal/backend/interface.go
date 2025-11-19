package backend

import "context"

type BackendType string

const (
	BackendTypeRestic      BackendType = "restic"
	BackendTypeCustomImage BackendType = "custom"
)

// Backend defines the interface for backup backends (Restic, custom Docker image, etc.)
type Backend interface {
	// Init initializes the backend (e.g., create repository if needed)
	Init(ctx context.Context) error

	// Backup performs the backup operation with the given paths and tags.
	// Returns output logs from the backup operation
	Backup(ctx context.Context, paths []string, tags []string) (string, error)

	// DeleteOldSnapshots applies retention policy to remove old backups
	// Returns output logs from the cleanup operation
	DeleteOldSnapshots(ctx context.Context, daily, weekly, monthly int) (string, error)

	// Close cleans up any resources used by the backend
	Close() error

	GetType() BackendType

	GetImage() string

	// GetResticTimeout returns the configured timeout for this backend
	GetResticTimeout() string
}
