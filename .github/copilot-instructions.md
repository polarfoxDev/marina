# Marina - Docker Backup Orchestrator

## Architecture Overview

Marina is a config-driven backup orchestrator that backs up directly to S3-compatible storage (no third-party backup tool involved), with optional end-to-end encryption. All backup targets are defined in `config.yml`, validated at backup time, and executed via cron scheduling with application-aware hooks.

**Core workflow**: Config Loading → Schedule Building → Cron Scheduling → Execution (Runner) → Backend (S3/Custom)

**Runtime Validation**: Targets are validated at backup time—missing containers or volumes are skipped with warnings logged.

### Key Components

- **`internal/config/config.go`**: Parses `config.yml` and expands environment variable references (`${VAR}` or `$VAR`); defines `BackupInstance` and `TargetConfig` structs
- **`internal/scheduler/builder.go`**: Converts config instances to backup schedules; validates that targets have exactly one of `volume` or `db` set
- **`internal/runner/runner.go`**: Orchestrates backup execution and cron scheduling; manages job lifecycle and status tracking
- **`internal/runner/volume.go`**: Handles volume staging—container stopping, data copying, pre/post hooks, cleanup
- **`internal/runner/database.go`**: Handles database staging—dump creation, auto-detection of DB type, pre/post hooks, cleanup
- **`internal/runner/helpers.go`**: Validation utilities (file size checks) and hook execution
- **`internal/backend/s3.go`**: Archives each target (tar+zstd, optional age encryption) and streams it directly to an S3-compatible bucket via the S3 API; also implements the read path (`ListSnapshots`, `GetManifest`, `RestoreArtifact`) shared with `cmd/marinactl`
- **`internal/backend/custom_image.go`**: Custom Docker image backend support for destinations other than S3
- **`internal/archive/tar.go`**: Streaming tar+zstd archive/extract of a staged directory tree
- **`internal/crypto/crypto.go`**: Thin wrapper around `filippo.io/age` for optional password-based encryption (no-op passthrough when no password is set)
- **`cmd/marinactl/main.go`**: CLI for listing and restoring snapshots directly from S3 -- the only way to read a Marina backup back, since there's no external backup tool to fall back on
- **`internal/model/model.go`**: Defines `BackupTarget` (volume or DB), `Retention` policy, and job state
- **`internal/database/database.go`**: SQLite database for persistent job status and log storage
- **`internal/logging/logger.go`**: Structured logging with job-specific loggers that write to both stdout and database
- **`cmd/manager/main.go`**: Entry point—loads config, creates backend instances, builds schedules, starts runner and cron scheduler
- **`cmd/api/main.go`**: REST API server for querying job status, logs, and schedules; supports peer federation for multi-node setups

### Configuration System

Marina uses a single `config.yml` file that defines all backup instances and their targets. Environment variable expansion is supported using `${VAR_NAME}` or `$VAR_NAME` syntax.

**config.yml structure**:

```yaml
instances:
  - id: hetzner-s3
    s3:
      endpoint: fsn1.your-objectstorage.com # host[:port], no scheme
      bucket: marina-backups
      accessKeyId: ${AWS_KEY}
      secretAccessKey: ${AWS_SECRET}
    encryption:
      password: ${ENC_PASS} # Optional: omit to store archives unencrypted
    schedule: "0 2 * * *" # Cron schedule for this instance's backups
    retention: "30d:12w:24m" # Optional: instance-specific retention
    timeout: "10m" # Optional: instance-specific timeout (default 60m)
    targets:
      - volume: app-data
        paths: ["/"] # Optional: paths relative to volume root
        stopAttached: false # Optional: stop containers during backup
      - db: postgres # Container name
        dbKind: postgres # Optional: auto-detected if not specified
        dumpArgs: ["--clean"] # Optional: additional dump arguments
    beforeBackup: # Optional commands to run before backup starts
      - container: app-postgres # run inside this container
        command: "psql -U myapp -c 'CHECKPOINT;'"
      - image: ubuntu:latest # run in temporary container
        command: "curl -X POST http://app-service/prepare-backup"
    afterBackup: # Optional commands to run after backup completes
      - container: app-postgres
        command: "psql -U myapp -c 'VACUUM;'"
      - image: ubuntu:latest
        command: "curl -X POST http://app-service/backup-complete"

  - id: custom-backup
    customImage: your-registry/backup:latest # Alternative to S3
    schedule: "0 3 * * *"
    env:
      BACKUP_TOKEN: ${TOKEN}
    targets:
      - volume: custom-data

# Global defaults (can be overridden per instance or target)
retention: "14d:8w:12m" # Format: daily:weekly:monthly
stopAttached: true # Default for all volume targets
timeout: "60m" # Global timeout for backend operations

# Runtime configuration
dbPath: "/var/lib/marina/marina.db" # Database path (default shown)
apiPort: "8080" # API server port (default shown)
corsOrigins: # Additional CORS origins (optional)
  - https://marina.example.com

# Optional: Peer federation for multi-node setups
peers:
  - http://marina-node2:8080
```

