# Job Status Database

Marina tracks backup job status in a SQLite database for monitoring and API access.

## Configuration

```bash
DB_PATH=/var/lib/marina/marina.db  # Default location
```

The database contains both job status and logs in a unified schema.

## Job Status States

- **scheduled** - Job is queued for execution
- **in_progress** - Currently running
- **success** - Completed successfully
- **partial_success** - Completed with some warnings/failures
- **failed** - Hard error occurred
- **aborted** - Interrupted by restart/shutdown

## API Server

The API server provides HTTP endpoints for querying job status:

```bash
# Default port 8080
API_PORT=8080 marina-api
```

### Basic Endpoints

- `GET /api/status` - All job statuses
- `GET /api/status/instance/{id}` - Jobs for specific instance
- `GET /api/logs` - Query logs with filters

See API code for complete endpoint documentation.

## Database Operations

Marina automatically manages job status:

- **Startup**: Jobs stuck in `in_progress` or `scheduled` are marked as `aborted`
- **Discovery**: New targets create job status entries
- **Execution**: Status updates during backup lifecycle
- **Removal**: Inactive instances are marked as such

## Notes

- WAL mode enabled for concurrent access
- Manager and API can access database simultaneously
- Status updates are best-effort (backups continue on DB failures)
