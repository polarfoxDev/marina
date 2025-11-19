# Custom Docker Image Backend - Implementation Summary

This document summarizes the custom Docker image backend feature implementation.

## Overview

Marina now supports using custom Docker images as backup backends, providing an alternative to the built-in Restic integration. This allows users to implement any backup workflow by packaging it as a Docker container.

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
│         │  ResticBackend   │          │ CustomImageBackend│ │
│         │  (CLI wrapper)   │          │  (Docker API)     │ │
│         └──────────────────┘          └───────────────────┘ │
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
    Backup(ctx context.Context, paths []string, tags []string) (string, error)
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

### Backups with Custom Image

1. **Discovery**: Marina verifies configured volumes and databases exist in Docker
2. **Scheduling**: Cron triggers backup at configured time
3. **Staging**: Marina copies data to `/backup/{instance}/{timestamp}/`
4. **Container Launch**: Marina starts custom image with:
   - `/backup/{instance}/{timestamp}/` mounted to `/backup`
   - Environment variables from config
   - `MARINA_INSTANCE_ID` and `MARINA_HOSTNAME` injected
5. **Execution**: Container runs `/backup.sh` script
6. **Log Capture**: Marina captures stdout/stderr
7. **Cleanup**: Container removed, staging cleaned up based on exit code

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
- All variables from `env` section in config
