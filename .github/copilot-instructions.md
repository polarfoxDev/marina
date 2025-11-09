# Marina - Docker Backup Orchestrator

## Architecture Overview

Marina is a Docker label-based backup orchestrator that uses Restic as its backend. The system discovers backup targets by scanning Docker labels on volumes and containers, schedules backups via cron, and executes them with application-aware hooks.

**Core workflow**: Discovery (Docker labels) → Scheduling (cron jobs) → Execution (Runner) → Backend (Restic)

**Dynamic Discovery**: Marina continuously monitors Docker for changes via event listener and periodic polling, automatically adding/removing/updating backup jobs without requiring restarts.

### Key Components

- **`internal/config/config.go`**: Parses `config.yml` and expands environment variable references (`${VAR}` or `$VAR`)
- **`internal/docker/discovery.go`**: Scans Docker API for volumes/containers with `eu.polarnight.marina.*` labels, builds `BackupTarget` models
- **`internal/docker/events.go`**: Listens to Docker events API for real-time detection of container/volume lifecycle changes (create, destroy, start, stop)
- **`internal/runner/runner.go`**: Orchestrates backup execution and manages dynamic job scheduling—handles pre/post hooks, container stop/start, and delegates to appropriate backend destination
- **`internal/backend/restic.go`**: Wraps Restic CLI commands (backup, forget, prune) with repository and environment variables
- **`internal/model/model.go`**: Defines `BackupTarget` (volume or DB), `Retention` policy, and job state
- **`cmd/manager/main.go`**: Entry point—loads config, creates destinations map, performs initial discovery, starts periodic rediscovery loop and event listener, and manages runner lifecycle

### Configuration System

Marina uses a two-tier configuration approach:

1. **`config.yml`**: Defines backup destinations (repositories and credentials)
2. **Docker labels**: Define what to backup, when, and to which destination

**config.yml structure** (supports environment variable expansion):

```yaml
destinations:
  - id: hetzner-s3
    repository: s3:https://fsn1.your-objectstorage.com/bucket
    env:
      AWS_ACCESS_KEY_ID: ${AWS_KEY}
      AWS_SECRET_ACCESS_KEY: ${AWS_SECRET}
      RESTIC_PASSWORD: ${RESTIC_PASS}
  - id: local-backup
    repository: /mnt/backup/restic
    env:
      RESTIC_PASSWORD: direct-value-also-works

# Optional: Default settings that can be overridden by Docker labels
defaultSchedule: "0 2 * * *" # Cron format: minute hour day month weekday
defaultRetention: "14d:8w:12m" # Format: daily:weekly:monthly
defaultStopAttached: true # Stop containers when backing up volumes
```

Environment variables in config.yml are expanded using `${VAR_NAME}` or `$VAR_NAME` syntax.

**Default settings**: Config can define `defaultSchedule`, `defaultRetention`, and `defaultStopAttached` that apply to all backup targets unless overridden by Docker labels. Priority: Label > Config default > Hardcoded default (schedule: "0 3 \* \* _" for volumes, "30 2 _ \* \*" for DBs; retention: "7d:4w:6m"; stopAttached: false).

### Label-Driven Configuration

All backup configuration lives in Docker labels with namespace `eu.polarnight.marina.*`. See `labels.txt` for reference.

**Volume backup labels** (on volumes):

```yaml
eu.polarnight.marina.enabled: "true"
eu.polarnight.marina.schedule: "0 3 * * *" # Standard cron (5 fields)
eu.polarnight.marina.instanceID: "hetzner-s3" # Maps to config.yml
eu.polarnight.marina.retention: "7d:14w:6m" # daily:weekly:monthly
eu.polarnight.marina.paths: "/" # Relative to volume/_data
eu.polarnight.marina.stopAttached: "true" # Stop containers using volume
```

**DB backup labels** (on DB containers):

```yaml
eu.polarnight.marina.db: "postgres" # postgres|mysql|mariadb|mongo|redis
eu.polarnight.marina.dump.args: "--clean,--if-exists"
```

### Data Flow Patterns

