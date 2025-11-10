# Quick Start with API

This guide shows how to run Marina with the integrated API server.

## Quick Test

```bash
# Build and start Marina with API
docker-compose -f docker-compose.dev.yml up --build

# In another terminal, test the API
curl http://localhost:8080/api/health
curl http://localhost:8080/api/stats/summary | jq
curl http://localhost:8080/api/status | jq
```

The API is now accessible at `http://localhost:8080`.

## What's Running

The Marina container now runs TWO services:
1. **Backup Manager** (`marina`) - Discovers and schedules backups
2. **API Server** (`marina-api`) - Serves REST API on port 8080

Both services share:
- `/var/lib/marina/status.db` - Job status database
- `/var/lib/marina/logs.db` - Structured logs database

## API Endpoints

Visit these URLs in your browser:
- http://localhost:8080/ - Welcome page with API links
- http://localhost:8080/api/health - Health check
- http://localhost:8080/api/status - All backup job statuses
- http://localhost:8080/api/stats/summary - Summary statistics

## View the Status Database

```bash
# Install jq if needed: brew install jq

# Get summary of all backups
curl -s http://localhost:8080/api/stats/summary | jq

# Get status for specific instance
curl -s http://localhost:8080/api/status/instance/local-backup | jq

# Monitor for errors
watch -n 5 'curl -s http://localhost:8080/api/status | jq "[.[] | select(.status == \"error\")]"'
```

## Next Steps

1. **Add your React app**: Place build output in `/app/web` directory
2. **Customize CORS**: Edit `cmd/api/main.go` for production domains
3. **Monitor backups**: Build dashboard using the REST API
4. **Set up alerts**: Query error states and send notifications

## Development

To develop the frontend:

```bash
# Create React app
cd web
npx create-react-app .

# Add proxy to package.json
{
  "proxy": "http://localhost:8080"
}

# Start dev server
npm start

# Build for production
npm run build
```

Then add to Dockerfile:
```dockerfile
COPY web/build /app/web
```
