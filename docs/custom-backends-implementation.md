# Custom Docker Image Backend - Implementation Summary

This document summarizes the custom Docker image backend feature implementation.

## Overview

Marina now supports using custom Docker images as backup backends, providing an alternative to the built-in Restic integration. This allows users to implement any backup workflow by packaging it as a Docker container.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        Marina Manager                        │
│                                                              │
│  ┌──────────────┐     ┌────────────────────────────────┐   │
│  │  Discovery   │────▶│         Runner                 │   │
│  │  (Labels)    │     │  - Schedules backups           │   │
│  └──────────────┘     │  - Prepares staging data       │   │
│                       │  - Selects backend             │   │
│                       └──────────┬─────────────────────┘   │
│                                  │                          │
│                       ┌──────────▼─────────────┐           │
│                       │   Backend Interface    │           │
│                       └──────────┬─────────────┘           │
│                                  │                          │
│                  ┌───────────────┴────────────────┐        │
│                  │                                 │        │
│         ┌────────▼─────────┐          ┌──────────▼──────┐ │
│         │  ResticBackend   │          │ CustomImageBackend│ │
│         │  (CLI wrapper)   │          │  (Docker API)    │ │
│         └──────────────────┘          └──────────────────┘ │
│                  │                              │           │
└──────────────────┼──────────────────────────────┼───────────┘
                   │                              │
                   ▼                              ▼
            ┌─────────────┐              ┌──────────────────┐
            │   Restic    │              │  Custom Docker   │
            │   Binary    │              │    Container     │
            └─────────────┘              │  - /backup mount │
                                         │  - /backup.sh    │
                                         │  - Exit code 0/1 │
                                         └──────────────────┘
```

## Key Components

### 1. Backend Interface (`internal/backend/interface.go`)

Defines the contract that all backup backends must implement:

```go
type Backend interface {
    Init(ctx context.Context) error
    Backup(ctx context.Context, paths []string, tags []string, excludes []string) (string, error)
    DeleteOldSnapshots(ctx context.Context, daily, weekly, monthly int) (string, error)
    Close() error
}
```

### 2. Restic Backend (`internal/backend/restic.go`)

Existing implementation wrapped to satisfy the Backend interface. No functional changes.

### 3. Custom Image Backend (`internal/backend/custom_image.go`)

New implementation that:
- Pulls the specified Docker image
- Starts a container with staging volume mounted at `/backup`
- Executes the backup script (defaults to `/backup.sh`)
- Captures stdout/stderr for logs
- Returns exit code as success/failure indicator

### 4. Configuration Updates (`internal/config/config.go`)

Added `customImage` field to `BackupInstance`:

```go
type BackupInstance struct {
    ID          string
    Repository  string            // Optional when customImage is set
    CustomImage string            // Optional custom Docker image
    Schedule    string
    Retention   string
    Env         map[string]string
}
```

### 5. Runner Updates (`internal/runner/runner.go`)

Changed to use `Backend` interface instead of concrete `BackupInstance` type, enabling backend selection at runtime.

### 6. Main Entry Point (`cmd/manager/main.go`)

Updated to instantiate the appropriate backend based on configuration:

```go
if dest.CustomImage != "" {
    backend = NewCustomImageBackend(...)
} else {
    backend = &ResticBackend{...}
}
```

## Data Flow

### Volume Backup with Custom Image

1. **Discovery**: Marina scans Docker for volumes with `dev.polarfox.marina.enabled: "true"`
2. **Scheduling**: Cron triggers backup at configured time
3. **Staging**: Marina copies volume data to `/backup/{instance}/{timestamp}/vol/{volume}/`
4. **Container Launch**: Marina starts custom image with:
   - `/backup` volume mounted
   - Environment variables from config
   - `MARINA_INSTANCE_ID` and `MARINA_HOSTNAME` injected
5. **Execution**: Container runs `/backup.sh` script
6. **Log Capture**: Marina captures stdout/stderr
7. **Cleanup**: Container removed, staging cleaned up based on exit code

### Database Backup with Custom Image

Similar flow but Marina creates database dumps first:
- Marina dumps DB to `/backup/{instance}/{timestamp}/dbs/{db-name}/`
- Custom container processes the dump files
- Same log capture and cleanup process

## Configuration Examples

### Restic Backend (existing)
```yaml
instances:
  - id: restic-s3
    repository: s3:https://s3.example.com/bucket
    schedule: "0 2 * * *"
    env:
      RESTIC_PASSWORD: secret
      AWS_ACCESS_KEY_ID: key
