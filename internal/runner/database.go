package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"

	"github.com/polarfoxDev/marina/internal/docker"
	"github.com/polarfoxDev/marina/internal/logging"
	"github.com/polarfoxDev/marina/internal/model"
)

// stageDatabase prepares a database backup and returns the staged path and cleanup function
func (r *Runner) stageDatabase(ctx context.Context, instanceID, timestamp string, target model.BackupTarget, jobLogger *logging.JobLogger) (string, cleanupFunc, error) {
	// Look up container from Docker to ensure it exists
	containers, err := r.Docker.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", nil, fmt.Errorf("list containers: %w", err)
	}

	var ctrInfo *container.Summary
	for _, c := range containers {
		// Match by container name (with or without leading slash)
		name := strings.TrimPrefix(c.Names[0], "/")
		if name == target.Name {
			ctrInfo = &c
			break
		}
	}

	if ctrInfo == nil {
		return "", nil, fmt.Errorf("database container %q not found", target.Name)
	}

	containerID := ctrInfo.ID
	jobLogger.Debug("found container: %s (id: %s)", target.Name, containerID)

	// Auto-detect database kind if not specified
	dbKind := target.DBKind
	if dbKind == "" {
		dbKind = detectDBKind(ctrInfo.Image)
		if dbKind == "" {
			return "", nil, fmt.Errorf("could not auto-detect database type from image %q", ctrInfo.Image)
		}
		jobLogger.Debug("auto-detected database kind: %s", dbKind)
	}

	// Execute pre-hook
	if target.PreHook != "" {
		jobLogger.Debug("executing pre-hook")
		output, err := docker.ExecInContainer(ctx, r.Docker, containerID, []string{"/bin/sh", "-lc", target.PreHook})
		if err != nil {
			return "", nil, fmt.Errorf("prehook: %w", err)
		}
		if output != "" {
			jobLogger.Debug("pre-hook output: %s", output)
		}
		// Defer post-hook
		defer func() {
			if target.PostHook != "" {
				jobLogger.Debug("executing post-hook")
				output, err := docker.ExecInContainer(ctx, r.Docker, containerID, []string{"/bin/sh", "-lc", target.PostHook})
				if err != nil {
					jobLogger.Warn("post-hook failed: %v", err)
				} else if output != "" {
					jobLogger.Debug("post-hook output: %s", output)
				}
			}
		}()
	}

	// Create dump inside DB container (use same timestamp as instance backup)
	containerDumpDir := fmt.Sprintf("/tmp/marina-%s", timestamp)
	mk := fmt.Sprintf("mkdir -p %q", containerDumpDir)
	if _, err := docker.ExecInContainer(ctx, r.Docker, containerID, []string{"/bin/sh", "-lc", mk}); err != nil {
		return "", nil, fmt.Errorf("prepare dump dir: %w", err)
	}

	// Prepare host staging directory
	hostStagingDir := filepath.Join("/backup", instanceID, timestamp, "db", target.Name)
	if err := os.MkdirAll(hostStagingDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("prepare host staging: %w", err)
	}

	// Build target with resolved values for dump command generation
	resolvedTarget := target
	resolvedTarget.ContainerID = containerID
	resolvedTarget.DBKind = dbKind

	// Build and execute dump command
	dumpCmd, dumpFile, err := buildDumpCmd(resolvedTarget, containerDumpDir)
	if err != nil {
		return "", nil, err
	}

	jobLogger.Info("creating database dump")
	output, err := docker.ExecInContainer(ctx, r.Docker, containerID, []string{"/bin/sh", "-lc", dumpCmd})
	if err != nil {
		return "", nil, fmt.Errorf("dump failed: %w", err)
	}
	jobLogger.Debug("dump output: %s", output)

	// Copy dump file from container
	hostDumpPath, err := docker.CopyFileFromContainer(ctx, r.Docker, containerID, dumpFile, hostStagingDir, func(expected, written int64) {
		if expected > 0 && expected != written {
			jobLogger.Warn("copy warning: expected %d bytes, wrote %d", expected, written)
		}
	})
	if err != nil {
		return "", nil, err
	}

	// Create cleanup function
	cleanup := func() {
		// Clean up container dump directory
		_, _ = docker.ExecInContainer(ctx, r.Docker, containerID, []string{"/bin/sh", "-lc", fmt.Sprintf("rm -rf %q", containerDumpDir)})
		// Clean up host staging directory
		_ = os.RemoveAll(hostStagingDir)
	}

	// Validate dump file has content
	if err := validateFileSize([]string{hostDumpPath}, jobLogger); err != nil {
		// Run cleanup immediately since we're returning an error and the cleanup
		// function won't be added to the deferred cleanups list in runInstanceBackup
		cleanup()
		return "", nil, fmt.Errorf("dump validation failed: %w", err)
	}

	return hostDumpPath, cleanup, nil
}

