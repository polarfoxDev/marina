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
export LOG_DB_PATH=/var/lib/marina/logs.db
./marina
```

Default: `/var/lib/marina/logs.db`

## Database Schema

```sql
CREATE TABLE logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME NOT NULL,
    level TEXT NOT NULL,
    message TEXT NOT NULL,
    job_id TEXT,        -- NULL for system logs
    instance_id TEXT    -- NULL for system logs
);

-- Indexes for fast querying
CREATE INDEX idx_logs_timestamp ON logs(timestamp);
CREATE INDEX idx_logs_job_id ON logs(job_id);
CREATE INDEX idx_logs_instance_id ON logs(instance_id);
CREATE INDEX idx_logs_level ON logs(level);
```

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

### Using Go Code

```go
import "github.com/polarfoxDev/marina/internal/logging"

// Open logger
logger, err := logging.New("/var/lib/marina/logs.db", os.Stdout)
if err != nil {
    log.Fatal(err)
}
defer logger.Close()

// Query logs for a specific job
entries, err := logger.Query(logging.QueryOptions{
    JobID: "volume:my-data",
    Level: logging.LevelError,
    Limit: 50,
})

// Query logs by time range
since := time.Now().Add(-24 * time.Hour)
entries, err = logger.Query(logging.QueryOptions{
    Since: since,
    InstanceID: "hetzner-s3",
})

// Prune old logs
deleted, err := logger.PruneOldLogs(30 * 24 * time.Hour) // 30 days
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
```
2024-01-15 10:30:00 [job:volume:my-data] [instance:hetzner-s3] INFO: backup job started
2024-01-15 10:30:15 [job:volume:my-data] [instance:hetzner-s3] INFO: backup job completed successfully (duration: 15.2s)
```

## Log Rotation

Use the `PruneOldLogs()` method or `logquery -prune` command to remove old logs:

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

## Future GUI Integration

The SQLite-based logging system is designed for easy integration with a web GUI:

```javascript
// Example REST API endpoint
GET /api/logs?job=volume:my-data&limit=100

// Example response
{
  "logs": [
    {
      "id": 123,
      "timestamp": "2024-01-15T10:30:00Z",
      "level": "INFO",
      "message": "backup job started",
      "job_id": "volume:my-data",
      "instance_id": "hetzner-s3"
    }
  ]
}
```

The structured nature of the logs enables:
- Real-time log streaming for active jobs
- Job history and status tracking
- Error rate monitoring per instance
- Performance metrics (job duration trends)
- Full-text search across all logs

## Performance Considerations

- **WAL mode** - Enabled for better concurrent read performance
- **Indexes** - All filterable columns are indexed
- **Connection pooling** - Single shared connection per Logger instance
- **Console + DB** - Both outputs are written synchronously; DB failures don't stop logging to console

## Migration from Old Logging

The new Logger is backward compatible with the old `Logf func(string, ...any)` interface via the `Logf()` method, making migration seamless. The only change required in existing code is passing a `*logging.Logger` instead of a function.
