# Marina - Docker Backup Orchestrator

## Architecture Overview

Marina is a Docker label-based backup orchestrator that uses Restic as its backend. The system discovers backup targets by scanning Docker labels on volumes and containers, schedules backups via cron, and executes them with application-aware hooks.

**Core workflow**: Discovery (Docker labels) → Scheduling (cron jobs) → Execution (Runner) → Backend (Restic)

### Key Components

- **`internal/docker/discovery.go`**: Scans Docker API for volumes/containers with `eu.polarnight.marina.*` labels, builds `BackupTarget` models
- **`internal/runner/runner.go`**: Orchestrates backup execution—handles pre/post hooks, container stop/start, and delegates to backend
- **`internal/backend/restic.go`**: Wraps Restic CLI commands (backup, forget, prune) with environment variables
- **`internal/model/model.go`**: Defines `BackupTarget` (volume or DB), `Retention` policy, and job state
- **`cmd/manager/main.go`**: Entry point—creates discoverer, runner, schedules all targets, and blocks

### Label-Driven Configuration

All backup configuration lives in Docker labels with namespace `eu.polarnight.marina.*`. See `labels.txt` for reference.

**Volume backup labels** (on volumes):

```yaml
eu.polarnight.marina.enabled: "true"
eu.polarnight.marina.type: "volume"
eu.polarnight.marina.schedule: "0 3 * * *" # Standard cron (5 fields)
eu.polarnight.marina.destination: "hetzner-s3" # Maps to config.yml
eu.polarnight.marina.retention: "7d:14w:6m" # daily:weekly:monthly
eu.polarnight.marina.paths: "/" # Relative to volume/_data
eu.polarnight.marina.stopAttached: "true" # Stop containers using volume
```

**DB backup labels** (on DB containers):

```yaml
eu.polarnight.marina.type: "db"
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

**Deferred cleanup**: Pre/post hooks and container restarts use `defer` to ensure cleanup even on error (see `runner.go:75-79, 89-101, 129-133`)

**Read-only volume detection**: Skips stopping containers mounted with `Mode == "ro"` to avoid unnecessary disruption (`runner.go:96-100`)

**Cron parser**: Uses `robfig/cron/v3` with 5-field standard format (minute hour dom month dow), not 6-field with seconds

**Shell invocation**: All hooks/dumps use `/bin/sh -lc` to ensure login shell and proper env loading (`runner.go:76, 130, 138, 145`)

**Error handling**: Errors bubble up but post-hooks/cleanup still execute via defer; runner logs job failures but continues scheduling

## Development Workflows

**Build**: `go build -o marina ./cmd/manager`

**Run locally** (requires Docker socket):

```bash
export RESTIC_REPOSITORY=/tmp/test-repo
export RESTIC_PASSWORD=test
export VOLUME_ROOT=/var/lib/docker/volumes
export STAGING_DIR=/tmp/marina-staging
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
- **Config format**: `config.yml` will define backup destinations (mapped by ID); `main.go` is WIP and currently uses env vars directly—will eventually parse config.yml
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
