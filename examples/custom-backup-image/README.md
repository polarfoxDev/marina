# Marina Example Custom Backup Image

This is an example Docker image that demonstrates how to create a custom backup backend for Marina.

## How It Works

When Marina uses a custom image backend:

1. Marina stages all backup data in the `/backup` directory (which is mounted from Marina's staging volume)
2. Marina starts a container with your custom image
3. The container's `/backup.sh` script is executed
4. Marina captures stdout/stderr from the container for logs
5. The container's exit code determines success (0) or failure (non-zero)

## Example Script Behavior

This example script:
- Waits for 10 seconds to simulate backup work
- Lists the contents of the `/backup` directory
- **Randomly fails 25% of the time** to demonstrate error handling
- Succeeds 75% of the time

The random failures help demonstrate how Marina handles failed backups in the dashboard and logs.

## Environment Variables

Marina automatically provides these environment variables to custom containers:

- `MARINA_INSTANCE_ID` - The instance ID from config.yml
- `MARINA_HOSTNAME` - The hostname of the Marina node
- Any custom environment variables defined in the `env` section of your instance configuration

## Building the Image

```bash
cd examples/custom-backup-image
docker build -t marina/example-backup:latest .
```

## Using in Config

Add this to your `config.yml`:

```yaml
instances:
  - id: custom-backup
    customImage: marina/example-backup:latest
    schedule: "0 4 * * *" # Daily at 4 AM
    env:
      # Add any custom environment variables your backup script needs
      BACKUP_ENDPOINT: https://backup.example.com
      BACKUP_TOKEN: your-token-here
```

## Creating Your Own Custom Backup Image

To create your own custom backup image:

1. Create a Dockerfile that includes your backup tools
2. Add a `/backup.sh` script that:
   - Reads data from `/backup` directory
   - Performs your backup operation (e.g., upload to cloud, rsync, etc.)
   - Prints logs to stdout/stderr
   - Returns exit code 0 on success, non-zero on failure
3. Build and push your image to a registry
4. Configure Marina to use your image via `customImage` in config.yml

### Requirements

Your custom image must:
- Have a `/backup.sh` script (or configure a different entrypoint)
- Read backup data from `/backup` directory
- Exit with code 0 on success, non-zero on failure
- Output logs to stdout/stderr (captured by Marina)

### Optional Features

Your custom image can optionally:
- Implement its own retention policy (Marina won't enforce retention for custom images)
- Use environment variables from the `env` section in config.yml
- Access metadata via `MARINA_INSTANCE_ID` and `MARINA_HOSTNAME` environment variables
- Perform incremental backups, deduplication, encryption, etc.

## Real-World Examples

Custom images are useful for:
- **Cloud-native backups**: Upload directly to S3, GCS, Azure Blob, etc. with native SDKs
- **Specialized backup tools**: Use tools like Borg, Duplicati, or proprietary backup software
- **Custom workflows**: Implement pre/post-processing, notifications, or integrations
- **Air-gapped environments**: Use custom protocols or transport methods