```

### Custom Image Backend (new)
```yaml
instances:
  - id: custom-backup
    customImage: myorg/backup-tool:latest
    schedule: "0 3 * * *"
    env:
      BACKUP_TOKEN: secret
      BACKUP_ENDPOINT: https://api.example.com
```

## Environment Variables for Custom Images

Marina automatically provides:
- `MARINA_INSTANCE_ID` - Instance identifier from config
- `MARINA_HOSTNAME` - Hostname of Marina node
- All variables from `env` section in config

## Testing

### Unit Tests
- `internal/backend/restic_test.go` - Restic backend tests (updated)
- `internal/backend/custom_image_test.go` - Custom image backend tests (new)
- `internal/config/config_test.go` - Config parsing tests (updated)

### Integration Testing
- Example image in `examples/custom-backup-image/`
- Demonstrates real-world usage
- Includes random failures (25% failure rate) for testing error handling

### Security
- CodeQL scan: 0 alerts
- Docker container lifecycle properly managed
- No credential leakage in logs

## Breaking Changes

None. This is a fully backward-compatible addition:
- Existing Restic configurations work unchanged
- New `customImage` field is optional
- Backend selection is automatic based on config

## Future Enhancements

Possible future improvements:
1. Support for custom retention policies in custom images
2. Streaming backup data instead of staging
3. Multi-container backup workflows
4. Backup verification hooks
5. Custom backend plugins (Go plugins)

## Documentation

- `docs/custom-backends.md` - Comprehensive guide for custom backends
- `examples/custom-backup-image/README.md` - Example image documentation
- `config.example.yml` - Updated with custom image example (commented out)
- `CHANGELOG.md` - Release notes for this feature

## File Changes

**New Files:**
- `internal/backend/interface.go` - Backend interface definition
- `internal/backend/custom_image.go` - Custom image implementation
- `internal/backend/custom_image_test.go` - Tests for custom image backend
- `examples/custom-backup-image/Dockerfile` - Example image
- `examples/custom-backup-image/backup.sh` - Example backup script
- `examples/custom-backup-image/README.md` - Example documentation
- `docs/custom-backends.md` - Feature documentation

**Modified Files:**
- `internal/backend/restic.go` - Renamed struct, implements interface
- `internal/backend/restic_test.go` - Updated for renamed struct
- `internal/config/config.go` - Added customImage field
- `internal/config/config_test.go` - Added test for custom image config
- `internal/runner/runner.go` - Uses Backend interface
- `cmd/manager/main.go` - Backend selection logic
- `config.example.yml` - Added example (commented)
- `CHANGELOG.md` - Release notes

## Performance Considerations

- **Image Pulls**: Images are pulled once during Init(), cached by Docker
- **Container Startup**: ~100-500ms overhead per backup
- **Log Capture**: Minimal overhead, streamed efficiently
- **Cleanup**: Automatic via Docker API (AutoRemove flag)

## Reliability

- Container failures captured and reported
- Logs preserved in Marina database
- Automatic container cleanup prevents resource leaks
- Graceful handling of missing images
- Network-isolated builds (example image uses no external dependencies)

## Monitoring

Custom image backups are visible in:
- Marina web dashboard (same as Restic backups)
- API endpoints (`/api/jobs`, `/api/logs`)
- Job status tracking in database
- System logs for backend errors

## Migration Path

Users can migrate gradually:
1. Keep existing Restic instance
2. Add new custom image instance
3. Test with non-critical volumes
4. Gradually migrate volumes
5. Retire Restic instance when ready

No downtime required during migration.
