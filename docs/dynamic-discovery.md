# Dynamic Discovery

Marina supports dynamic discovery of backup targets, allowing containers and volumes to be created or destroyed while Marina is running. Configured targets are verified and scheduled automatically.

## How It Works

Marina uses two complementary mechanisms to detect changes:

### 1. Periodic Polling (Default: 30 seconds)

Marina periodically verifies that configured containers and volumes exist. This ensures all changes are eventually detected, even if events are missed.

**Configure via environment variable:**

```bash
DISCOVERY_INTERVAL=1m # Set to 1 minute, 30s, 5m, etc.
```

### 2. Docker Event Listener (Enabled by default)

Marina listens to Docker events in real-time for immediate detection of:

- Container lifecycle: create, destroy, start, stop
- Volume lifecycle: create, destroy, mount, unmount

Events are debounced (2 second delay) to avoid excessive rediscovery during rapid changes like `docker-compose down && docker-compose up`.

**Disable via environment variable:**

```bash
ENABLE_EVENTS=false # Rely only on periodic polling
```

## Behavior

### Adding/Updating Targets

When Marina detects a container or volume that matches a configured target:

- Targets are automatically scheduled according to instance configuration
- Changes to config.yml require Marina restart to take effect
- Container/volume name changes require updating config.yml

### Removing Targets

When a configured container is destroyed or volume is removed:

- The scheduled backup job is automatically removed
- Job is rescheduled when the container/volume is recreated
- No cleanup of existing backups (use Restic manually if needed)

### Container Recreation

When using `docker-compose down && docker-compose up`:

1. Event listener detects container destruction → removes old schedule
2. Event listener detects container creation → adds new schedule
3. Backup jobs continue with the new container ID

## Testing Dynamic Discovery

```bash
# Start Marina with configured targets in config.yml
docker-compose up -d marina

# Create a volume that matches a configured target
docker volume create app-data

# Watch Marina logs - should see "scheduled volume:app-data"
docker-compose logs -f marina

# Remove and recreate the volume
docker volume rm app-data
docker volume create app-data

# Marina will detect and reschedule automatically
```

## Performance Considerations

- **Event listener overhead**: Minimal - only processes relevant events
- **Polling overhead**: Depends on number of containers/volumes
  - < 100 containers/volumes: negligible
  - > 500 containers/volumes: consider increasing `DISCOVERY_INTERVAL`
- **Debouncing**: 2-second delay prevents rapid rediscovery during orchestration changes

## Troubleshooting

### Targets not being scheduled

1. Check Marina logs for discovery errors
2. Verify container/volume names match those in config.yml exactly
3. Use `docker volume ls` and `docker ps` to check names
4. Ensure `ENABLE_EVENTS=true` (default)
5. Manually trigger discovery by restarting Marina

### Too frequent rediscovery

Increase the polling interval:

```bash
DISCOVERY_INTERVAL=5m # Poll every 5 minutes
```

Or disable event listener (not recommended):

```bash
ENABLE_EVENTS=false
```