**Configuration hierarchy**: Instance config > Global config > Target config > Hardcoded defaults

- Schedule: Required per-instance in config.yml
- Retention: Instance-specific (optional) > Global `retention` > Hardcoded default "7d:4w:6m"
- StopAttached: Target-specific > Global `stopAttached` > Hardcoded default false
- Timeout: Instance-specific (optional) > Global `timeout` > Hardcoded default "60m"
- Node name: `nodeName` (top-level) > hostname
- Auth password: `authPassword` (top-level) > empty (disabled)

**Target validation**: Each target must have exactly one of `volume` or `db` set. The `scheduler.BuildSchedulesFromConfig` function validates this at startup and returns an error for invalid configurations.

**Important for MySQL/MariaDB**: Do NOT set `MYSQL_PWD` environment variable as it interferes with container initialization. Instead, pass credentials via `dumpArgs` using `["-uroot", "-pPASSWORD"]` format.

### Data Flow Patterns

1. **Volume backups** (`internal/runner/volume.go`):

   - Validates volume exists via Docker API at backup time (skipped with warning if missing)
   - Finds containers using the volume (for hooks and optional stopping)
   - Executes pre-hook in first attached container (if specified)
   - Stops attached containers if `stopAttached=true` (skips read-only mounts)
   - Copies volume data to staging via temporary Alpine container: `/backup/{instanceID}/{timestamp}/volume/{name}/`
   - Marina detects actual host path for `/backup` by inspecting its own container mounts at startup
   - Validates staged files have content (errors if empty)
   - Cleanup function: removes staging directory and restarts stopped containers (executed via defer)
   - Post-hook executes in first attached container after backup completes

1. **DB backups** (`internal/runner/database.go`):

   - Validates container exists via Docker API at backup time (skipped with warning if missing)
   - Auto-detects database type from container image if `dbKind` not specified
   - Executes pre-hook inside DB container (if specified)
   - Creates dump inside container at `/tmp/marina-{timestamp}` using appropriate tool (pg_dumpall, mysqldump, mariadb-dump, mongodump)
   - Copies dump file to staging: `/backup/{instanceID}/{timestamp}/db/{name}/`
   - Validates dump file has content (errors if empty)
   - Cleanup function: removes `/tmp/marina-*` from container and staging directory on host
   - Post-hook executes inside DB container after backup completes

1. **Backend execution**:

   - One `backend.Artifact{Type, Name, Root}` collected per successfully staged target (root dir, not a flat path list)
   - S3 backend: each artifact archived (tar+zstd, optionally age-encrypted) and uploaded as its own object, then a `manifest.json` written last as the completion marker for that snapshot
   - Custom image backend: Container created with `/backup/{instanceID}` mounted, runs `/backup.sh` script
   - Failed targets logged but don't stop other targets from being backed up

