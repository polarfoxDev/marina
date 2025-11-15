# Marina Logging

Marina logs to both console (stdout) and a SQLite database for persistence.

## Configuration

```bash
DB_PATH=/var/lib/marina/marina.db  # Default location
```

The database contains both logs and job status in a unified schema.

## Log Format

Console logs include hierarchical context:

```text
2025-11-10 14:23:45 INFO: marina starting...
2025-11-10 14:23:46 [hetzner-s3] INFO: instance backup started (3 targets)
2025-11-10 14:23:47 [hetzner-s3/volume:mydata] INFO: preparing volume: mydata
```

## Log Levels

- **INFO** - Normal operations
- **WARN** - Non-fatal issues (backup continues)
- **ERROR** - Fatal errors (operation fails)
- **DEBUG** - Detailed diagnostic information

## Querying

Logs are queryable via the API server. See API documentation for endpoints.

## Notes

- WAL mode enabled for better concurrency
- DB write failures don't stop console logging
- Logs include instance_id and target_id for filtering
