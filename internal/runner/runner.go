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
	"github.com/polarfoxDev/marina/internal/model"
)

type Runner struct {
	Cron            *cron.Cron
	BackupInstances map[string]*backend.BackupInstance // keyed by destination ID
	Docker          *client.Client
	VolumeRoot      string // usually /var/lib/docker/volumes
	StagingDir      string // e.g. /backup/tmp
	Logf            func(string, ...any)

	// Track scheduled jobs for dynamic updates
	scheduledJobs map[string]cron.EntryID       // target ID -> cron entry ID
	targets       map[string]model.BackupTarget // target ID -> target config
}

func New(instances map[string]*backend.BackupInstance, docker *client.Client, volRoot, staging string, logf func(string, ...any)) *Runner {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Runner{
		Cron:            cron.New(cron.WithParser(cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow))),
		BackupInstances: instances,
		Docker:          docker,
		VolumeRoot:      volRoot,
		StagingDir:      staging,
		Logf:            logf,
		scheduledJobs:   make(map[string]cron.EntryID),
		targets:         make(map[string]model.BackupTarget),
	}
}

func (r *Runner) ScheduleTarget(t model.BackupTarget) error {
	// Check if already scheduled
	if existingEntry, ok := r.scheduledJobs[t.ID]; ok {
		// Check if schedule or config changed
		if existing, found := r.targets[t.ID]; found {
			if existing.Schedule == t.Schedule && targetsEqual(existing, t) {
				// No changes, skip
				return nil
			}
		}
		// Remove old entry
		r.Cron.Remove(existingEntry)
		delete(r.scheduledJobs, t.ID)
	}

	// Schedule new job
	entryID, err := r.Cron.AddFunc(t.Schedule, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
		defer cancel()
		if err := r.runOnce(ctx, t); err != nil {
			r.Logf("job %s failed: %v", t.ID, err)
		} else {
			r.Logf("job %s done", t.ID)
		}
	})
	if err != nil {
		return err
	}

	r.scheduledJobs[t.ID] = entryID
	r.targets[t.ID] = t
	return nil
}

// RemoveTarget removes a scheduled backup target
func (r *Runner) RemoveTarget(targetID string) {
	if entryID, ok := r.scheduledJobs[targetID]; ok {
		r.Cron.Remove(entryID)
		delete(r.scheduledJobs, targetID)
		delete(r.targets, targetID)
	}
}

// SyncTargets updates the scheduler with a new set of discovered targets
// Adds new targets, removes deleted ones, and updates changed ones
func (r *Runner) SyncTargets(newTargets []model.BackupTarget) {
	r.Logf("syncing %d discovered targets...", len(newTargets))
	newSet := make(map[string]model.BackupTarget)
	for _, t := range newTargets {
		newSet[t.ID] = t
	}

	// Remove targets that no longer exist
	for id := range r.targets {
		if _, exists := newSet[id]; !exists {
			r.Logf("removing target %s (no longer exists)", id)
			r.RemoveTarget(id)
		}
	}

	// Add or update targets
	for id, t := range newSet {
		// Validate instance exists
		if _, ok := r.BackupInstances[string(t.InstanceID)]; !ok {
			r.Logf("WARNING: target %s references unknown instance %q, skipping", id, t.InstanceID)
			continue
		}

		// Check if it's new or changed BEFORE scheduling
		existing, found := r.targets[id]
		isNew := !found
		isChanged := found && !targetsEqual(existing, t)

		if err := r.ScheduleTarget(t); err != nil {
			r.Logf("schedule %s: %v", id, err)
		} else {
			// Only log if it's new or changed
			if isNew || isChanged {
				r.Logf("scheduled %s (name: %s, instance: %s, schedule: %s)", id, t.Name, t.InstanceID, t.Schedule)
			}
		}
	}
}

// targetsEqual checks if two targets are functionally equivalent
func targetsEqual(a, b model.BackupTarget) bool {
	return a.Schedule == b.Schedule &&
		a.InstanceID == b.InstanceID &&
		a.Type == b.Type &&
		a.StopAttached == b.StopAttached &&
		a.PreHook == b.PreHook &&
		a.PostHook == b.PostHook &&
		strings.Join(a.Paths, ",") == strings.Join(b.Paths, ",") &&
		strings.Join(a.Tags, ",") == strings.Join(b.Tags, ",") &&
		strings.Join(a.Exclude, ",") == strings.Join(b.Exclude, ",") &&
		strings.Join(a.DumpArgs, ",") == strings.Join(b.DumpArgs, ",") &&
		a.Retention.KeepDaily == b.Retention.KeepDaily &&
		a.Retention.KeepWeekly == b.Retention.KeepWeekly &&
		a.Retention.KeepMonthly == b.Retention.KeepMonthly
}

func (r *Runner) Start()                   { r.Cron.Start() }
func (r *Runner) Stop(ctx context.Context) { r.Cron.Stop() }

func (r *Runner) TriggerNow(ctx context.Context, t model.BackupTarget) error {
	return r.runOnce(ctx, t)
}

func (r *Runner) runOnce(ctx context.Context, t model.BackupTarget) error {
	switch t.Type {
	case model.TargetVolume:
		return r.backupVolume(ctx, t)
	case model.TargetDB:
		return r.backupDB(ctx, t)
	default:
		return fmt.Errorf("unknown target type: %s", t.Type)
	}
}

