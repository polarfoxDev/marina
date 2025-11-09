# Dynamic Discovery

Marina supports dynamic discovery of backup targets, allowing containers and volumes to be created, destroyed, or have their labels changed while Marina is running.

## How It Works

Marina uses two complementary mechanisms to detect changes:

### 1. Periodic Polling (Default: 30 seconds)

Marina periodically scans Docker for containers and volumes with backup labels. This ensures all changes are eventually detected, even if events are missed.

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

When Marina detects a new container or volume with backup labels, or when labels change:

- New targets are automatically scheduled
- Changed schedules trigger rescheduling
- Configuration changes (retention, hooks, etc.) are applied on next backup

### Removing Targets

When a container is destroyed or a volume is removed:

- The scheduled backup job is automatically removed
- No cleanup of existing backups (use Restic manually if needed)

### Container Recreation

When using `docker-compose down && docker-compose up`:

1. Event listener detects container destruction → removes old schedule
2. Event listener detects container creation → adds new schedule
3. Backup jobs continue with the new container ID

## Testing Dynamic Discovery

```bash
# Start Marina
docker-compose up -d marina

# Add a volume with backup labels
docker volume create \
  --label eu.polarnight.marina.enabled=true \
  --label eu.polarnight.marina.schedule="*/5 * * * *" \
  --label eu.polarnight.marina.instanceID=local-backup \
  test-volume

# Watch Marina logs - should see "scheduled volume:test-volume"
docker-compose logs -f marina

# Update the schedule
docker volume rm test-volume
docker volume create \
  --label eu.polarnight.marina.enabled=true \
  --label eu.polarnight.marina.schedule="*/10 * * * *" \
  --label eu.polarnight.marina.instanceID=local-backup \
  test-volume

# Marina will detect and reschedule automatically
```

## Performance Considerations

- **Event listener overhead**: Minimal - only processes relevant events
- **Polling overhead**: Depends on number of containers/volumes
  - < 100 containers/volumes: negligible
  - > 500 containers/volumes: consider increasing `DISCOVERY_INTERVAL`
- **Debouncing**: 2-second delay prevents rapid rediscovery during orchestration changes

## Troubleshooting

### Targets not updating

1. Check Marina logs for discovery errors
2. Verify labels are correct: `docker volume inspect <name>` or `docker inspect <container>`
3. Ensure `ENABLE_EVENTS=true` (default)
4. Manually trigger discovery by restarting Marina

### Too frequent rediscovery

Increase the polling interval:

```bash
DISCOVERY_INTERVAL=5m # Poll every 5 minutes
```

Or disable event listener (not recommended):

```bash
ENABLE_EVENTS=false
```
