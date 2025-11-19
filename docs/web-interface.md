# Marina Web Interface

The Marina web interface is a React-based dashboard for monitoring backup status across single or multiple Marina instances. The API server is integrated into the main Docker container and automatically runs alongside the backup manager.

## Features

- **Schedules Overview**: View all backup schedules across all instances
- **Job Status View**: Monitor backup job execution and results
- **Job Details & Logs**: Detailed view with log filtering and search
- **Mesh Mode**: Unified monitoring across multiple Marina nodes
- **Authentication**: Optional password protection for the dashboard

## Web Interface

The web interface is built with React and TypeScript and is located in the `web/` directory.

### Development

```bash
cd web

# Install dependencies
pnpm install

# Start dev server (proxies API requests to localhost:8080)
pnpm run dev

# Build for production
pnpm run build
```

### Production Build

The web interface is automatically built and included in the Docker image:

```dockerfile
# Frontend build stage
FROM node:20-alpine AS frontend
WORKDIR /app
COPY web/package*.json web/pnpm-lock.yaml ./
RUN npm install -g pnpm && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

# Copied into final image
COPY --from=frontend /app/dist /app/web
```

## Peer Federation

When Marina is configured with peer federation, the dashboard automatically:

- Fetches schedules and status from all configured peer nodes
- Groups data by node name for easy organization
- Shows connectivity status and peer count
- Uses shared authentication across all nodes

### Federation Configuration

In your `config.yml`:

```yaml
# Node identification and authentication
nodeName: production-server-1
authPassword: your-secure-password

# Peer federation - connect to other Marina instances
peers:
  - http://marina-node2:8080
  - http://marina-node3:8080
```

All nodes in the federation should use the same `authPassword` for authentication.

## API Server

The API server runs automatically when you start the Marina container and exposes port 8080 by default.

### Configuration

Environment variables:

```bash
API_PORT=8080                           # Port for API server (default: 8080)
DB_PATH=/var/lib/marina/marina.db       # Unified database location
STATIC_DIR=/app/web                     # Directory for React app build
```

### Available Endpoints

#### Health & Status

- `GET /api/health` - Health check
- `GET /api/schedules` - All schedules (includes mesh peers)
- `GET /api/status/{instanceID}` - Job statuses for specific instance

#### Logs

- `GET /api/logs/job/{id}` - Logs for a specific job

#### Authentication

- `POST /api/auth/login` - Login with password (returns JWT token)
- Protected routes require `Authorization: Bearer <token>` header

#### Frontend

- `GET /` - React app (serves from `/app/web`)
- `GET /*` - SPA routing (all non-API routes serve `index.html`)

## Docker Setup

The container runs both services via an entrypoint script:

```yaml
services:
  marina:
    image: marina:latest
    ports:
      - "8080:8080"  # API access
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./config.yml:/app/config.yml:ro
      - marina-data:/var/lib/marina  # For marina.db
    environment:
      API_PORT: 8080
      CONFIG_FILE: /app/config.yml
```

## Docker Setup

The container runs both services via an entrypoint script:

```yaml
services:
  marina:
    image: ghcr.io/polarfoxdev/marina:latest
    ports:
      - "8080:8080"  # Web interface and API
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./config.yml:/app/config.yml:ro
      - marina-data:/var/lib/marina  # For marina.db
    environment:
      API_PORT: 8080
      CONFIG_FILE: /app/config.yml
```

Both the backup manager and API server start automatically and shut down gracefully together.

## Access the Dashboard

Open your browser to:

```text
http://localhost:8080
```

If authentication is configured (via `authPassword` in config.yml), you'll be prompted to log in.

## Testing the API

```bash
# Check health
curl http://localhost:8080/api/health

# Get all schedules (includes mesh peers if configured)
curl http://localhost:8080/api/schedules | jq

# Get job statuses for a specific instance
curl http://localhost:8080/api/status/local-backup | jq

# Get logs for a specific job
curl http://localhost:8080/api/logs/job/1 | jq

# Login (if auth is enabled)
curl -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"password":"your-password"}'
```

## Architecture

```text
┌─────────────────────────────────────┐
│     Marina Container                │
│                                     │
│  ┌──────────────┐  ┌─────────────┐  │
│  │   Manager    │  │  API Server │  │
│  │   (marina)   │  │ (marina-api)│  │
│  │              │  │             │  │
│  │ Backup Jobs  │  │  Port 8080  │  │
│  │ Discovery    │  │             │  │
│  │ Scheduling   │  │  REST API   │  │
│  └──────┬───────┘  │  + Web UI   │  │
│         │          └──────┬──────┘  │
│         └────┬────────────┘         │
│              ▼                      │
│     ┌────────────────┐              │
│     │   marina.db    │              │
│     │  (unified DB)  │              │
│     └────────────────┘              │
│                                     │
└─────────────────────────────────────┘
            │
            ▼
      [React App]
    (Browser Client)
       
    ┌────────────────┐
    │  Mesh Peers    │
    │  (Optional)    │
    │  Node 2, 3...  │
    └────────────────┘
```

## CORS Configuration

The API server includes CORS middleware configured for local development:

- Allows origins: `http://localhost:*`, `http://127.0.0.1:*`
- Methods: GET, POST, PUT, DELETE, OPTIONS
- Credentials: Enabled

For production, customize CORS settings in `cmd/api/main.go`.
