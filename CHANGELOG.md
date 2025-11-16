# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/polarfoxDev/marina/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/polarfoxDev/marina/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/polarfoxDev/marina/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/polarfoxDev/marina/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/polarfoxDev/marina/releases/tag/v0.1.0
