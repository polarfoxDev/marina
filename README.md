# Marina

**Docker-native backup orchestration using Restic**

Marina is a backup orchestrator that backs up Docker volumes and databases based on configuration in config.yml. It uses [Restic](https://restic.net/) as the backup backend and supports multiple backup destinations (S3, local, and any Restic-compatible storage). In addition, Marina supports custom Docker image backends for maximum flexibility.

## Project Status

> [!CAUTION]
> Marina is currently in **early beta**. The core functionality is working, but the API and configuration format may change at any time.
> There are no migration paths yet, you might need to reconfigure and delete existing backups and the marina database when upgrading to a new version.

Planned features:

- Recovery operations from Restic snapshots

## Features

- **Config-driven targets**: Define backup targets in config.yml for easier management
- **Multiple backup destinations**: S3, local filesystem, or any Restic repository
- **Custom backup backends**: Use custom Docker images for alternative backup destinations
- **Database dumps**: Native support for PostgreSQL, MySQL, MariaDB, MongoDB, and Redis
- **Volume backups**: Back up Docker volumes with optional container stop/start
- **Dynamic discovery**: Automatically verifies configured containers and volumes exist
- **Flexible scheduling**: Per-destination cron schedules
- **Retention policies**: Configurable daily/weekly/monthly retention per instance
- **Pre/post hooks**: Execute commands before and after backups
- **Web Interface**: React-based dashboard for monitoring backup status and logs
- **Mesh Mode**: Connect multiple Marina instances for unified monitoring across servers
- **REST API**: Query backup status, logs, and schedules programmatically

## Quick Start

### 1. Create a config file

Create a `config.yml` file with your backup instances and targets:

```yaml
instances:
  - id: local-backup
    repository: /mnt/backup/restic
    schedule: "0 2 * * *"  # Daily at 2 AM
    retention: "7d:4w:6m"  # 7 daily, 4 weekly, 6 monthly
    resticTimeout: "10m"   # Optional: backup timeout for the restic command (default: 60m)
    env:
      RESTIC_PASSWORD: your-restic-password
    targets:
      # Shorthand syntax (recommended)
      - "volume:app-data"
      - "db:postgres"      # dbKind auto-detected from container image
      
      # Full object syntax (use when you need custom options)
      # - volume: app-data
      #   paths: ["/data"]
      #   stopAttached: true
      # - db: postgres
      #   dbKind: postgres   # Override auto-detection
      #   preHook: "psql -U myapp -c 'CHECKPOINT;'"

# Optional global defaults
stopAttached: true  # Stop containers when backing up volumes
resticTimeout: "60m"      # Global timeout for all restic commands (default: 60m)

# Optional mesh configuration for multi-node setups
mesh:
  nodeName: ${NODE_NAME}
  authPassword: ${MARINA_AUTH_PASSWORD}
  peers:
    - http://marina-node2:8080
    - http://marina-node3:8080
```

See [config.example.yml](config.example.yml) for more examples including S3 configuration and additional target options.

### 2. Set up your docker-compose.yml

```yaml
services:
  # Marina backup orchestrator
  marina:
    image: ghcr.io/polarfoxdev/marina:latest
    container_name: marina
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      # IMPORTANT: Must be a bind mount from the host, not a Docker volume
      # Marina uses this to stage backup data and automatically detects
      # the host path for creating temporary containers
      - ./staging:/backup
      - marina-data:/var/lib/marina
      - ./config.yml:/app/config.yml:ro
    ports:
      - "8080:8080"
    environment:
      RESTIC_PASSWORD: "${RESTIC_PASSWORD}"
      POSTGRES_PASSWORD: "${POSTGRES_PASSWORD}"

  # Example: PostgreSQL database (container name must match config.yml)
  postgres:
    image: postgres:16-alpine
    container_name: postgres  # Referenced in config.yml targets
    environment:
      POSTGRES_DB: myapp
      POSTGRES_PASSWORD: "${POSTGRES_PASSWORD}"
      PGPASSWORD: "${POSTGRES_PASSWORD}"
    volumes:
      - postgres-data:/var/lib/postgresql/data

  # Example: Application with volumes (volume name must match config.yml)
  app:
    image: myapp:latest
    volumes:
      - app-data:/app/data  # Referenced in config.yml targets

volumes:
  marina-data:
  postgres-data:
  app-data:
```

**Note**: Container and volume names must match those specified in your config.yml targets.

### 3. Start Marina

```bash
docker-compose up -d
```

Marina will automatically verify and schedule backups for the configured targets.

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

## Configuration Reference

Marina uses a single configuration file (`config.yml`) to define backup instances and their targets.

### Target Configuration

Each backup instance can have multiple targets configured. Targets support two syntaxes:

**Shorthand syntax** (recommended for simple targets):
```yaml
targets:
  - "volume:app-data"
  - "db:postgres"
```

**Full object syntax** (for advanced options):
```yaml
targets:
  - volume: app-data
    paths: ["/data"]
    stopAttached: true
  - db: postgres
    dbKind: postgres
    dumpArgs: ["--clean"]
```

#### Volume Targets

| Field          | Required | Description                                       | Example                  |
| -------------- | -------- | ------------------------------------------------- | ------------------------ |
| `volume`       | Yes      | Volume name (as shown in `docker volume ls`)      | `"app-data"`             |
| `paths`        | No       | Paths to backup (relative to volume root)         | `["/", "/data"]`         |
| `stopAttached` | No       | Stop attached containers during backup            | `true`                   |
| `preHook`      | No       | Command to run before backup (in first container) | `"echo Starting"`        |
| `postHook`     | No       | Command to run after backup (in first container)  | `"echo Done"`            |

#### Database Targets

| Field      | Required | Description                                          | Example                                                    |
| ---------- | -------- | ---------------------------------------------------- | ---------------------------------------------------------- |
| `db`       | Yes      | Container name (as shown in `docker ps`)             | `"postgres"`, `"my-mysql"`                                 |
| `dbKind`   | No*      | Database type (auto-detected if not provided)        | `"postgres"`, `"mysql"`, `"mariadb"`, `"mongo"`, `"redis"` |
| `dumpArgs` | No       | Additional arguments for dump command                | `["--clean", "--if-exists"]` (PostgreSQL)                  |
| `preHook`  | No       | Command to run before backup (inside DB container)   | `"psql -U myapp -c 'CHECKPOINT;'"`                         |
| `postHook` | No       | Command to run after backup (inside DB container)    | `"echo Done"`                                              |

**\*dbKind auto-detection**: Marina automatically detects the database type from the container image name (e.g., `postgres:16` → `postgres`). You can override this by explicitly specifying `dbKind`. If detection fails and no `dbKind` is provided, the target will be skipped.

**Important for MySQL/MariaDB**: Pass credentials via `dumpArgs` using `["-uroot", "-pPASSWORD"]` format. Do not set `MYSQL_PWD` environment variable as it interferes with container initialization.

> **Note**: Marina automatically generates a single tag for each backup in the format `type:name` (e.g., `volume:mydata` for volume backups or `db:postgres` for database backups).

## Configuration

Marina uses a single configuration file (`config.yml`) that defines:

1. **Backup instances**: Repositories, credentials, schedules, and retention policies
2. **Backup targets**: Volumes and databases to back up for each instance
3. **Global defaults**: Optional default values for all instances
4. **Mesh networking**: Optional multi-node federation for unified monitoring

### Important: Staging Directory Mount

Marina requires `/backup` to be mounted as a **host bind mount** (not a Docker volume). This directory is used for:

- **Volume backups**: Staging data from Docker volumes before sending to backup destination
- **Database dumps**: Temporarily storing database dumps before backup
- **Custom backends**: Providing backup data to custom Docker image backends

Marina automatically detects the actual host path where `/backup` is mounted by inspecting its own container. This host path is then used to create bind mounts in temporary containers for:

- Volume copy operations (temporary Alpine containers)
- Custom image backend containers (scoped to `/backup/{instanceID}`)

**Example mounting options**:

```yaml
volumes:
  - ./staging:/backup              # Relative path
  - /var/lib/marina/staging:/backup  # Absolute path
  - $HOME/marina-staging:/backup   # With environment variable
```

**Note**: Each custom backend container only sees its own instance's data at `/backup/{instanceID}` for security and isolation.

### macOS Compatibility Note

When using Restic on macOS with Docker Desktop, **use named Docker volumes instead of bind mounts** for the repository storage. Bind mounts on macOS can cause "bad file descriptor" errors during fsync operations due to filesystem compatibility issues between the Docker VM and macOS.

**Recommended for macOS**:

```yaml
services:
  marina:
    volumes:
      - restic-repo:/repo  # Named volume (recommended on macOS)
      - ./staging:/backup  # Staging can still use bind mount

volumes:
  restic-repo:  # Let Docker manage the repository
```

**Linux users can safely use bind mounts** for both `/backup` and repository paths without these issues.

### Custom Backup Backends

In addition to Restic, Marina supports **custom Docker image backends** that allow you to implement your own backup logic. This is useful for:

- Backing up to services not supported by Restic
- Implementing custom backup formats or compression
- Integrating with proprietary backup systems
- Custom data transformation before backup

**Configuration example**:

```yaml
instances:
  - id: custom-s3
    customImage: your-registry/your-backup-image:latest
    schedule: "0 3 * * *"
    env:
      BACKUP_ENDPOINT: https://backup.example.com
      BACKUP_TOKEN: ${BACKUP_TOKEN}
```

**How it works**:

1. Marina stages backup data in `/backup/{instanceID}` on the host
2. Marina creates a container from your custom image
3. Only that instance's subfolder is mounted at `/backup` in the container (scoped access)
4. Your container's `/backup.sh` script executes with access to the staged data
5. Marina captures the exit code (0 = success, non-zero = failure) and logs

**Your custom image must**:

- Have a `/backup.sh` script (or configure a different entrypoint)
- Read backup data from `/backup` directory
- Exit with code 0 on success, non-zero on failure
- Handle its own retention policy (Marina's retention config is informational only)

See the [custom backup image example](examples/custom-backup-image/) for a complete working example and [custom backends documentation](docs/custom-backends.md) for detailed implementation guide.

## Documentation

See [config.example.yml](config.example.yml) for a complete configuration example including mesh mode setup and [docker-compose.example.yml](docker-compose.example.yml) for a full deployment example.

- [Custom Backends](docs/custom-backends.md) - Build your own backup backend using Docker images
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
