# Marina - Docker Backup Orchestrator

## Architecture Overview

Marina is a Docker label-based backup orchestrator that uses Restic as its backend. The system discovers backup targets by scanning Docker labels on volumes and containers, schedules backups via cron, and executes them with application-aware hooks.

**Core workflow**: Discovery (Docker labels) → Scheduling (cron jobs) → Execution (Runner) → Backend (Restic)

**Dynamic Discovery**: Marina continuously monitors Docker for changes via event listener and periodic polling, automatically adding/removing/updating backup jobs without requiring restarts.

### Key Components

- **`internal/config/config.go`**: Parses `config.yml` and expands environment variable references (`${VAR}` or `$VAR`)
- **`internal/docker/discovery.go`**: Scans Docker API for volumes/containers with `dev.polarfox.marina.*` labels, builds `BackupTarget` models
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
instances:
  - id: hetzner-s3
    repository: s3:https://fsn1.your-objectstorage.com/bucket
    schedule: "0 2 * * *" # Cron schedule for this instance's backups
    retention: "30d:12w:24m" # Optional: instance-specific retention
    env:
      AWS_ACCESS_KEY_ID: ${AWS_KEY}
      AWS_SECRET_ACCESS_KEY: ${AWS_SECRET}
      RESTIC_PASSWORD: ${RESTIC_PASS}
  - id: local-backup
    repository: /mnt/backup/restic
    schedule: "0 3 * * *"
    env:
      RESTIC_PASSWORD: direct-value-also-works

# Global defaults that can be overridden by instance config or Docker labels
retention: "14d:8w:12m" # Format: daily:weekly:monthly
stopAttached: true # Stop containers when backing up volumes

# Optional mesh configuration for multi-node federation
mesh:
  nodeName: ${NODE_NAME} # Optional custom node name
  authPassword: ${MARINA_AUTH_PASSWORD} # Password for mesh auth and dashboard
  peers:
    - http://marina-node2:8080
    - http://marina-node3:8080
```

Environment variables in config.yml are expanded using `${VAR_NAME}` or `$VAR_NAME` syntax.

**Configuration hierarchy**: Instance config > Global config > Docker labels > Hardcoded defaults

- Schedule: Required per-instance in config.yml
- Retention: Instance-specific (optional) > Global `retention` > Label > Hardcoded default "7d:4w:6m"
- StopAttached: Global `stopAttached` > Label > Hardcoded default false

### Label-Driven Configuration

All backup configuration lives in Docker labels with namespace `dev.polarfox.marina.*`. See `labels.txt` for reference.

**Volume backup labels** (on volumes):

```yaml
dev.polarfox.marina.enabled: "true"
dev.polarfox.marina.instanceID: "hetzner-s3" # Maps to config.yml instance (schedule comes from there)
dev.polarfox.marina.retention: "7d:14w:6m" # Optional: daily:weekly:monthly (overrides global/instance retention)
dev.polarfox.marina.paths: "/" # Relative to volume/_data
dev.polarfox.marina.stopAttached: "true" # Stop containers using volume
```

**DB backup labels** (on DB containers):

```yaml
dev.polarfox.marina.enabled: "true"
dev.polarfox.marina.db: "postgres" # postgres|mysql|mariadb|mongo|redis
dev.polarfox.marina.instanceID: "hetzner-s3" # Maps to config.yml instance
dev.polarfox.marina.dump.args: "--clean,--if-exists" # For postgres
# For MySQL/MariaDB, pass credentials via dump.args (no MYSQL_PWD needed):
# dev.polarfox.marina.dump.args: "-uroot,-p${PASSWORD}"
```

**Important for MySQL/MariaDB**: Do NOT set `MYSQL_PWD` environment variable as it interferes with container initialization. Instead, pass credentials via `dump.args` label using `-uroot,-pPASSWORD` format (no spaces after commas).

### Data Flow Patterns

1. **Volume backups**:

   - Volume data copied to staging directory via temporary Alpine container with volume mounted read-only
   - Temporary container started with both the source volume mounted at `/source` (read-only) and the staging volume (same as Marina's `/backup`) mounted at `/backup`
   - Marina automatically detects its staging volume by inspecting its own container mounts
   - Data copied using `cp -a` to preserve attributes into `/backup/volume/{name}/{timestamp}/`
   - Staging subdirectory cleaned up automatically after backup completes
   - If `stopAttached=true`, stops non-readonly mounted containers before backup
   - Pre/post hooks execute in _first attached container_ (`AttachedCtrs[0]`)

2. **DB backups**:

   - Dump executed _inside DB container_ via `docker exec` to `/tmp/marina-{timestamp}`
   - Dump file copied out using Docker API to Marina's staging directory: `/backup/db/{name}/{timestamp}`
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
- **Tests**: Project has unit tests in `*_test.go` files and integration tests in `tests/integration/`
- **Config format**: `config.yml` defines backup instances (mapped by ID); instances include repository URL and environment variables (credentials, etc.)
- **Configuration philosophy**: Backup instances in config.yml, all other configuration (schedule, retention, hooks) via Docker labels or config.yml

## Planned Features

- **Recovery**: Restore operations from Restic snapshots

## Implemented Features

- **Web Interface**: React-based dashboard for monitoring backup status and logs (in `web/` directory)
- **Mesh Mode**: Multi-node federation allowing unified monitoring across multiple Marina instances
  - Configured via `mesh` section in config.yml
  - Mesh client in `internal/mesh/client.go` fetches data from peer nodes
  - Web interface automatically displays data from all connected nodes
  - Authentication via shared password across mesh nodes

## Docker Compose Example

See `docker-compose.example.yml` for a complete deployment example with:

- Marina manager container with Docker socket access
- Volume and database backup examples with labels
- Staging directory mount for DB dumps
- S3/local backend configuration

## Context

Use context7 MCP to check how to do stuff in the current Go version.
