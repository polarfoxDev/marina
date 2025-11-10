package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/robfig/cron/v3"

	"github.com/polarfoxDev/marina/internal/backend"
	"github.com/polarfoxDev/marina/internal/docker"
	"github.com/polarfoxDev/marina/internal/logging"
	"github.com/polarfoxDev/marina/internal/model"
)

type cleanupFunc func()

type Runner struct {
	Cron            *cron.Cron
	BackupInstances map[string]*backend.BackupInstance // keyed by destination ID
	Docker          *client.Client
	Logger          *logging.Logger

	// Track scheduled jobs for dynamic updates
	scheduledJobs map[model.InstanceID]cron.EntryID            // instance ID -> cron entry ID
	jobs          map[model.InstanceID]model.InstanceBackupJob // instance ID -> backup job config
}

func New(instances map[string]*backend.BackupInstance, docker *client.Client, logger *logging.Logger) *Runner {
	return &Runner{
		Cron:            cron.New(cron.WithParser(cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow))),
		BackupInstances: instances,
		Docker:          docker,
		Logger:          logger,
		scheduledJobs:   make(map[model.InstanceID]cron.EntryID),
		jobs:            make(map[model.InstanceID]model.InstanceBackupJob),
	}
}

func (r *Runner) ScheduleJob(job model.InstanceBackupJob) error {
	// Check if already scheduled
	if existingEntry, ok := r.scheduledJobs[job.InstanceID]; ok {
		// Check if schedule or config changed
		if existing, found := r.jobs[job.InstanceID]; found {
			if existing.Schedule == job.Schedule && jobsEqual(existing, job) {
				// No changes, skip
				return nil
			}
		}
		// Remove old entry
		r.Cron.Remove(existingEntry)
		delete(r.scheduledJobs, job.InstanceID)
	}

	// Schedule new job
	entryID, err := r.Cron.AddFunc(job.Schedule, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
		defer cancel()

		// Create instance-level logger
		instanceLogger := r.Logger.NewJobLogger(string(job.InstanceID))

		instanceLogger.Info("instance backup started (%d targets)", len(job.Targets))
		startTime := time.Now()

		if err := r.runInstanceBackup(ctx, job, instanceLogger); err != nil {
			instanceLogger.Error("instance backup failed: %v", err)
		} else {
			duration := time.Since(startTime)
			instanceLogger.Info("instance backup completed (duration: %v)", duration)
		}
	})
	if err != nil {
		return err
	}

	r.scheduledJobs[job.InstanceID] = entryID
	r.jobs[job.InstanceID] = job
	return nil
}

// RemoveJob removes a scheduled backup job for an instance
func (r *Runner) RemoveJob(instanceID model.InstanceID) {
	if entryID, ok := r.scheduledJobs[instanceID]; ok {
		r.Cron.Remove(entryID)
		delete(r.scheduledJobs, instanceID)
		delete(r.jobs, instanceID)
	}
}

// SyncJobs updates the scheduler with a new set of discovered jobs
// Adds new jobs, removes deleted ones, and updates changed ones
func (r *Runner) SyncJobs(newJobs []model.InstanceBackupJob) {
	r.Logger.Info("syncing %d discovered instance jobs...", len(newJobs))
	newSet := make(map[model.InstanceID]model.InstanceBackupJob)
	for _, j := range newJobs {
		newSet[j.InstanceID] = j
	}

	// Remove jobs that no longer exist
	for id := range r.jobs {
		if _, exists := newSet[id]; !exists {
			r.Logger.Info("removing instance job %s (no longer exists)", id)
			r.RemoveJob(id)
		}
	}

	// Add or update jobs
	for id, job := range newSet {
		// Validate instance exists
		if _, ok := r.BackupInstances[string(job.InstanceID)]; !ok {
			r.Logger.Warn("instance job %s references unknown instance, skipping", id)
			continue
		}

		// Check if it's new or changed BEFORE scheduling
		existing, found := r.jobs[id]
		isNew := !found
		isChanged := found && !jobsEqual(existing, job)

		if err := r.ScheduleJob(job); err != nil {
			r.Logger.Error("schedule instance %s: %v", id, err)
		} else {
			// Only log if it's new or changed
			if isNew || isChanged {
				r.Logger.Info("scheduled instance %s (%d targets, schedule: %s)", id, len(job.Targets), job.Schedule)
			}
		}
	}
}