// buildDumpCmd generates the appropriate dump command for a database target
func buildDumpCmd(t model.BackupTarget, dumpDir string) (cmd string, output string, err error) {
	switch t.DBKind {
	case "postgres":
		file := filepath.Join(dumpDir, "dump.sql")
		// Use pg_dumpall to dump all databases with postgres user
		// PGPASSWORD env var should be set in container
		args := stringsJoin(append([]string{"pg_dumpall", "-U", "postgres"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	case "mysql":
		file := filepath.Join(dumpDir, "dump.sql")
		// Build dump command with automatic credential fallback
		// If no dump.args provided, try MYSQL_ROOT_PASSWORD, then MYSQL_PASSWORD
		if len(t.DumpArgs) == 0 {
			cmd := fmt.Sprintf(`
				mysqldump --single-transaction --all-databases -uroot -p"$MYSQL_ROOT_PASSWORD" > %q 2>/tmp/dump.err || \
				(echo "Root dump failed, trying MYSQL_USER..." >&2 && \
				 mysqldump --single-transaction --all-databases -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" > %q)
			`, file, file)
			return cmd, file, nil
		}
		// Use provided dump.args
		baseArgs := []string{"mysqldump", "--single-transaction", "--all-databases"}
		args := stringsJoin(append(baseArgs, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	case "mariadb":
		file := filepath.Join(dumpDir, "dump.sql")
		// Build dump command with automatic credential fallback
		// If no dump.args provided, try MARIADB_ROOT_PASSWORD, then MARIADB_PASSWORD
		if len(t.DumpArgs) == 0 {
			cmd := fmt.Sprintf(`
				mariadb-dump --single-transaction --all-databases -uroot -p"$MARIADB_ROOT_PASSWORD" > %q 2>/tmp/dump.err || \
				(echo "Root dump failed, trying MARIADB_USER..." >&2 && \
				 mariadb-dump --single-transaction --all-databases -u"$MARIADB_USER" -p"$MARIADB_PASSWORD" > %q)
			`, file, file)
			return cmd, file, nil
		}
		// Use provided dump.args
		baseArgs := []string{"mariadb-dump", "--single-transaction", "--all-databases"}
		args := stringsJoin(append(baseArgs, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	case "mongo":
		file := filepath.Join(dumpDir, "dump.archive")
		args := stringsJoin(append([]string{"mongodump", "--archive"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	default:
		return "", "", fmt.Errorf("unsupported db kind %q", t.DBKind)
	}
}

// detectDBKind attempts to detect the database type from the container image name
func detectDBKind(imageName string) string {
	// Convert to lowercase for case-insensitive matching
	image := strings.ToLower(imageName)

	// Check for common database image patterns
	// Format is typically: "postgres:16" or "docker.io/library/postgres:16"
	switch {
	case strings.Contains(image, "postgres"):
		return "postgres"
	case strings.Contains(image, "mysql"):
		return "mysql"
	case strings.Contains(image, "mariadb"):
		return "mariadb"
	case strings.Contains(image, "mongo"):
		return "mongo"
	case strings.Contains(image, "redis"):
		return "redis"
	default:
		return ""
	}
}

func stringsJoin(ss ...string) string { return strings.Join(ss, " ") }
