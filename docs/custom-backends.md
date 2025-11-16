# Custom Backup Backends

Marina supports custom Docker images as an alternative to the built-in Restic backend. This allows you to use any backup tool or workflow by packaging it as a Docker image.

## Quick Start

1. **Configure in config.yml:**

```yaml
instances:
  - id: my-custom-backup
    customImage: myorg/custom-backup:latest
    schedule: "0 4 * * *"
    env:
      BACKUP_TOKEN: secret-token
      BACKUP_ENDPOINT: https://backup.example.com
```

1. **Create your Docker image** with a `/backup.sh` script:

```dockerfile
FROM alpine:3.20
COPY backup.sh /backup.sh
RUN chmod +x /backup.sh
ENTRYPOINT ["/backup.sh"]
```

1. **Label your volumes/containers** to use this instance:

```yaml
volumes:
  my-data:
    labels:
      dev.polarfox.marina.enabled: "true"
      dev.polarfox.marina.instanceID: "my-custom-backup"
```

## How It Works

### Backup Flow

1. Marina discovers targets (volumes/databases) via Docker labels
1. Marina stages backup data in `/backup/{instanceID}` directory on the host
1. Marina creates a container using your custom image
1. Only `/backup/{instanceID}` is mounted at `/backup` in the container (scoped to this instance)
1. Your container's `/backup.sh` script executes
1. Marina captures stdout/stderr for logs
1. Container exit code determines success (0) or failure (non-zero)
1. Container is automatically removed after completion

### Custom Image Contract

Your custom Docker image must:

- **Have a `/backup.sh` script** (or specify a different entrypoint)
- **Read data from `/backup` directory** - Marina mounts only this instance's subfolder here (`/backup/{instanceID}` on host → `/backup` in container)
- **Exit with code 0** on success, non-zero on failure
- **Write logs to stdout/stderr** - Marina captures these for the dashboard

### Directory Structure

Marina internally stages data with this structure:

```text
/backup/
  └── {instance-id}/
      └── {timestamp}/
          ├── vol/
          │   └── {volume-name}/
          │       └── {paths}...
          └── dbs/
              └── {db-name}/
                  └── dump.sql (or .archive for MongoDB)
```

Inside your custom container, access the data under `/backup/{timestamp}/vol/` and `/backup/{timestamp}/dbs/`, because `{instance-id}` is mounted at `/backup`.

### Environment Variables

Marina automatically provides:

- `MARINA_INSTANCE_ID` - The instance ID from config.yml
- `MARINA_HOSTNAME` - The hostname of the Marina node
- Any custom environment variables from the `env` section in config.yml

### Example Backup Script

```bash
#!/bin/sh
set -e

echo "Starting backup for instance: ${MARINA_INSTANCE_ID}"
echo "Node: ${MARINA_HOSTNAME}"

# Check if backup directory exists
if [ ! -d "/backup" ]; then
    echo "ERROR: /backup directory not found"
    exit 1
fi

# Find the latest backup data
BACKUP_DIR=$(find /backup -type d -maxdepth 2 -mindepth 2 | sort -r | head -1)

if [ -z "$BACKUP_DIR" ]; then
    echo "ERROR: No backup data found"
    exit 1
fi

echo "Backing up: $BACKUP_DIR"

# Example: Upload to S3
aws s3 sync "$BACKUP_DIR" "s3://my-bucket/backups/$(basename $BACKUP_DIR)/" \
    --storage-class GLACIER \
    --endpoint-url "$BACKUP_ENDPOINT"

echo "Backup completed successfully"
exit 0
```

## Common Use Cases

### Cloud-Native Backups

Upload directly to cloud storage with native SDKs:

```dockerfile
FROM python:3.11-alpine
RUN pip install boto3 google-cloud-storage azure-storage-blob
COPY backup.sh /backup.sh
RUN chmod +x /backup.sh
ENTRYPOINT ["/backup.sh"]
```

### Specialized Backup Tools

Use tools like Borg, Duplicati, or custom backup software:

```dockerfile
FROM alpine:3.20
RUN apk add --no-cache borgbackup
COPY backup.sh /backup.sh
RUN chmod +x /backup.sh
ENTRYPOINT ["/backup.sh"]
```

### Custom Workflows

Implement pre-processing, compression, encryption, or custom logic:

```bash
#!/bin/sh
set -e

# Compress and encrypt
tar -czf - /backup | openssl enc -aes-256-cbc -salt -k "$ENCRYPTION_KEY" > backup.tar.gz.enc

# Upload with retries
for i in 1 2 3; do
    if curl -F "file=@backup.tar.gz.enc" "$UPLOAD_URL"; then
        exit 0
    fi
    sleep 30
done

exit 1
```