// jobsEqual checks if two instance backup jobs are functionally equivalent
func jobsEqual(a, b model.InstanceBackupJob) bool {
	if a.Schedule != b.Schedule || len(a.Targets) != len(b.Targets) {
		return false
	}

	// Compare targets (simplified - just check IDs and key fields)
	aIDs := make(map[string]bool)
	for _, t := range a.Targets {
		aIDs[t.ID] = true
	}
	for _, t := range b.Targets {
		if !aIDs[t.ID] {
			return false
		}
	}

	return true
}
func (r *Runner) Start()                   { r.Cron.Start() }
func (r *Runner) Stop(ctx context.Context) { r.Cron.Stop() }

func (r *Runner) TriggerNow(ctx context.Context, job model.InstanceBackupJob) error {
	instanceLogger := r.Logger.NewJobLogger(string(job.InstanceID))
	return r.runInstanceBackup(ctx, job, instanceLogger)
}

// runInstanceBackup executes all backups for an instance in a single Restic operation
func (r *Runner) runInstanceBackup(ctx context.Context, job model.InstanceBackupJob, instanceLogger *logging.JobLogger) error {
	dest, ok := r.BackupInstances[string(job.InstanceID)]
	if !ok {
		return fmt.Errorf("instance %q not found", job.InstanceID)
	}

	// Generate single timestamp for this instance backup run
	timestamp := time.Now().Format("20060102-150405")

	// Collect all staging paths from all targets
	var allPaths []string
	var allTags []string
	var allExcludes []string

	// Track cleanup functions to defer
	var cleanups []cleanupFunc
	defer func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}()

	// Track failed targets
	var failedTargets []string

	// Process each target and collect staged paths
	for _, target := range job.Targets {
		// Create target-specific logger for detailed logs
		targetLogger := instanceLogger.WithTarget(target.ID)
		targetLogger.Info("preparing %s: %s", target.Type, target.Name)

		switch target.Type {
		case model.TargetVolume:
			paths, cleanup, err := r.prepareVolumeBackup(ctx, string(job.InstanceID), timestamp, target, targetLogger)
			if err != nil {
				targetLogger.Warn("failed to prepare volume: %v", err)
				failedTargets = append(failedTargets, fmt.Sprintf("volume:%s", target.Name))
				continue // Skip this target but continue with others
			}
			allPaths = append(allPaths, paths...)
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

		case model.TargetDB:
			path, cleanup, err := r.prepareDBBackup(ctx, string(job.InstanceID), timestamp, target, targetLogger)
			if err != nil {
				targetLogger.Warn("failed to prepare db: %v", err)
				failedTargets = append(failedTargets, fmt.Sprintf("db:%s", target.Name))
				continue // Skip this target but continue with others
			}
			allPaths = append(allPaths, path)
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

		default:
			targetLogger.Warn("unknown target type: %s", target.Type)
			failedTargets = append(failedTargets, fmt.Sprintf("unknown:%s", target.Name))
			continue
		}

		// Collect tags and excludes from all targets
		allTags = append(allTags, target.Tags...)
		allExcludes = append(allExcludes, target.Exclude...)
	}

	// Check if all targets failed
	if len(allPaths) == 0 {
		if len(failedTargets) > 0 {
			return fmt.Errorf("all targets failed: %v", failedTargets)
		}
		return fmt.Errorf("no paths to backup")
	}

	// Log warning if some targets failed
	if len(failedTargets) > 0 {
		instanceLogger.Warn("backup proceeding with %d/%d targets (%d failed: %v)",
			len(allPaths), len(job.Targets), len(failedTargets), failedTargets)
	}

	// Deduplicate tags and excludes
	allTags = deduplicate(allTags)
	allExcludes = deduplicate(allExcludes)

	// Perform single backup with all collected paths
	instanceLogger.Info("backing up %d paths to instance %s", len(allPaths), job.InstanceID)
	_, err := dest.Backup(ctx, allPaths, allTags, allExcludes)
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	// Apply retention policy
	_, _ = dest.DeleteOldSnapshots(ctx, job.Retention.KeepDaily, job.Retention.KeepWeekly, job.Retention.KeepMonthly)

	return nil
}