1. **Retention**:
   - Applied _after every backup_ via `Backend.ApplyRetention()`
   - Parsed from config like `"7d:14w:6m"` → keep the newest snapshot in each of the most recent 7 days, 14 weeks, 6 months (`helpers.SelectExpiredSnapshots`); the single newest snapshot is always kept regardless of policy
   - Defaults: 7 daily, 4 weekly, 6 monthly (see `helpers/retention.go`)
   - S3 backend: snapshots with no manifest (aborted mid-upload) are never touched by retention, and never considered "complete" by `marinactl list` either

### Critical Patterns

**Static scheduling**: Schedules built once at startup from config; no dynamic discovery or event listening. To add/remove targets, update config.yml and restart Marina.

**Job lifecycle tracking**: Runner tracks scheduled jobs in `scheduledJobs` map (instance ID → cron.EntryID); `SyncBackups()` updates schedules if config changes

**Multi-backend support**: Runner accepts a map of `Backend` objects keyed by instance ID; supports both S3 and custom Docker image backends

**Runtime validation**: Targets validated at backup time via Docker API—missing volumes/containers skipped with warnings, don't fail entire backup

**Deferred cleanup**: Pre/post hooks and container restarts use `defer` to ensure cleanup even on error (see `volume.go`, `database.go`)

**Read-only volume detection**: Skips stopping containers when target volume is mounted with `Mode == "ro"` to avoid unnecessary disruption

**Host path detection**: Marina detects actual host path for `/backup` by inspecting its own container mounts at startup (see `docker.GetBackupHostPath`), then uses this for bind mounts in temporary containers

**Cron parser**: Uses `robfig/cron/v3` with 5-field standard format (minute hour dom month dow), not 6-field with seconds

**Shell invocation**: All hooks/dumps use `/bin/sh -lc` to ensure login shell and proper env loading

**Error handling**: Individual target failures don't stop other targets; errors logged but backup continues. Post-hooks/cleanup still execute via defer.

**Environment variable expansion**: Config loader uses regex to match `${VAR}` and `$VAR` patterns and expands them using `os.Getenv()`

**Database auto-detection**: `detectDBKind()` in `database.go` checks container image name for "postgres", "mysql", "mariadb", "mongo", "redis"

**Logging**: Structured logging with job-specific loggers; logs written to both stdout and SQLite database for API queries

**Job status persistence**: SQLite database tracks job history, status, timestamps, target counts; survives restarts


## Development Workflows

**Build**: `go build -o marina ./cmd/manager`

**Run locally** (requires Docker socket):

```bash
# Set up environment variables referenced in config.yml
export S3_ACCESS_KEY_ID=your-key
export S3_SECRET_ACCESS_KEY=your-secret
export MARINA_ENC_PASSWORD=your-encryption-password  # optional

# Run with default config file (/config.yml or config.yml in current directory)
./marina

# Or specify custom config location via environment variable
export CONFIG_FILE=/path/to/config.yml
./marina
```

**Test config loading and schedule building**:

```go
cfg, _ := config.Load("config.yml")
schedules, _ := scheduler.BuildSchedulesFromConfig(cfg)
// Inspect schedules slice
```

## Project Conventions

- **Package structure**: `cmd/` for binaries, `internal/` for libraries (not importable outside module)
- **Interfaces**: `Backend` interface supports multiple backends (S3, custom Docker images)
- **Error wrapping**: Use `fmt.Errorf("context: %w", err)` for wrappable errors throughout
- **Logging**: Structured logging via `internal/logging`; job loggers write to both stdout and database
- **Tests**: Project has unit tests in `*_test.go` files (see `internal/scheduler/builder_test.go`) and integration tests in `tests/integration/`
- **Config format**: `config.yml` defines backup instances (mapped by ID) with embedded targets; instances include an `s3:` block (or `customImage:`), optional `encryption:`, schedule, and target list
- **Configuration philosophy**: All backup configuration in config.yml; no Docker labels; targets validated at runtime
- **Runner organization**: Core orchestration in `runner.go`; staging logic split into `volume.go` and `database.go`; helpers in `helpers.go`
- **CHANGELOG**: After making changes, add entries to the `## [Unreleased]` section in `CHANGELOG.md` following Keep a Changelog format (Added/Changed/Deprecated/Removed/Fixed/Security)