1. **Volume backups**:

   - Paths constructed as `/var/lib/docker/volumes/{name}/_data/{path}`
   - If `stopAttached=true`, stops non-readonly mounted containers before backup
   - Pre/post hooks execute in _first attached container_ (`AttachedCtrs[0]`)

2. **DB backups**:

   - Dump executed _inside DB container_ via `docker exec` → staged to `{StagingDir}/db/{name}/{timestamp}`
   - Commands built per DB kind in `buildDumpCmd()` (see `runner.go:155-180`)
   - Staging directory cleaned up after backup
   - Pre/post hooks execute in the DB container itself

3. **Retention**:
   - Applied _after every backup_ via `DeleteOldSnapshots()`
   - Parsed from label like `"7d:14w:6m"` → `--keep-daily 7 --keep-weekly 14 --keep-monthly 6`
   - Defaults: 7 daily, 4 weekly, 6 monthly (see `helpers/retention.go`)

### Critical Patterns

**Dynamic job management**: Runner tracks scheduled jobs in `scheduledJobs` map (target ID → cron.EntryID); `SyncTargets()` diffs current vs new targets to add/remove/update jobs without restart

**Multi-destination support**: Runner accepts a map of `BackupDestination` objects keyed by ID; each backup target references a destination by ID, and the runner looks it up at execution time

**Event debouncing**: Docker event listener debounces events (2s delay) to prevent excessive rediscovery during rapid container lifecycle changes (e.g., compose down/up)

**Deferred cleanup**: Pre/post hooks and container restarts use `defer` to ensure cleanup even on error (see `runner.go`)

**Read-only volume detection**: Skips stopping containers mounted with `Mode == "ro"` to avoid unnecessary disruption

**Cron parser**: Uses `robfig/cron/v3` with 5-field standard format (minute hour dom month dow), not 6-field with seconds

**Shell invocation**: All hooks/dumps use `/bin/sh -lc` to ensure login shell and proper env loading

**Error handling**: Errors bubble up but post-hooks/cleanup still execute via defer; runner logs job failures but continues scheduling

**Environment variable expansion**: Config loader uses regex to match `${VAR}` and `$VAR` patterns and expands them using `os.Getenv()`

## Development Workflows

**Build**: `go build -o marina ./cmd/manager`

**Run locally** (requires Docker socket):

```bash
# Set up environment variables referenced in config.yml
export RESTIC_PASSWORD=test
export AWS_ACCESS_KEY_ID=your-key
export AWS_SECRET_ACCESS_KEY=your-secret

# Run with config file
./marina

# Or specify custom config location
export CONFIG_FILE=/path/to/config.yml
./marina
```

**Test discovery** without scheduling:

```go
disc, _ := docker.NewDiscoverer()
targets, _ := disc.Discover(ctx)
// Inspect targets slice
```

## Project Conventions

- **Package structure**: `cmd/` for binaries, `internal/` for libraries (not importable outside module)
- **Interfaces**: `BackupDestination` could support backends beyond Restic, but currently only Restic impl exists
- **Error wrapping**: Use `fmt.Errorf("context: %w", err)` for wrappable errors throughout
- **Logging**: Runner accepts `Logf func(string, ...any)` for structured logging flexibility
- **No tests**: Project currently has no test files; consider adding for `helpers/` parsing logic
- **Config format**: `config.yml` defines backup destinations (mapped by ID); destinations include repository URL and environment variables (credentials, etc.)
- **Configuration philosophy**: Backup destinations in config.yml, all other configuration (schedule, retention, hooks) via Docker labels

## Planned Features

- **Recovery**: Restore operations from Restic snapshots
- **Web Interface**: Status dashboard and log viewer for backup jobs
- **Mesh Mode**: Federation of multiple Marina instances across servers with unified web interface

## Docker Compose Example

See `docker-compose.example.yml` for a complete deployment example with:

- Marina manager container with Docker socket access
- Volume and database backup examples with labels
- Staging directory mount for DB dumps
- S3/local backend configuration

## Context

Use context7 MCP to check how to do stuff in the current Go version.
