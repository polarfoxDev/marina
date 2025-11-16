# Release Process

Marina uses semantic versioning (MAJOR.MINOR.PATCH) and follows a develop-to-main workflow with automated releases.

## Development Workflow

1. **Work on the `develop` branch** for all new features and fixes
2. **Update CHANGELOG.md** as you make changes:
   - Add entries under the `[Unreleased]` section
   - Use categories: `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`
3. **When ready to release**, merge `develop` to `main` after updating the version

## Creating a Release

### 1. Update Version and Changelog

On the `develop` branch:

1. Update the `VERSION` file with the new version number (e.g., `0.2.0`)

2. Update `CHANGELOG.md`:
   - Change `[Unreleased]` to `[0.2.0] - YYYY-MM-DD`
   - Add a new `[Unreleased]` section at the top
   - Update the comparison links at the bottom

   ```markdown
   ## [Unreleased]

   ## [0.2.0] - 2024-11-16
   ### Added
   - New feature description

   [Unreleased]: https://github.com/polarfoxDev/marina/compare/v0.2.0...HEAD
   [0.2.0]: https://github.com/polarfoxDev/marina/compare/v0.1.2...v0.2.0
   ```

3. Commit the changes:

   ```bash
   git add VERSION CHANGELOG.md
   git commit -m "Bump version to 0.2.0"
   git push origin develop
   ```

### 2. Merge to Main

1. Create a pull request from `develop` to `main`
2. Review and merge the PR

### 3. Automatic Release Process

Once merged to `main`, the GitHub Action will automatically:

1. **Detect the version change** in the `VERSION` file
2. **Create a git tag** (e.g., `v0.2.0`)
3. **Extract changelog** for this version from `CHANGELOG.md`
4. **Create a GitHub Release** with the changelog as release notes
5. **Build Docker images** for linux/amd64 and linux/arm64
6. **Tag and push images**:
   - `ghcr.io/polarfoxdev/marina:0.2.0`
   - `ghcr.io/polarfoxdev/marina:latest`
   - `polarfoxdev/marina:0.2.0`
   - `polarfoxdev/marina:latest`

### 4. Post-Release

After the automated release completes:

1. Pull the latest changes from `main`
2. Merge `main` back to `develop` to keep branches in sync:

   ```bash
   git checkout develop
   git pull origin develop
   git merge main
   git push origin develop
   ```

## Manual Build (Development Only)

For testing or manual builds without creating a release:

1. Go to Actions → "Build and Push Docker Image"
2. Click "Run workflow"
3. Enter the desired version number
4. Click "Run workflow"

This will build and push images but won't create a GitHub release or tag.

## Version Numbering Guidelines

- **MAJOR** (x.0.0): Breaking changes or major feature releases
- **MINOR** (0.x.0): New features, backward compatible
- **PATCH** (0.0.x): Bug fixes and minor improvements

## Using the Docker Image

Pull the latest version:

```bash
docker pull ghcr.io/polarfoxdev/marina:latest
# or
docker pull polarfoxdev/marina:latest
```

Pull a specific version:

```bash
docker pull ghcr.io/polarfoxdev/marina:0.2.0
# or
docker pull polarfoxdev/marina:0.2.0
```

## Troubleshooting

**Release workflow didn't trigger:**

- Ensure the `VERSION` file was actually changed in the merge to `main`
- Check the Actions tab for workflow runs and any errors

**Tag already exists:**

- The workflow will skip tag creation if it already exists
- Delete the tag if you need to recreate it: `git tag -d v0.2.0 && git push origin :refs/tags/v0.2.0`

**Docker Hub push fails:**

- Ensure `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` secrets are configured in repository settings

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
