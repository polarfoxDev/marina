# Marina Logging System

Marina uses a structured logging system based on SQLite for persistent, queryable logs that can be easily integrated with a future web GUI.

## Overview

All logs are written to both:

1. **Console (stdout)** - for real-time monitoring
2. **SQLite database** - for persistence and querying

Each log entry includes:

- Timestamp
- Level (DEBUG, INFO, WARN, ERROR)
- Message
- Instance ID (backup instance like "hetzner-s3")
- Target ID (specific backup target like "volume:mydata" or "container:db123")

## Log Hierarchy

Marina supports hierarchical logging:

1. **System logs** - No instance or target context (general system messages)
2. **Instance logs** - Instance context only (backup instance-level operations)
3. **Target logs** - Both instance and target context (specific volume/DB operations)

### Console Output Format

```
2025-11-10 14:23:45 INFO: marina starting...
2025-11-10 14:23:46 [hetzner-s3] INFO: instance backup started (3 targets)
2025-11-10 14:23:47 [hetzner-s3/volume:mydata] INFO: preparing volume: mydata
2025-11-10 14:23:50 [hetzner-s3/container:db123] INFO: preparing db: postgres
```

## Configuration

The log database location is configurable via the `LOG_DB_PATH` environment variable:

```bash
LOG_DB_PATH=/var/lib/marina/logs.db
```

Default: `/var/lib/marina/logs.db`

## Querying Logs

### Using the logquery CLI Tool

Marina includes a `logquery` utility for querying logs from the command line:

```bash
# Build the tool
go build -o logquery ./cmd/logquery

# Show all recent logs (last 100)
./logquery -db /var/lib/marina/logs.db

# Filter by instance (shows all backups for this instance)
./logquery -instance "hetzner-s3"

# Filter by specific target (shows logs for specific volume/DB)
./logquery -target "volume:mydata"

# Filter by log level
./logquery -level ERROR

# Filter by time range (RFC3339 format)
./logquery -since "2024-01-01T00:00:00Z" -until "2024-01-02T00:00:00Z"

# Combine filters (all backups for this instance that had errors)
./logquery -instance "hetzner-s3" -level ERROR -limit 50

# Prune old logs (older than 30 days)
./logquery -prune "720h"
```

## Log Levels

- **DEBUG** - Detailed diagnostic information (e.g., dump command output)
- **INFO** - General informational messages (job started, completed, container stopped)
- **WARN** - Warning messages that don't stop execution (target preparation failed, copy size mismatch, unknown instance)
- **ERROR** - Error messages that stop the operation (all targets failed, discovery failed, instance not found)

## Usage Examples

### Viewing Instance-Level Logs

View all operations for a specific backup instance:

```bash
./logquery -instance "hetzner-s3"
```

This shows:
- Instance backup started/completed
- All target preparations
- Overall backup operations
- Retention operations

### Viewing Target-Level Logs

View operations for a specific volume or database:

```bash
./logquery -target "volume:mydata"
```

This shows detailed logs for just that target:
- Volume copying
- Container stopping/starting
- Hooks execution

### Debugging Failed Backups

```bash
# Show all errors for an instance
./logquery -instance "hetzner-s3" -level ERROR

# Show warnings to see partial failures (individual targets that failed)
./logquery -instance "hetzner-s3" -level WARN

# Show recent errors across all instances
./logquery -level ERROR -limit 20
```

**Note**: Marina continues backup even if individual targets fail. Check WARN logs to see which targets failed while the instance backup still succeeded with remaining targets.

## Console Output

Backup job logs automatically include hierarchical context:

```text
2025-11-10 10:30:00 [hetzner-s3] INFO: instance backup started (3 targets)
2025-11-10 10:30:01 [hetzner-s3/volume:mydata] INFO: preparing volume: mydata
2025-11-10 10:30:05 [hetzner-s3/volume:mydata] INFO: copying volume mydata to staging
2025-11-10 10:30:10 [hetzner-s3/container:db123] INFO: preparing db: postgres
2025-11-10 10:30:12 [hetzner-s3/container:db123] WARN: failed to prepare db: connection refused
2025-11-10 10:30:12 [hetzner-s3] WARN: backup proceeding with 1/2 targets (1 failed: [db:postgres])
2025-11-10 10:30:15 [hetzner-s3] INFO: backing up 1 paths to instance hetzner-s3
2025-11-10 10:30:45 [hetzner-s3] INFO: instance backup completed (duration: 45s)
```

This example shows a backup where the database failed but the volume succeeded, so the backup continued with just the volume.

## Log Rotation

Use `logquery -prune` command to remove old logs:

```bash
# Prune logs older than 30 days
./logquery -prune "720h"

# Prune logs older than 7 days
./logquery -prune "168h"
```

Consider setting up a cron job for automatic log rotation:

```cron
# Prune logs older than 30 days every day at 3 AM
0 3 * * * /usr/local/bin/logquery -db /var/lib/marina/logs.db -prune "720h"
```

## Performance Considerations

- **WAL mode** - Enabled for better concurrent read performance
- **Indexes** - All filterable columns are indexed
- **Connection pooling** - Single shared connection per Logger instance
- **Console + DB** - Both outputs are written synchronously; DB failures don't stop logging to console
