# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **BREAKING**: Removed `dev.polarfox.marina.tags` label - Marina now auto-generates descriptive tags for each backup
  - Volume backups: `type:volume`, `volume:<name>`, `instance:<id>`
  - Database backups: `type:db`, `db:<kind>`, `container:<name>`, `instance:<id>`

### Fixed

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

[Unreleased]: https://github.com/polarfoxDev/marina/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/polarfoxDev/marina/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/polarfoxDev/marina/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/polarfoxDev/marina/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/polarfoxDev/marina/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/polarfoxDev/marina/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/polarfoxDev/marina/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/polarfoxDev/marina/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/polarfoxDev/marina/releases/tag/v0.1.0
