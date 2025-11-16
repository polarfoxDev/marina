# Marina

**Docker-native backup orchestration using Restic**

Marina is a backup orchestrator that discovers and backs up Docker volumes and databases based on Docker labels. It uses [Restic](https://restic.net/) as the backup backend and supports multiple backup destinations (S3, local, and any Restic-compatible storage).

## Project Status

> [!CAUTION]
> Marina is currently in **early beta**. The core functionality is working, but the API and configuration format may change at any time.
> There are no migration paths yet, you might need to reconfigure and delete existing backups and the marina database when upgrading to a new version.

Planned features:

- Recovery operations from Restic snapshots

## Features

- **Label-driven configuration**: Define backups using Docker labels on volumes and containers
- **Multiple backup destinations**: S3, local filesystem, or any Restic repository
- **Database dumps**: Native support for PostgreSQL, MySQL, MariaDB, MongoDB, and Redis
- **Volume backups**: Back up Docker volumes with optional container stop/start
- **Dynamic discovery**: Automatically detects new/removed containers and volumes
- **Flexible scheduling**: Per-destination cron schedules
- **Retention policies**: Configurable daily/weekly/monthly retention per instance
- **Pre/post hooks**: Execute commands before and after backups
- **Web Interface**: React-based dashboard for monitoring backup status and logs
- **Mesh Mode**: Connect multiple Marina instances for unified monitoring across servers
- **REST API**: Query backup status, logs, and schedules programmatically

## Quick Start

### 1. Create a config file

Create a `config.yml` file with your backup instances:

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

# Optional mesh configuration for multi-node setups
mesh:
  nodeName: ${NODE_NAME}
  authPassword: ${MARINA_AUTH_PASSWORD}
  peers:
    - http://marina-node2:8080
    - http://marina-node3:8080
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
      dev.polarfox.marina.enabled: "true"
      dev.polarfox.marina.db: "postgres"
      dev.polarfox.marina.instanceID: "local-backup"

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
      dev.polarfox.marina.enabled: "true"
      dev.polarfox.marina.instanceID: "local-backup"
      dev.polarfox.marina.paths: "/"
      dev.polarfox.marina.stopAttached: "true"
```

### 3. Start Marina

```bash
docker-compose up -d
```

Marina will automatically discover and schedule backups for any volumes or containers with the appropriate labels.

### 4. Monitor backups

Access the web interface:

```bash
# Open in browser
open http://localhost:8080
```

Or check the logs:

```bash
docker-compose logs -f marina
```

Or query the API directly:

```bash
# Health check
curl http://localhost:8080/api/health

# Get all schedules (includes mesh peers if configured)
curl http://localhost:8080/api/schedules | jq

# Get backup status for an instance
curl http://localhost:8080/api/status/local-backup | jq

# Get logs for a specific job
curl http://localhost:8080/api/logs/job/1 | jq
```

## Docker Labels Reference

Marina uses labels with the namespace `dev.polarfox.marina.*` to configure backups.

### Common Labels (volumes and containers)

| Label                            | Required | Description                                       | Example                 |
| -------------------------------- | -------- | ------------------------------------------------- | ----------------------- |
| `dev.polarfox.marina.enabled`    | Yes      | Enable backup for this target                     | `"true"`                |
| `dev.polarfox.marina.instanceID` | Yes      | Which backup destination to use (from config.yml) | `"local-backup"`        |
| `dev.polarfox.marina.tags`       | No       | Comma-separated tags for Restic                   | `"env:prod,service:db"` |
| `dev.polarfox.marina.pre`        | No       | Command to run before backup                      | `"echo Starting"`       |
| `dev.polarfox.marina.post`       | No       | Command to run after backup                       | `"echo Done"`           |

### Volume-Specific Labels

| Label                              | Required | Description                               | Example                     |
| ---------------------------------- | -------- | ----------------------------------------- | --------------------------- |
| `dev.polarfox.marina.paths`        | No       | Paths to backup (relative to volume root) | `"/"` or `"uploads,config"` |
| `dev.polarfox.marina.stopAttached` | No       | Stop attached containers during backup    | `"true"`                    |
| `dev.polarfox.marina.exclude`      | No       | Exclude patterns (comma-separated)        | `"*.tmp,cache/*"`           |

### Database Container Labels

| Label                           | Required | Description               | Example                                                                          |
| ------------------------------- | -------- | ------------------------- | -------------------------------------------------------------------------------- |
| `dev.polarfox.marina.db`        | Yes      | Database type             | `"postgres"`, `"mysql"`, `"mariadb"`, `"mongo"`, `"redis"`                       |
| `dev.polarfox.marina.dump.args` | No       | Additional dump arguments | `"--clean,--if-exists"` (PostgreSQL)<br>`"-uroot,-p${PASSWORD}"` (MySQL/MariaDB) |

**Important for MySQL/MariaDB**: Pass credentials via `dump.args` using `-uroot,-pPASSWORD` format (no spaces after commas). Do not set `MYSQL_PWD` environment variable as it interferes with container initialization.

## Configuration

Marina uses a two-tier configuration approach:

1. **config.yml**: Defines backup instances (repositories, credentials, schedules) and optional mesh networking
2. **Docker labels**: Define what to backup and target-specific settings

See [config.example.yml](config.example.yml) for a complete configuration example including mesh mode setup and [docker-compose.example.yml](docker-compose.example.yml) for a full deployment example.

## Documentation

- [Dynamic Discovery](docs/dynamic-discovery.md) - How Marina detects changes automatically
- [Architecture](docs/architecture-diagram.md) - System design and data flow
- [Web Interface](docs/web-interface.md) - React dashboard and mesh mode
- [Logging](docs/logging.md) - Job logging and status tracking

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

## License

This project is open source. See the repository for license details.

## Contributing

Contributions are welcome! Please open an issue or pull request on GitHub.