## Code Quality Standards

**Modern Go Code**: Always write modern, idiomatic Go code. If the IDE suggests modernization (e.g., using `any` instead of `interface{}`, using `strings.Cut` instead of `strings.Split`, simplifying range loops), apply those suggestions. Stay current with Go language features and best practices.

**Code Quality Practices**:

1. Use modern Go idioms and language features (Go 1.21+)
1. Prefer `any` over `interface{}` for empty interfaces
1. Use `strings.Cut`, `strings.CutPrefix`, `strings.CutSuffix` over index-based string manipulation where appropriate
1. Simplify range loops when only the value is needed (omit the index variable)
1. Use `clear()` builtin for clearing maps/slices when appropriate
1. Minimize allocations in hot paths
1. Add context parameters to functions that may block or be long-running
1. Use structured error handling with `fmt.Errorf("context: %w", err)` for error wrapping
1. Prefer early returns to reduce nesting depth
1. Keep functions focused and under 50 lines when possible
1. Use meaningful variable names (avoid single-letter names except for common idioms like `i`, `j`, `err`, `ctx`)
1. Add package-level documentation comments for exported types and functions
1. Group related functionality into focused files (e.g., `volume.go`, `database.go`, `helpers.go`)

**Markdown Quality**: Always fix markdown linter issues. Proper markdown formatting is important for documentation quality. Common fixes:

1. Ensure proper spacing around headers
1. Use consistent list formatting (prefer `1.` for all ordered list items, not `1., 2., 3.`)
1. Close code blocks properly
1. Use consistent heading levels
1. Add blank lines before and after code blocks and lists

**Testing**: Write tests for new functionality. Use table-driven tests for testing multiple cases. Validate error messages and edge cases.

## Release Process

When the user requests to "prepare release" or similar:

1. **Choose version number** using semantic versioning:

   - Patch (e.g., 0.8.0 → 0.8.1): Bug fixes, documentation updates, minor changes
   - Minor (e.g., 0.8.0 → 0.9.0): New features, non-breaking changes
   - Major (e.g., 0.9.0 → 1.0.0): Breaking changes, major milestones

1. **Update CHANGELOG.md**:

   - Move all entries from `## [Unreleased]` section to new version section with today's date
   - Format: `## [X.Y.Z] - YYYY-MM-DD`
   - Create empty `## [Unreleased]` section at the top
   - Update version comparison links at bottom:
     - Change `[Unreleased]` link to compare new version with HEAD
     - Add new version comparison link

1. **Update VERSION file**:
   - Replace content with new version number (e.g., `0.9.0`)

**Example CHANGELOG.md changes**:

```markdown
## [Unreleased]

## [0.9.0] - 2025-11-30

### Added

- New feature description

[Unreleased]: https://github.com/polarfoxDev/marina/compare/v0.9.0...HEAD
[0.9.0]: https://github.com/polarfoxDev/marina/compare/v0.8.0...v0.9.0
```

## Implemented Features

- **Restore**: `marinactl list`/`marinactl restore` read snapshots directly from S3; see `docs/archive-format.md`
- **Web Interface**: React-based dashboard for monitoring backup status and logs (in `web/` directory)
- **Peer Federation**: Multi-node federation allowing unified monitoring across multiple Marina instances
  - Configured via top-level `peers` array in config.yml
  - Client in `internal/peer/client.go` fetches data from peer nodes
  - Web interface automatically displays data from all connected nodes
  - Authentication via shared password across federated nodes

## Docker Compose Example

See `docker-compose.example.yml` for a complete deployment example with:

- Marina manager container with Docker socket access
- Volume and database backup examples in config.yml
- Host bind mount for staging directory (required)
- S3/custom backend configuration
- Peer federation setup for multi-node deployments

## Context

Use context7 MCP to check how to do stuff in the current Go version.
