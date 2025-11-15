# Marina Mesh Mode

Mesh mode allows you to connect multiple Marina instances together and view all backup schedules from a unified dashboard.

## Overview

In mesh mode, one Marina instance can fetch backup schedule data from other Marina API endpoints and display them together in the web interface. This is useful when you have:

- Multiple servers each running Marina
- A centralized monitoring location where you want to see all backups
- Geographically distributed backup infrastructure

**Key Features:**
- View backup schedules from all connected nodes in one dashboard
- Automatic node grouping in the UI
- Resilient: failed peer connections don't break the dashboard
- Configuration-based: simple YAML setup
- No data replication: each node keeps its own data

## Configuration

### Basic Setup

Add a `mesh` section to your `config.yml`:

```yaml
instances:
  - id: local-backup
    repository: /mnt/backup/restic
    schedule: "0 2 * * *"
    env:
      RESTIC_PASSWORD: your-password

# Mesh configuration
mesh:
  # Optional: custom node name (defaults to hostname)
  nodeName: production-server-1
  
  # List of peer Marina API URLs
  peers:
    - http://marina-node2:8080
    - http://marina-node3:8080
```

### Using Environment Variables

You can use environment variables in the mesh configuration:

```yaml
mesh:
  nodeName: ${NODE_NAME}
  peers:
    - ${PEER_NODE_1}
    - ${PEER_NODE_2}
```

Then set the environment variables:

```bash
export NODE_NAME=production-server-1
export PEER_NODE_1=http://marina-node2:8080
export PEER_NODE_2=http://marina-node3:8080
```

## Architecture

### How It Works

1. **Each node** runs both:
   - Marina manager (backup orchestration)
   - Marina API (HTTP server on port 8080)

2. **When you access any dashboard**, the API server:
   - Fetches local schedules from its database
   - Concurrently fetches schedules from all configured peer URLs
   - Merges and returns the combined data

3. **The frontend** groups schedules by node name for easy organization

### Data Flow

```
┌─────────────────┐
│   Your Browser  │
└────────┬────────┘
         │ GET /api/schedules
         ▼
┌─────────────────┐      ┌──────────────┐
│  Marina Node 1  │─────▶│ Local DB     │
│  (API Server)   │      └──────────────┘
└────────┬────────┘
         │ HTTP GET
         ├─────────────────┐
         ▼                 ▼
┌─────────────┐   ┌─────────────┐
│ Node 2 API  │   │ Node 3 API  │
└─────────────┘   └─────────────┘
```

## Network Requirements

### Peer Connectivity

Each Marina node must be able to reach its configured peers via HTTP/HTTPS:

- Port 8080 (default API port) must be accessible between nodes
- Use internal hostnames/IPs or external URLs as needed
- Supports both HTTP and HTTPS (configure appropriately)

### Docker Compose Example

```yaml
version: '3.8'

services:
  marina:
    image: your-marina-image
    environment:
      - NODE_NAME=server-1
      - PEER_NODE_1=http://server-2:8080
      - PEER_NODE_2=http://server-3:8080
    ports:
      - "8080:8080"
    volumes:
      - ./config.yml:/app/config.yml
      - /var/run/docker.sock:/var/run/docker.sock
```

## Security Considerations

### Network Security

- **Internal Network**: Best to keep Marina API on internal network only
- **Firewall Rules**: Restrict API port access to trusted nodes
- **Reverse Proxy**: Use Nginx/Traefik with authentication if exposing externally

### Authentication

Currently, mesh mode does not include built-in authentication. If you need security:

1. Use a reverse proxy with authentication (e.g., Nginx with basic auth)
2. Keep mesh communication on a private network (VPN, internal VLAN)
3. Use HTTPS with client certificates for mutual TLS

## UI Behavior

### Single Node Mode

When no peers are configured, the dashboard shows only local schedules:

```
Backup Schedules
Overview of all backup instances

[Schedule Card 1] [Schedule Card 2] [Schedule Card 3]
```

### Mesh Mode

With peers configured, schedules are grouped by node:

```
Backup Schedules
Overview of all backup instances
Mesh mode: viewing 3 node(s)

production-server-1
2 backup schedule(s)
[Schedule Card 1] [Schedule Card 2]

production-server-2
1 backup schedule(s)
[Schedule Card 3]

production-server-3
2 backup schedule(s)
[Schedule Card 4] [Schedule Card 5]
```

## Limitations

### Current Limitations

1. **Job Details**: Job statuses and logs are only available locally (not fetched from peers)
2. **Authentication**: No built-in auth between nodes
3. **One-way**: Each node independently fetches from peers (not bidirectional sync)
4. **Timeouts**: Slow or unavailable peers may delay dashboard loading (5s timeout per peer)

### What's NOT Replicated

- Job execution logs (remain local to each node)
- Backup repository data (each node manages its own backups)
- Configuration (each node has its own config.yml)
- Database state (each node has its own SQLite database)

## Troubleshooting

### Peer Connection Failures

If a peer is unreachable, Marina will:
- Log a warning message
- Continue showing data from available nodes
- Skip the unavailable peer (doesn't break the UI)

Check logs:
```bash
docker logs marina
# Look for: "Warning: failed to fetch schedules from peer"
```

### Node Name Not Showing

If node names aren't displaying correctly:

1. Check if `mesh.nodeName` is set in config.yml
2. Or set `NODE_NAME` environment variable
3. Fallback is hostname (may not be descriptive)

### Empty Schedules from Peer

If a peer shows 0 schedules:
- Verify the peer API is accessible: `curl http://peer:8080/api/health`
- Check peer's local schedules: `curl http://peer:8080/api/schedules/`
- Ensure peer has backup instances configured

## Performance

### Network Impact

- Each dashboard page load triggers peer API calls
- Calls are concurrent (parallel, not sequential)
- 5-second timeout per peer prevents long delays
- Dashboard auto-refreshes every 30 seconds

### Scaling

- Tested with up to 10 nodes in mesh
- Each node can have different peer lists (flexible topology)
- Consider caching for large deployments (not currently implemented)

## Advanced Configuration

### Different Peers Per Node

Each node can have its own peer list. Example for hub-and-spoke:

**Hub (server-1)**:
```yaml
mesh:
  nodeName: hub-server
  peers:
    - http://spoke-1:8080
    - http://spoke-2:8080
    - http://spoke-3:8080
```

**Spoke (server-2)**:
```yaml
mesh:
  nodeName: spoke-1
  peers:
    - http://hub-server:8080  # Only connect to hub
```

### Conditional Mesh

Use environment variables to enable mesh only when needed:

```yaml
mesh:
  nodeName: ${NODE_NAME}
  peers:
    - ${PEER_1:-}  # Empty if not set
    - ${PEER_2:-}
```

## Future Enhancements

Planned improvements (not yet implemented):

- [ ] Bidirectional mesh synchronization
- [ ] Built-in authentication between nodes
- [ ] Remote job log viewing
- [ ] Health status aggregation
- [ ] Node discovery via service registry
- [ ] TLS certificate validation
- [ ] Response caching for performance

## See Also

- [QUICKSTART-API.md](./QUICKSTART-API.md) - API server setup
- [config.example.yml](./config.example.yml) - Configuration examples
