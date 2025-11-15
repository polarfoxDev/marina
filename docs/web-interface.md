# Marina Web Interface

The Marina API server is now integrated into the main Docker container and automatically runs alongside the backup manager. It provides a REST API for querying backup status and is ready to serve a React frontend.

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
- `GET /api/status/{instanceID}` - Statuses for specific instance

#### Logs

- `GET /api/logs/job/{id}` - Logs for a specific job

#### Frontend (Coming Soon)

- `GET /` - React app (serves from `/app/web`)

## Docker Setup

The container now runs both services via an entrypoint script:

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

Both the backup manager and API server start automatically and shut down gracefully together.

## Testing the API

```bash
# Check health
curl http://localhost:8080/api/health

# Get job statuses for a specific instance
curl http://localhost:8080/api/status/hetzner-s3 | jq

# Get logs for a specific job (replace 1 with actual job ID)
curl http://localhost:8080/api/logs/job/1 | jq
```

## Adding a React Frontend

The API is ready to serve a React app. To add your frontend:

1. **Build your React app** and place the build output in the container at `/app/web/`

2. **Update Dockerfile** to copy your build:
  
   ```dockerfile
   # Add before the final ENTRYPOINT
   COPY --from=frontend-build /app/build /app/web
   ```

3. **The API server will automatically**:
   - Serve static files from `/app/web`
   - Handle SPA routing (all non-API routes serve `index.html`)
   - Apply CORS headers for development

### Example React Setup

Create a `web/` directory in the Marina repo:

```bash
npx create-react-app web
cd web
npm install axios recharts
```

Add proxy to `web/package.json` for development:

```json
{
  "proxy": "http://localhost:8080"
}
```

Build and add to Docker:

```dockerfile
# In Dockerfile, add a frontend build stage
FROM node:20-alpine AS frontend
WORKDIR /app
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Then in the runner stage, copy the build
COPY --from=frontend /app/build /app/web
```

### API Client Example

```typescript
// api/marina.ts
import axios from 'axios';

const api = axios.create({
  baseURL: '/api'
});

export const getInstanceStatus = (instanceId: string) =>
  api.get(`/status/${instanceId}`).then(res => res.data);

export const getJobLogs = (jobId: number) =>
  api.get(`/logs/job/${jobId}`).then(res => res.data);
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
│  └──────┬───────┘  └──────┬──────┘  │
│         │                 │         │
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
```

## CORS Configuration

The API server includes CORS middleware configured for local development:

- Allows origins: `http://localhost:*`, `http://127.0.0.1:*`
- Methods: GET, POST, PUT, DELETE, OPTIONS
- Credentials: Enabled

For production, customize CORS settings in `cmd/api/main.go`.

## Future Enhancements

Planned features for the web interface:

- Real-time backup progress via WebSockets
- Backup history and snapshot browser
- Manual backup triggers
- Configuration editor
- Alert management
- Restore operations UI
