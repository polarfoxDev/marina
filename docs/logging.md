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
- Job ID (for backup job logs)
- Instance ID (backup destination)

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

# Filter by job ID
./logquery -job "volume:my-data"

# Filter by instance
./logquery -instance "hetzner-s3"

# Filter by log level
./logquery -level ERROR

# Filter by time range (RFC3339 format)
./logquery -since "2024-01-01T00:00:00Z" -until "2024-01-02T00:00:00Z"

# Combine filters
./logquery -job "volume:my-data" -level ERROR -limit 50

# Prune old logs (older than 30 days)
./logquery -prune "720h"
```

## Log Levels

- **DEBUG** - Detailed diagnostic information (e.g., dump command output)
- **INFO** - General informational messages (job started, completed, container stopped)
- **WARN** - Warning messages that don't stop execution (copy size mismatch, unknown instance)
- **ERROR** - Error messages (job failed, discovery failed)

## Job Context

Backup job logs automatically include:

- **Job ID** - Unique identifier (e.g., `volume:my-data`, `container:abc123`)
- **Instance ID** - Backup destination (e.g., `hetzner-s3`, `local-backup`)
- **Duration** - Time taken for the job to complete

Example console output:

```log
2024-01-15 10:30:00 [job:volume:my-data] [instance:hetzner-s3] INFO: backup job started
2024-01-15 10:30:15 [job:volume:my-data] [instance:hetzner-s3] INFO: backup job completed successfully (duration: 15.2s)
```

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
