# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.7.0] - 2025-11-19

### Changed

- **BREAKING**: Default config file path changed from `config.yml` to `/config.yml` for Docker-friendly defaults
  - Mount config to `/config.yml` in container: `- ./config.yml:/config.yml:ro`
  - Override with `CONFIG_FILE` env var if needed: `CONFIG_FILE=/app/config.yml`
  - See updated documentation in README for examples
- **BREAKING**: Moved `nodeName` and `authPassword` from `mesh` section to top-level config fields
  - `mesh.nodeName` → `nodeName` (top-level field)
  - `mesh.authPassword` → `authPassword` (top-level field)
  - The `mesh` section now only contains `peers` list for actual mesh networking
  - This makes single-node authentication and node naming more intuitive
  - Example single-node config: `nodeName: my-server` and `authPassword: secret` at top level
  - Example mesh config: Add `mesh: { peers: [...] }` in addition to the top-level fields
- **BREAKING**: Simplified peer federation configuration and removed nested `mesh` section
  - `mesh.peers` → `peers` (top-level field)
  - Removed `MeshConfig` struct entirely - just use top-level `peers` array
  - Terminology changed from "mesh mode" to "peer federation" for clarity
  - Internal package renamed from `internal/mesh` to `internal/peer`
  - Configuration is now flatter and more intuitive: `nodeName`, `authPassword`, and `peers` all at top level
  - Example: `peers: ["http://node2:8080", "http://node3:8080"]`

## [0.6.0] - 2025-11-19

### Changed

- **BREAKING**: Removed direct environment variable fallbacks for `NODE_NAME` and `MARINA_AUTH_PASSWORD`
  - These must now be configured in `config.yml` under `mesh.nodeName` and `mesh.authPassword`
  - Environment variable expansion still works (e.g., `nodeName: ${NODE_NAME}`)
  - Node name defaults to hostname if not specified in mesh config
  - Auth password has no default - leave empty to disable authentication
- **BREAKING**: Removed direct `CORS_ORIGINS` environment variable support
  - Use `corsOrigins` array in `config.yml` instead
  - Environment variable expansion works: `corsOrigins: [${CORS_ORIGIN_1}, ${CORS_ORIGIN_2}]`
- **BREAKING**: Removed direct `DB_PATH` environment variable support
  - Use `dbPath` field in `config.yml` instead (defaults to `/var/lib/marina/marina.db`)
  - Environment variable expansion works: `dbPath: ${DB_PATH}`
- **BREAKING**: Completely removed Docker label-based discovery system
  - All backup targets (volumes and databases) must now be defined in `config.yml` under the `targets` field of each instance
  - Removed support for `dev.polarfox.marina.*` Docker labels
  - Target validation now happens at backup time, not at startup
  - Volumes and containers are looked up by name during backup execution
  - Fails with clear error messages if configured volume/container doesn't exist
- **BREAKING**: Removed shorthand syntax for target configuration (e.g., `"volume:name"` and `"db:name"`)
  - Use proper YAML syntax instead: `volume: name` or `db: name`
  - This makes configuration clearer and removes unnecessary string parsing
- Configuration now fully supports environment variable expansion in all fields
  - All config fields support `${VAR}` or `$VAR` syntax
  - Includes new fields: `dbPath`, `apiPort`, `corsOrigins`
- Simplified architecture: no more periodic rediscovery or Docker event listening
  - Removed `internal/docker/discovery.go` and `internal/docker/events.go`
  - No more `DISCOVERY_INTERVAL` or `ENABLE_EVENTS` environment variables
  - Configuration changes require restart (edit config.yml and restart Marina)

### Added

- New `dbPath` field in config.yml for database path configuration
- New `apiPort` field in config.yml for API server port configuration
- New `corsOrigins` array in config.yml for additional CORS origins

### Removed

- **BREAKING**: Removed `internal/docker/discovery.go` - discovery system no longer needed
- **BREAKING**: Removed `internal/docker/events.go` - Docker event listener no longer needed
- **BREAKING**: Removed dynamic discovery and automatic rescheduling features

### Fixed

- Manager now respects `mesh.nodeName` from config.yml instead of always reading `NODE_NAME` environment variable directly (consistent with API server behavior)
- Fixed comment typo in manager: "resticresticTimeout" → "restic timeout"

## [0.5.0] - 2025-11-19

### Added

- File size validation: Backups now fail if ALL files are empty (0 bytes), preventing silent failures from database dumps or volume copies. Individual empty files are allowed (normal for lock files, .gitkeep, etc.) as long as at least one file has content

