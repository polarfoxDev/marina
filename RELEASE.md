# Release Process

Marina uses semantic versioning (MAJOR.MINOR.PATCH).

## Creating a Release

1. Update the `VERSION` file with the new version number (e.g., `0.1.0`)

2. Commit the version change:

   ```bash
   git add VERSION
   git commit -m "Bump version to 0.1.0"
   ```

3. Create and push a git tag:

   ```bash
   git tag v0.1.0
   git push origin v0.1.0
   ```

4. The GitHub Action will automatically:
   - Build Docker images for linux/amd64 and linux/arm64
   - Tag images with the version number (e.g., `ghcr.io/polarfoxdev/marina:0.1.0`)
   - Tag images as `latest`
   - Push to GitHub Container Registry

## Manual Build

To manually trigger a build without creating a tag:

1. Go to Actions → "Build and Push Docker Image"
2. Click "Run workflow"
3. Enter the desired version number
4. Click "Run workflow"

## Using the Docker Image

Pull the latest version:

```bash
docker pull ghcr.io/polarfoxdev/marina:latest
```

Pull a specific version:

```bash
docker pull ghcr.io/polarfoxdev/marina:0.1.0
```

## Checking Version

Check the version of a running binary:

```bash
marina --version
marina-api --version
```

Or in Docker:

```bash
docker run ghcr.io/polarfoxdev/marina:latest marina --version
```