// prepareVolumeBackup prepares a volume for backup and returns the staged paths and cleanup function
func (r *Runner) prepareVolumeBackup(ctx context.Context, instanceID, timestamp string, target model.BackupTarget, jobLogger *logging.JobLogger) ([]string, cleanupFunc, error) {
	// Execute pre-hook in first attached container
	if target.PreHook != "" && len(target.AttachedCtrs) > 0 {
		if _, err := docker.ExecInContainer(ctx, r.Docker, target.AttachedCtrs[0], []string{"/bin/sh", "-lc", target.PreHook}); err != nil {
			return nil, nil, fmt.Errorf("prehook: %w", err)
		}
		// Defer post-hook
		defer func() {
			if target.PostHook != "" {
				_, _ = docker.ExecInContainer(ctx, r.Docker, target.AttachedCtrs[0], []string{"/bin/sh", "-lc", target.PostHook})
			}
		}()
	}

	// Stop attached containers if needed
	var stoppedContainers []string
	if target.StopAttached && len(target.AttachedCtrs) > 0 {
		for _, ctr := range target.AttachedCtrs {
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
	jobLogger.Info("copying volume %s to staging", target.VolumeName)
	stagedPaths, err := docker.CopyVolumeToStaging(ctx, r.Docker, instanceID, timestamp, target.VolumeName, target.Paths)
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
			dir := firstPath
			for {
				parent := filepath.Dir(dir)
				if parent == "/backup" {
					_ = os.RemoveAll(dir)
					break
				}
				if parent == dir || parent == "/" {
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

	return stagedPaths, cleanup, nil
}

// prepareDBBackup prepares a database backup and returns the staged path and cleanup function
func (r *Runner) prepareDBBackup(ctx context.Context, instanceID, timestamp string, target model.BackupTarget, jobLogger *logging.JobLogger) (string, cleanupFunc, error) {
	// Execute pre-hook
	if target.PreHook != "" {
		if _, err := docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", target.PreHook}); err != nil {
			return "", nil, fmt.Errorf("prehook: %w", err)
		}
		// Defer post-hook
		defer func() {
			if target.PostHook != "" {
				_, _ = docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", target.PostHook})
			}
		}()
	}

	// Create dump inside DB container (use same timestamp as instance backup)
	containerDumpDir := fmt.Sprintf("/tmp/marina-%s", timestamp)
	mk := fmt.Sprintf("mkdir -p %q", containerDumpDir)
	if _, err := docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", mk}); err != nil {
		return "", nil, fmt.Errorf("prepare dump dir: %w", err)
	}

	// Prepare host staging directory
	hostStagingDir := filepath.Join("/backup", instanceID, timestamp, "dbs", target.Name)
	if err := os.MkdirAll(hostStagingDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("prepare host staging: %w", err)
	}

	// Build and execute dump command
	dumpCmd, dumpFile, err := buildDumpCmd(target, containerDumpDir)
	if err != nil {
		return "", nil, err
	}

	jobLogger.Debug("executing dump command")
	output, err := docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", dumpCmd})
	if err != nil {
		return "", nil, fmt.Errorf("dump failed: %w", err)
	}
	jobLogger.Debug("dump output: %s", output)

	// Copy dump file from container
	hostDumpPath, err := docker.CopyFileFromContainer(ctx, r.Docker, target.ContainerID, dumpFile, hostStagingDir, func(expected, written int64) {
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
		_, _ = docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", fmt.Sprintf("rm -rf %q", containerDumpDir)})
		// Clean up host staging directory
		_ = os.RemoveAll(hostStagingDir)
	}

	return hostDumpPath, cleanup, nil
}

// deduplicate removes duplicate strings from a slice
func deduplicate(slice []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	for _, item := range slice {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}
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
		// Use --all-databases to dump everything
		args := stringsJoin(append([]string{"mysqldump", "--single-transaction", "--all-databases"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	case "mariadb":
		file := filepath.Join(dumpDir, "dump.sql")
		// Use --all-databases to dump everything
		args := stringsJoin(append([]string{"mariadb-dump", "--single-transaction", "--all-databases"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	case "mongo":
		file := filepath.Join(dumpDir, "dump.archive")
		args := stringsJoin(append([]string{"mongodump", "--archive"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	default:
		return "", "", fmt.Errorf("unsupported db kind %q", t.DBKind)
	}
}

func stringsJoin(ss ...string) string { return strings.Join(ss, " ") }
