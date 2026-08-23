# Custom Docker Image Backend - Implementation Summary

This document summarizes the custom Docker image backend feature implementation.

## Overview

Marina's built-in destination is S3 (see [Archive Format](archive-format.md) for the on-disk layout). Custom Docker images are the escape
hatch for anyone who needs a different destination -- this allows users to implement any backup workflow by packaging it as a Docker container.

## Architecture

```text
┌─────────────────────────────────────────────────────────────┐
│                        Marina Manager                       │
│                                                             │
│  ┌──────────────┐     ┌────────────────────────────────┐    │
│  │  Discovery   │────▶│         Runner                 │    │
│  │ (config.yml) │     │  - Schedules backups           │    │
│  └──────────────┘     │  - Prepares staging data       │    │
│                       │  - Selects backend             │    │
│                       └──────────┬─────────────────────┘    │
│                                  │                          │
│                       ┌──────────▼─────────────┐            │
│                       │   Backend Interface    │            │
│                       └──────────┬─────────────┘            │
│                                  │                          │
│                  ┌───────────────┴───────────────┐          │
│                  │                               │          │
│         ┌────────▼─────────┐          ┌──────────▼────────┐ │
│         │    S3Backend     │          │ CustomImageBackend│ │
│         │  (S3 API client) │          │  (Docker API)     │ │
│         └──────────────────┘          └───────────────────┘ │
│                  │                              │           │
└──────────────────┼──────────────────────────────┼───────────┘
                   │                              │
                   ▼                              ▼
            ┌─────────────┐              ┌──────────────────┐
            │  S3-compat  │              │  Custom Docker   │
            │   bucket    │              │    Container     │
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
    Backup(ctx context.Context, snap Snapshot) (string, error)
    ApplyRetention(ctx context.Context, retention model.Retention) (string, error)
    SetLogger(logger *logging.JobLogger)
    Close() error
    GetType() BackendType
}
```

`Snapshot` carries one `Artifact` (a staged tree, plus its type/name) per target, rather than a
flat list of paths -- this is what lets `S3Backend` archive and upload each target as a separate
object.

### 2. S3 Backend (`internal/backend/s3.go`)

Archives each artifact as tar+zstd (optionally age-encrypted) and streams it directly to the
bucket via the S3 API, with no external binary involved. Also implements the read path
(`ListSnapshots`, `GetManifest`, `RestoreArtifact`) that `cmd/marinactl` calls into for restore --
backup and restore share the exact same object-format code.

### 3. Custom Image Backend (`internal/backend/custom_image.go`)

- Pulls the specified Docker image
- Starts a container with staging volume mounted at `/backup`
- Executes the backup script (defaults to `/backup.sh`)
- Captures stdout/stderr for logs
- Returns exit code as success/failure indicator

### 4. Configuration (`internal/config/config.go`)

`BackupInstance.S3` and `BackupInstance.CustomImage` are mutually exclusive; `config.Validate()`
(called from `Load`) rejects a config with both or neither set, and validates the `S3Config`
fields are complete when `s3:` is used:

```go
type BackupInstance struct {
    ID          string
    S3          *S3Config          // mutually exclusive with CustomImage
    CustomImage string             // Optional custom Docker image
    Encryption  *EncryptionConfig  // S3 destinations only
    Schedule    string
    Retention   string
    Env         map[string]string  // custom-image backend only
}
```

### 5. Runner (`internal/runner/runner.go`)

Uses the `Backend` interface exclusively, so backend selection happens once at startup
(`cmd/manager/main.go`) and the runner itself never branches on backend type -- it stages each
target, collects one `backend.Artifact` per successfully staged target, and calls `Backup` once
per instance run with the resulting `Snapshot`.

### 6. Main Entry Point (`cmd/manager/main.go`)

Instantiates the appropriate backend based on configuration:

```go
if dest.CustomImage != "" {
    backendInstance, backendErr = backend.NewCustomImageBackend(...)
} else {
    backendInstance = &backend.S3Backend{...}
}
```

## Data Flow

### Backups with Custom Image

1. **Discovery**: Marina verifies configured volumes and databases exist in Docker
2. **Scheduling**: Cron triggers backup at configured time
3. **Staging**: Marina copies data to `/backup/{instance}/{timestamp}/`
4. **Container Launch**: Marina starts custom image with:
   - `/backup/{instance}/` mounted to `/backup` (scoped to just this instance)
   - Environment variables from config
   - `MARINA_INSTANCE_ID`, `MARINA_HOSTNAME`, `MARINA_TIMESTAMP`, `MARINA_TARGETS` injected
5. **Execution**: Container runs `/backup.sh` script
6. **Log Capture**: Marina captures stdout/stderr
7. **Cleanup**: Container removed, staging cleaned up based on exit code

## Configuration Examples

### S3 Backend (built-in)

```yaml
instances:
  - id: s3-backup
    s3:
      endpoint: s3.example.com
      bucket: my-bucket
      accessKeyId: key
      secretAccessKey: secret
    encryption:
      password: secret  # optional
    schedule: "0 2 * * *"
```

### Custom Image Backend

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
- `MARINA_TIMESTAMP` - This run's timestamp (`20060102-150405`)
- `MARINA_TARGETS` - Comma-separated `type:name` list of staged targets (e.g. `volume:app-data,db:postgres`)
- All variables from `env` section in config
