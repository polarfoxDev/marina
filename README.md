# Marina

**Docker-native backup orchestration using Restic**

Marina is a backup orchestrator that discovers and backs up Docker volumes and databases based on Docker labels. It uses [Restic](https://restic.net/) as the backup backend and supports multiple backup destinations (S3, local, and any Restic-compatible storage).

## Features

- **Label-driven configuration**: Define backups using Docker labels on volumes and containers
- **Multiple backup destinations**: S3, local filesystem, or any Restic repository
- **Database dumps**: Native support for PostgreSQL, MySQL, MariaDB, MongoDB, and Redis
- **Volume backups**: Back up Docker volumes with optional container stop/start
- **Dynamic discovery**: Automatically detects new/removed containers and volumes
- **Flexible scheduling**: Per-destination cron schedules
- **Retention policies**: Configurable daily/weekly/monthly retention per target
- **Pre/post hooks**: Execute commands before and after backups
- **Web API**: Query backup status and logs via REST API

## Quick Start

### 1. Create a config file

Create a `config.yml` file with your backup destinations:

```yaml
instances:
  - id: local-backup
    repository: /mnt/backup/restic
    schedule: "0 2 * * *"  # Daily at 2 AM
    retention: "7d:4w:6m"  # 7 daily, 4 weekly, 6 monthly
    env:
      RESTIC_PASSWORD: your-restic-password

# Optional global defaults
stopAttached: true  # Stop containers when backing up volumes
```

See [config.example.yml](config.example.yml) for more examples including S3 configuration.

### 2. Add backup labels to your docker-compose.yml

```yaml
services:
  # Marina backup orchestrator
  marina:
    image: ghcr.io/polarfoxdev/marina:latest
    container_name: marina
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - marina-staging:/backup
      - marina-data:/var/lib/marina
      - ./config.yml:/app/config.yml:ro
    ports:
      - "8080:8080"
    environment:
      RESTIC_PASSWORD: "${RESTIC_PASSWORD}"

  # Example: PostgreSQL database with backup labels
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: myapp
      POSTGRES_PASSWORD: "${POSTGRES_PASSWORD}"
      PGPASSWORD: "${POSTGRES_PASSWORD}"
    volumes:
      - postgres-data:/var/lib/postgresql/data
    labels:
      eu.polarnight.marina.enabled: "true"
      eu.polarnight.marina.db: "postgres"
      eu.polarnight.marina.instanceID: "local-backup"
      eu.polarnight.marina.retention: "7d:4w:6m"

  # Example: Application with volume backup
  app:
    image: myapp:latest
    volumes:
      - app-data:/app/data

volumes:
  marina-staging:
  marina-data:
  postgres-data:
  
  # Volume with backup labels
  app-data:
    labels:
      eu.polarnight.marina.enabled: "true"
      eu.polarnight.marina.instanceID: "local-backup"
      eu.polarnight.marina.paths: "/"
      eu.polarnight.marina.stopAttached: "true"
```

### 3. Start Marina

```bash
docker-compose up -d
```

Marina will automatically discover and schedule backups for any volumes or containers with the appropriate labels.

### 4. Monitor backups

Check the logs:

```bash
docker-compose logs -f marina
```

Query the API for status:

```bash
# Health check
curl http://localhost:8080/api/health

# Get backup status for an instance
curl http://localhost:8080/api/status/local-backup | jq

# Get logs for a specific job
curl http://localhost:8080/api/logs/job/1 | jq
```

## Docker Labels Reference

Marina uses labels with the namespace `eu.polarnight.marina.*` to configure backups.

### Common Labels (volumes and containers)

| Label                             | Required | Description                                       | Example                 |
| --------------------------------- | -------- | ------------------------------------------------- | ----------------------- |
| `eu.polarnight.marina.enabled`    | Yes      | Enable backup for this target                     | `"true"`                |
| `eu.polarnight.marina.instanceID` | Yes      | Which backup destination to use (from config.yml) | `"local-backup"`        |
| `eu.polarnight.marina.retention`  | No       | Retention policy (daily:weekly:monthly)           | `"7d:4w:6m"`            |
| `eu.polarnight.marina.tags`       | No       | Comma-separated tags for Restic                   | `"env:prod,service:db"` |
| `eu.polarnight.marina.pre`        | No       | Command to run before backup                      | `"echo Starting"`       |
| `eu.polarnight.marina.post`       | No       | Command to run after backup                       | `"echo Done"`           |

### Volume-Specific Labels

| Label                               | Required | Description                               | Example                     |
| ----------------------------------- | -------- | ----------------------------------------- | --------------------------- |
| `eu.polarnight.marina.paths`        | No       | Paths to backup (relative to volume root) | `"/"` or `"uploads,config"` |
| `eu.polarnight.marina.stopAttached` | No       | Stop attached containers during backup    | `"true"`                    |
| `eu.polarnight.marina.exclude`      | No       | Exclude patterns (comma-separated)        | `"*.tmp,cache/*"`           |

### Database Container Labels

| Label                            | Required | Description               | Example                                                                          |
| -------------------------------- | -------- | ------------------------- | -------------------------------------------------------------------------------- |
| `eu.polarnight.marina.db`        | Yes      | Database type             | `"postgres"`, `"mysql"`, `"mariadb"`, `"mongo"`, `"redis"`                       |
| `eu.polarnight.marina.dump.args` | No       | Additional dump arguments | `"--clean,--if-exists"` (PostgreSQL)<br>`"-uroot,-p${PASSWORD}"` (MySQL/MariaDB) |

**Important for MySQL/MariaDB**: Pass credentials via `dump.args` using `-uroot,-pPASSWORD` format (no spaces after commas). Do not set `MYSQL_PWD` environment variable as it interferes with container initialization.

## Configuration

Marina uses a two-tier configuration approach:

1. **config.yml**: Defines backup destinations (repositories, credentials, schedules)
2. **Docker labels**: Define what to backup and target-specific settings

See [config.example.yml](config.example.yml) for a complete configuration example and [docker-compose.example.yml](docker-compose.example.yml) for a full deployment example.

## Documentation

- [Dynamic Discovery](docs/dynamic-discovery.md) - How Marina detects changes automatically
- [Architecture](docs/architecture-diagram.md) - System design and data flow
- [API Quickstart](QUICKSTART-API.md) - Working with the REST API
- [Web Interface](docs/web-interface.md) - Status dashboard (planned)

## Requirements

- Docker 20.10 or later
- Docker Compose v2 (optional, for easier deployment)
- Restic-compatible storage (included in the Docker image)

## Development

### Build from source

```bash
# Clone the repository
git clone https://github.com/polarfoxDev/marina.git
cd marina

# Build the binaries
go build -o marina ./cmd/manager
go build -o marina-api ./cmd/api

# Or build the Docker image
docker build -t marina:dev .
```

### Run tests

```bash
go test ./...
```

## Project Status

Marina is currently in **beta**. The core functionality is working, but the API and configuration format may change.

Planned features:

- Recovery operations from Restic snapshots
- Multi-node federation (mesh mode)

## License

This project is open source. See the repository for license details.

## Contributing

Contributions are welcome! Please open an issue or pull request on GitHub.
