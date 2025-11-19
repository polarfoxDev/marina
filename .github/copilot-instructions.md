# Marina - Docker Backup Orchestrator

## Architecture Overview

Marina is a config-driven backup orchestrator that uses Restic as its backend. All backup targets are defined in `config.yml`, validated at backup time, and executed via cron scheduling with application-aware hooks.

**Core workflow**: Config Loading → Schedule Building → Cron Scheduling → Execution (Runner) → Backend (Restic/Custom)

**Runtime Validation**: Targets are validated at backup time—missing containers or volumes are skipped with warnings logged.

### Key Components

- **`internal/config/config.go`**: Parses `config.yml` and expands environment variable references (`${VAR}` or `$VAR`); defines `BackupInstance` and `TargetConfig` structs
- **`internal/scheduler/builder.go`**: Converts config instances to backup schedules; validates that targets have exactly one of `volume` or `db` set
- **`internal/runner/runner.go`**: Orchestrates backup execution and cron scheduling; manages job lifecycle and status tracking
- **`internal/runner/volume.go`**: Handles volume staging—container stopping, data copying, pre/post hooks, cleanup
- **`internal/runner/database.go`**: Handles database staging—dump creation, auto-detection of DB type, pre/post hooks, cleanup
- **`internal/runner/helpers.go`**: Validation utilities (file size checks, deduplication)
- **`internal/backend/restic.go`**: Wraps Restic CLI commands (backup, forget, prune) with repository and environment variables
- **`internal/backend/custom_image.go`**: Custom Docker image backend support for alternative backup destinations
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
    repository: s3:https://fsn1.your-objectstorage.com/bucket
    schedule: "0 2 * * *" # Cron schedule for this instance's backups
    retention: "30d:12w:24m" # Optional: instance-specific retention
    resticTimeout: "10m" # Optional: instance-specific timeout (default 60m)
    env:
      AWS_ACCESS_KEY_ID: ${AWS_KEY}
      AWS_SECRET_ACCESS_KEY: ${AWS_SECRET}
      RESTIC_PASSWORD: ${RESTIC_PASS}
    targets:
      - volume: app-data
        paths: ["/"] # Optional: paths relative to volume root
        stopAttached: false # Optional: stop containers during backup
        preHook: "" # Optional: command before backup
        postHook: "" # Optional: command after backup
      - db: postgres # Container name
        dbKind: postgres # Optional: auto-detected if not specified
        dumpArgs: ["--clean"] # Optional: additional dump arguments

  - id: custom-backup
    customImage: your-registry/backup:latest # Alternative to Restic
    schedule: "0 3 * * *"
    env:
      BACKUP_TOKEN: ${TOKEN}
    targets:
      - volume: custom-data

# Global defaults (can be overridden per instance or target)
retention: "14d:8w:12m" # Format: daily:weekly:monthly
stopAttached: true # Default for all volume targets
resticTimeout: "60m" # Global timeout for backup operations

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
- Timeout: Instance-specific (optional) > Global `resticTimeout` > Hardcoded default "60m"
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

   - All staged paths from all targets collected into single list
   - Tags generated for each target: `volume:name` or `db:name`
   - Restic backend: `restic backup` with all paths in one operation, then `forget` + `prune` for retention
   - Custom image backend: Container created with `/backup/{instanceID}` mounted, runs `/backup.sh` script
   - Failed targets logged but don't stop other targets from being backed up

1. **Retention**:
   - Applied _after every backup_ via `DeleteOldSnapshots()`
   - Parsed from config like `"7d:14w:6m"` → `--keep-daily 7 --keep-weekly 14 --keep-monthly 6`
   - Defaults: 7 daily, 4 weekly, 6 monthly (see `helpers/retention.go`)

### Critical Patterns

**Static scheduling**: Schedules built once at startup from config; no dynamic discovery or event listening. To add/remove targets, update config.yml and restart Marina.

**Job lifecycle tracking**: Runner tracks scheduled jobs in `scheduledJobs` map (instance ID → cron.EntryID); `SyncBackups()` updates schedules if config changes

**Multi-backend support**: Runner accepts a map of `Backend` objects keyed by instance ID; supports both Restic and custom Docker image backends

**Runtime validation**: Targets validated at backup time via Docker API—missing volumes/containers skipped with warnings, don't fail entire backup

**Deferred cleanup**: Pre/post hooks and container restarts use `defer` to ensure cleanup even on error (see `volume.go`, `database.go`)

**Read-only volume detection**: Skips stopping containers when target volume is mounted with `Mode == "ro"` to avoid unnecessary disruption

**Host path detection**: Marina detects actual host path for `/backup` by inspecting its own container mounts at startup (see `docker.GetBackupHostPath`), then uses this for bind mounts in temporary containers

**Cron parser**: Uses `robfig/cron/v3` with 5-field standard format (minute hour dom month dow), not 6-field with seconds

**Shell invocation**: All hooks/dumps use `/bin/sh -lc` to ensure login shell and proper env loading

**Error handling**: Individual target failures don't stop other targets; errors logged but backup continues. Post-hooks/cleanup still execute via defer.

**Environment variable expansion**: Config loader uses regex to match `${VAR}` and `$VAR` patterns and expands them using `os.Getenv()`

**Restic unlock on backup**: Before each backup, Restic automatically runs `unlock` to clear stale locks from crashed or stopped processes

**Database auto-detection**: `detectDBKind()` in `database.go` checks container image name for "postgres", "mysql", "mariadb", "mongo", "redis"

**Logging**: Structured logging with job-specific loggers; logs written to both stdout and SQLite database for API queries

**Job status persistence**: SQLite database tracks job history, status, timestamps, target counts; survives restarts

**macOS compatibility**: On macOS with Docker Desktop, Restic repositories must use Docker named volumes (not bind mounts) to avoid "bad file descriptor" errors during fsync operations caused by the Docker VM filesystem layer (osxfs/VirtioFS)

## Development Workflows

**Build**: `go build -o marina ./cmd/manager`

**Run locally** (requires Docker socket):

```bash
# Set up environment variables referenced in config.yml
export RESTIC_PASSWORD=test
export AWS_ACCESS_KEY_ID=your-key
export AWS_SECRET_ACCESS_KEY=your-secret

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
- **Interfaces**: `Backend` interface supports multiple backends (Restic, custom Docker images)
- **Error wrapping**: Use `fmt.Errorf("context: %w", err)` for wrappable errors throughout
- **Logging**: Structured logging via `internal/logging`; job loggers write to both stdout and database
- **Tests**: Project has unit tests in `*_test.go` files (see `internal/scheduler/builder_test.go`) and integration tests in `tests/integration/`
- **Config format**: `config.yml` defines backup instances (mapped by ID) with embedded targets; instances include repository URL, schedule, environment variables, and target list
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

## Planned Features

- **Recovery**: Restore operations from Restic snapshots

## Implemented Features

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
- S3/local/custom backend configuration
- Peer federation setup for multi-node deployments

## Context

Use context7 MCP to check how to do stuff in the current Go version.
