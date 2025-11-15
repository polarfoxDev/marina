# Marina Web Interface

React-based web interface for monitoring Marina backup status.

## Features

- **Schedules View**: Overview of all backup instances and their schedules
  - **Mesh Mode**: Displays schedules from multiple Marina nodes grouped by server
- **Job Status View**: List of all backup jobs for a specific instance
- **Job Details View**: Detailed view with logs and filtering capabilities

## Mesh Mode

When Marina is configured with mesh networking (multiple instances connected together), the schedules view automatically:
- Groups schedules by node name for easy organization
- Shows a badge indicating how many nodes are in the mesh
- Fetches data from all configured peer nodes

See [docs/MESH.md](../docs/MESH.md) for configuration details.

## Development

```bash
# Install dependencies
pnpm install

# Start dev server (proxies API requests to localhost:8080)
pnpm run dev

# Build for production
pnpm run build
```

## Tech Stack

- **React 18** - UI framework
- **TypeScript** - Type safety
- **Vite** - Build tool and dev server
- **Tailwind CSS 4** - Styling
- **React Router** - Client-side routing

## Project Structure

```
src/
├── components/          # React components
│   ├── SchedulesView.tsx
│   ├── JobStatusesView.tsx
│   └── JobDetailsView.tsx
├── api.ts              # API client
├── types.ts            # TypeScript type definitions
├── utils.ts            # Utility functions
├── App.tsx             # Main app component
├── main.tsx            # Entry point
└── index.css           # Global styles
```

## API Integration

The app connects to the Marina API at `/api/*` endpoints:

- `GET /api/schedules/` - Get all backup schedules
- `GET /api/status/{instanceID}` - Get job statuses for an instance
- `GET /api/logs/job/{id}` - Get logs for a specific job

In development, Vite proxies these requests to `http://localhost:8080`.
In production, the built files are served by the Marina API server from `/app/web`.