### Changed
- **BREAKING**: Removed `dev.polarfox.marina.tags` label - Marina now auto-generates a single tag for each backup
  - Volume backups: `volume:<name>`
  - Database backups: `db:<kind>`
- `dbKind` is now optional for database targets (auto-detected from container image if not specified)
- Web UI: Log level filter now defaults to INFO instead of "All Levels"
- Web UI: Log level filtering now works hierarchically (DEBUG shows all logs, INFO shows INFO+WARN+ERROR, WARN shows WARN+ERROR, ERROR shows only ERROR)

### Fixed

- Web UI now loads logs one final time after job completion to prevent missing final log entries when transitioning from "in_progress" to finished states

## [0.4.1] - 2025-11-16

### Fixed

- Fixed panic when Docker events contain IDs shorter than 12 characters (slice bounds out of range error)
- Web UI: "Next run" time now correctly displays future times (e.g., "in 5 minutes") instead of showing "Just now"

## [0.4.0] - 2025-11-16

### Added

- Custom Docker image backend support as an alternative to Restic
- `customImage` field in instance configuration to specify custom backup images
- Custom image backend containers only have access to their instance's subfolder (`/backup/{instanceID}` mounted to `/backup`)
- Backend interface to support multiple backup implementations
- Example custom backup image with random failure simulation (75% success rate)
- Environment variables `MARINA_INSTANCE_ID` and `MARINA_HOSTNAME` passed to custom images
- Documentation and README for creating custom backup images
- `resticTimeout` configuration option (global and per-instance) for backup operations with Go duration format (e.g., "5m", "30s", "1h")
- Restic automatically runs `unlock` before backups to clear stale locks from previous runs

### Changed

- **BREAKING**: Staging directory (`/backup`) now requires host bind mount instead of Docker named volume
- Marina now automatically detects the actual host path for `/backup` by inspecting its own container mounts
- Volume copy containers now use host bind mount for staging directory
- Removed automatic staging volume detection logic
- All docker-compose examples updated to use `./staging:/backup` bind mount
- Refactored backend to use interface-based design (Restic and Custom Image implementations)
- `repository` field in config is now optional when using `customImage`
- Backend initialization and execution now supports both Restic and custom Docker containers

## [0.3.1] - 2025-11-16

### Fixed

- Actually added changes from 0.3.0 release. Those were missing due to a merge issue.

## [0.3.0] - 2025-11-16

### Added

- System logs viewer in web UI with filtering by node and log level
- API endpoint `GET /api/logs/system` to query system logs (non-job-specific logs)
- Mesh federation support for system logs - view logs from all nodes
- Navigation link in header to access System Logs page

### Changed

- Updated web UI badge design: gray background now starts after type label (vol/db) for improved visual clarity

## [0.2.1] - 2025-11-16

### Removed

- **BREAKING**: Removed `dev.polarfox.marina.retention` label support. The label was parsed but never actually used - retention policies have always been controlled exclusively by instance configuration in `config.yml`. All targets within an instance share the same retention policy. Remove this label from your volumes and containers if present.

### Changed

- Clarified documentation that retention is configured per-instance, not per-target
- Updated all examples to remove the unused retention label

## [0.2.0] - 2025-11-16

### Added

- Mesh mode to have a unified dashboard for multiple Marina instances
- Password protection for the web dashboard and API
- CHANGELOG.md to track all notable changes
- Automated release workflow on merge to main

## [0.1.2] - 2025-11-15

### Fixed

- Build process improvements
- Fix labels to match repository name

## [0.1.1] - 2025-11-15

### Fixed

- Build process improvements

## [0.1.0] - 2025-11-15

### Added

- Initial release of Marina Docker Backup Orchestrator
- Docker label-based backup discovery
- Restic backend integration
- Volume and database backup support
- Cron-based scheduling
- Pre/post backup hooks
- Container stop/start management
- Multi-destination backup support
- S3 and local storage backends
- PostgreSQL, MySQL, MariaDB, MongoDB, Redis database support
- Docker event listener for dynamic discovery
- Configuration via config.yml and Docker labels

[Unreleased]: https://github.com/polarfoxDev/marina/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/polarfoxDev/marina/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/polarfoxDev/marina/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/polarfoxDev/marina/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/polarfoxDev/marina/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/polarfoxDev/marina/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/polarfoxDev/marina/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/polarfoxDev/marina/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/polarfoxDev/marina/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/polarfoxDev/marina/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/polarfoxDev/marina/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/polarfoxDev/marina/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/polarfoxDev/marina/releases/tag/v0.1.0