func (r *Runner) backupVolume(ctx context.Context, target model.BackupTarget) error {
	dest, ok := r.BackupInstances[string(target.InstanceID)]
	if !ok {
		return fmt.Errorf("instance %q not found", target.InstanceID)
	}

	// Optionally call pre hook in first attached container
	if target.PreHook != "" && len(target.AttachedCtrs) > 0 {
		if _, err := docker.ExecInContainer(ctx, r.Docker, target.AttachedCtrs[0], []string{"/bin/sh", "-lc", target.PreHook}); err != nil {
			return fmt.Errorf("prehook: %w", err)
		}
		defer func() {
			_, _ = docker.ExecInContainer(ctx, r.Docker, target.AttachedCtrs[0], []string{"/bin/sh", "-lc", target.PostHook})
		}()
	}

	if target.StopAttached && len(target.AttachedCtrs) > 0 {
		// stop all attached containers
		for _, ctr := range target.AttachedCtrs {
			// skip if already stopped
			running, err := docker.IsContainerRunning(ctx, r.Docker, ctr)
			if err != nil {
				return fmt.Errorf("check attached container state: %w", err)
			}
			if !running {
				continue
			}
			// skip if mounted read-only
			ctrInfo, err := r.Docker.ContainerInspect(ctx, ctr)
			if err != nil {
				return fmt.Errorf("inspect attached container: %w", err)
			}
			if ctrInfo.Mounts[0].Mode == "ro" {
				r.Logf("attached container %s is mounted read-only, skipping stop/start", ctr)
				continue
			}
			r.Logf("stopping attached container %s", ctr)
			if err := docker.StopContainer(ctx, r.Docker, ctr); err != nil {
				return fmt.Errorf("stop attached container: %w", err)
			}
			defer func() {
				r.Logf("starting attached container %s", ctr)
				_ = docker.StartContainer(ctx, r.Docker, ctr)
			}()
		}
	}

	var paths []string
	for _, p := range target.Paths {
		paths = append(paths, filepath.Join(r.VolumeRoot, target.VolumeName, "_data", p))
	}
	_, err := dest.Backup(ctx, paths, target.Tags, target.Exclude)
	if err != nil {
		return err
	}
	_, _ = dest.DeleteOldSnapshots(ctx, target.Retention.KeepDaily, target.Retention.KeepWeekly, target.Retention.KeepMonthly)
	return nil
}

func (r *Runner) backupDB(ctx context.Context, target model.BackupTarget) error {
	dest, ok := r.BackupInstances[string(target.InstanceID)]
	if !ok {
		return fmt.Errorf("instance %q not found", target.InstanceID)
	}

	// Pre hook
	if target.PreHook != "" {
		if _, err := docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", target.PreHook}); err != nil {
			return fmt.Errorf("prehook: %w", err)
		}
		defer func() {
			_, _ = docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", target.PostHook})
		}()
	}

	// Dump inside the DB container into a temporary directory, then copy to local staging
	ts := time.Now().UTC().Format("20060102-150405")
	containerDumpDir := fmt.Sprintf("/tmp/marina-%s", ts)
	mk := fmt.Sprintf("mkdir -p %q", containerDumpDir)
	if _, err := docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", mk}); err != nil {
		return fmt.Errorf("prepare staging: %w", err)
	}
	defer func() {
		_, _ = docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", fmt.Sprintf("rm -rf %q", containerDumpDir)})
	}()

	hostStagingDir := filepath.Join(r.StagingDir, "db", target.Name, ts)
	if err := os.MkdirAll(hostStagingDir, 0o755); err != nil {
		return fmt.Errorf("prepare host staging: %w", err)
	}
	defer func() { _ = os.RemoveAll(hostStagingDir) }()

	dumpCmd, dumpFile, err := buildDumpCmd(target, containerDumpDir)
	if err != nil {
		return err
	}
	output, err := docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", dumpCmd})
	if err != nil {
		return fmt.Errorf("dump failed: %w", err)
	}
	r.Logf("dump output: %s", output)

	hostDumpPath, err := docker.CopyFileFromContainer(ctx, r.Docker, target.ContainerID, dumpFile, hostStagingDir, func(expected, written int64) {
		if expected > 0 && expected != written {
			r.Logf("copy warning (%s): expected %d bytes, wrote %d", target.Name, expected, written)
		}
	})
	if err != nil {
		return err
	}

	_, err = dest.Backup(ctx, []string{hostDumpPath}, target.Tags, target.Exclude)
	if err != nil {
		return err
	}
	_, _ = dest.DeleteOldSnapshots(ctx, target.Retention.KeepDaily, target.Retention.KeepWeekly, target.Retention.KeepMonthly)
	return nil
}

func buildDumpCmd(t model.BackupTarget, dumpDir string) (cmd string, output string, err error) {
	switch t.DBKind {
	case "postgres":
		file := filepath.Join(dumpDir, "dump.pgcustom")
		// expect PGPASSWORD etc already present in container env
		args := stringsJoin(append([]string{"pg_dump", "-Fc"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	case "mysql":
		file := filepath.Join(dumpDir, "dump.sql")
		args := stringsJoin(append([]string{"mysqldump", "--single-transaction"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file, nil
	case "mariadb":
		file := filepath.Join(dumpDir, "dump.sql")
		args := stringsJoin(append([]string{"mariadb-dump", "--single-transaction"}, t.DumpArgs...)...)
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
