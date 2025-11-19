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

// stageVolume prepares a volume for backup and returns the staged paths and cleanup function
func (r *Runner) stageVolume(ctx context.Context, instanceID, timestamp string, target model.BackupTarget, jobLogger *logging.JobLogger) ([]string, cleanupFunc, error) {
	// Look up volume from Docker to ensure it exists
	volumeInfo, err := r.Docker.VolumeInspect(ctx, target.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("volume %q not found: %w", target.Name, err)
	}
	jobLogger.Debug("found volume: %s", volumeInfo.Name)

	// Find containers using this volume (for hooks and optional stopping)
	var attachedCtrs []string
	if target.PreHook != "" || target.PostHook != "" || target.StopAttached {
		containers, err := r.Docker.ContainerList(ctx, container.ListOptions{All: true})
		if err != nil {
			return nil, nil, fmt.Errorf("list containers: %w", err)
		}
		for _, c := range containers {
			for _, m := range c.Mounts {
				if m.Type == "volume" && m.Name == target.Name {
					attachedCtrs = append(attachedCtrs, c.ID)
					break
				}
			}
		}
		jobLogger.Debug("found %d containers using volume %s", len(attachedCtrs), target.Name)
	}

	// Execute pre-hook in first attached container
	if target.PreHook != "" && len(attachedCtrs) > 0 {
		jobLogger.Debug("executing pre-hook")
		output, err := docker.ExecInContainer(ctx, r.Docker, attachedCtrs[0], []string{"/bin/sh", "-lc", target.PreHook})
		if err != nil {
			return nil, nil, fmt.Errorf("prehook: %w", err)
		}
		if output != "" {
			jobLogger.Debug("pre-hook output: %s", output)
		}
		// Defer post-hook
		defer func() {
			if target.PostHook != "" {
				jobLogger.Debug("executing post-hook")
				output, err := docker.ExecInContainer(ctx, r.Docker, attachedCtrs[0], []string{"/bin/sh", "-lc", target.PostHook})
				if err != nil {
					jobLogger.Warn("post-hook failed: %v", err)
				} else if output != "" {
					jobLogger.Debug("post-hook output: %s", output)
				}
			}
		}()
	}

	// Stop attached containers if needed
	var stoppedContainers []string
	if target.StopAttached && len(attachedCtrs) > 0 {
		for _, ctr := range attachedCtrs {
			running, err := docker.IsContainerRunning(ctx, r.Docker, ctr)
			if err != nil {
				return nil, nil, fmt.Errorf("check container state: %w", err)
			}
			if !running {
				continue
			}

			// Skip if mounted read-only
			ctrInfo, err := r.Docker.ContainerInspect(ctx, ctr)
			if err != nil {
				return nil, nil, fmt.Errorf("inspect container: %w", err)
			}
			if len(ctrInfo.Mounts) > 0 && ctrInfo.Mounts[0].Mode == "ro" {
				jobLogger.Info("container %s is mounted read-only, skipping stop", ctr)
				continue
			}

			jobLogger.Info("stopping container %s", ctr)
			if err := docker.StopContainer(ctx, r.Docker, ctr); err != nil {
				return nil, nil, fmt.Errorf("stop container: %w", err)
			}
			stoppedContainers = append(stoppedContainers, ctr)
		}
	}

	// Copy volume data to staging
	jobLogger.Info("copying volume %s to staging", target.Name)
	stagedPaths, err := docker.CopyVolumeToStaging(ctx, r.Docker, r.HostBackupPath, instanceID, timestamp, target.Name, target.Paths, jobLogger)
	if err != nil {
		// Restart stopped containers before returning error
		for _, ctr := range stoppedContainers {
			_ = docker.StartContainer(ctx, r.Docker, ctr)
		}
		return nil, nil, err
	}

	// Create cleanup function
	cleanup := func() {
		// Clean up staging directory
		if len(stagedPaths) > 0 {
			firstPath := stagedPaths[0]
			// Find the volume-specific directory to remove (e.g., /backup/instance/timestamp/volume/volumename)
			// We want to remove up to the volume name level, not the entire instance directory
			dir := firstPath
			for {
				parent := filepath.Dir(dir)
				if parent == "/backup" || parent == dir || parent == "/" {
					// Reached too high or hit the root - something is wrong, don't delete
					break
				}
				// Check if parent path contains "/volume/" - if so, dir is the volume directory
				if strings.Contains(parent, "/volume/") {
					// Remove this directory (the volume-specific subdirectory)
					_ = os.RemoveAll(dir)
					break
				}
				dir = parent
			}
		}

		// Restart stopped containers
		for _, ctr := range stoppedContainers {
			jobLogger.Info("restarting container %s", ctr)
			_ = docker.StartContainer(ctx, r.Docker, ctr)
		}
	}

	// Validate staged files have content
	if err := validateFileSize(stagedPaths, jobLogger); err != nil {
		// Run cleanup immediately since we're returning an error and the cleanup
		// function won't be added to the deferred cleanups list in runInstanceBackup
		cleanup()
		return nil, nil, fmt.Errorf("validation failed: %w", err)
	}

	return stagedPaths, cleanup, nil
}