## Retention Policy

Unlike Restic, Marina does **not** enforce retention policies for custom images. Your custom image is responsible for implementing its own retention logic if needed.

The `retention` field in config.yml is preserved for documentation purposes but has no effect on custom image backups.

## Debugging

### View Logs

Logs from your custom backup container are captured and displayed in the Marina dashboard and available via the API:

```bash
# View instance logs
curl http://marina:8080/api/logs/job?instanceId=my-custom-backup

# View system logs
curl http://marina:8080/api/logs/system
```

### Test Locally

Test your custom image independently of Marina:

```bash
# Create test data
mkdir -p /tmp/test-backup/vol/test-vol
echo "test data" > /tmp/test-backup/vol/test-vol/file.txt

# Run your image
docker run --rm \
  -v /tmp/test-backup:/backup \
  -e MARINA_INSTANCE_ID=test \
  -e MARINA_HOSTNAME=localhost \
  myorg/custom-backup:latest
```

### Common Issues

**Container exits immediately:**

- Check that `/backup.sh` has execute permissions
- Verify the script has a proper shebang (`#!/bin/sh`)
- Check for syntax errors in your script

**"No backup data found":**

- Verify volumes/databases have correct labels
- Check that `instanceID` matches your config
- Ensure Marina can access Docker socket

**Permissions errors:**

- Custom containers run as root by default
- Staged files are owned by root
- Ensure your backup script handles permissions correctly

## Advanced Topics

### Custom Entrypoint

If you don't want to use `/backup.sh`, specify a custom entrypoint:

```dockerfile
FROM mybase:latest
COPY my-backup-tool /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/my-backup-tool"]
```

### Multi-Stage Processing

Your script can perform multiple operations:

```bash
#!/bin/sh
set -e

# 1. Validate data
echo "Validating backup data..."
find /backup -type f -exec md5sum {} \; > checksums.txt

# 2. Compress
echo "Compressing..."
tar -czf backup.tar.gz /backup checksums.txt

# 3. Encrypt
echo "Encrypting..."
gpg --encrypt --recipient "$GPG_KEY_ID" backup.tar.gz

# 4. Upload
echo "Uploading..."
curl -F "file=@backup.tar.gz.gpg" "$BACKUP_URL"

# 5. Verify
echo "Verifying..."
curl "$BACKUP_URL/verify?file=backup.tar.gz.gpg"

echo "Backup completed with verification"
```

### Health Checks

Implement health checks in your backup script:

```bash
#!/bin/sh
set -e

# Check prerequisites
command -v aws >/dev/null 2>&1 || { echo "aws-cli not installed"; exit 1; }
[ -n "$AWS_ACCESS_KEY_ID" ] || { echo "AWS credentials not set"; exit 1; }

# Check backup destination
aws s3 ls "s3://my-bucket/" >/dev/null || { echo "Cannot access S3 bucket"; exit 1; }

# Proceed with backup
# ...
```

## Example Images

Marina includes an example custom backup image in `examples/custom-backup-image/` that demonstrates:

- Basic container structure
- Environment variable usage
- Log output
- Error handling
- Random failures for testing

Build and test it:

```bash
cd examples/custom-backup-image
docker build -t marina/example-backup:latest .
docker run --rm -v /tmp/test:/backup marina/example-backup:latest
```

## Migration from Restic

If you're migrating from Restic to a custom image:

1. Keep your existing Restic instance for historical backups
2. Add a new custom image instance
3. Gradually migrate volumes to the new instance
4. Test thoroughly before removing Restic instance

Example parallel configuration:

```yaml
instances:
  - id: restic-legacy
    repository: s3:https://storage.example.com/restic
    schedule: "0 2 * * *"
    env:
      RESTIC_PASSWORD: ${RESTIC_PASS}
  
  - id: custom-new
    customImage: myorg/backup:latest
    schedule: "0 3 * * *"
    env:
      BACKUP_TOKEN: ${TOKEN}
```

## Best Practices

1. **Idempotency**: Make your backup script idempotent - safe to run multiple times
2. **Logging**: Include detailed logs with timestamps for debugging
3. **Error Handling**: Exit with non-zero code on any error
4. **Validation**: Validate backup data before uploading
5. **Cleanup**: Don't leave temporary files in `/backup`
6. **Versioning**: Tag your images with versions, not just `latest`
7. **Testing**: Test your image thoroughly before production use
8. **Monitoring**: Monitor backup success/failure rates in Marina dashboard

## See Also

- [Configuration Reference](../README.md#configuration)
- [Docker Labels Reference](../docs/labels.md)
- [Example Custom Image](../examples/custom-backup-image/)
- [Marina API Documentation](../docs/api.md)
