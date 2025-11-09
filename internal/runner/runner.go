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
	}
}

func (r *Runner) ScheduleTarget(t model.BackupTarget) error {
	_, err := r.Cron.AddFunc(t.Schedule, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
		defer cancel()
		if err := r.runOnce(ctx, t); err != nil {
			r.Logf("job %s failed: %v", t.ID, err)
		} else {
			r.Logf("job %s done", t.ID)
		}
	})
	return err
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

	dumpCmd, dumpFile := buildDumpCmd(target, containerDumpDir)
	if _, err := docker.ExecInContainer(ctx, r.Docker, target.ContainerID, []string{"/bin/sh", "-lc", dumpCmd}); err != nil {
		return fmt.Errorf("dump failed: %w", err)
	}

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

func buildDumpCmd(t model.BackupTarget, dumpDir string) (cmd string, output string) {
	switch t.DBKind {
	case "postgres":
		file := filepath.Join(dumpDir, "dump.pgcustom")
		// expect PGPASSWORD etc already present in container env
		args := stringsJoin(append([]string{"pg_dump", "-Fc"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file
	case "mysql":
		file := filepath.Join(dumpDir, "dump.sql")
		args := stringsJoin(append([]string{"mysqldump", "--single-transaction"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file
	case "mariadb":
		file := filepath.Join(dumpDir, "dump.sql")
		args := stringsJoin(append([]string{"mariadb-dump", "--single-transaction"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file
	case "mongo":
		file := filepath.Join(dumpDir, "dump.archive")
		args := stringsJoin(append([]string{"mongodump", "--archive"}, t.DumpArgs...)...)
		return fmt.Sprintf("%s > %q", args, file), file
	case "redis":
		file := filepath.Join(dumpDir, "dump.rdb")
		// A simple way is to copy RDB; for running instances, BGSAVE then copy
		return fmt.Sprintf("redis-cli BGSAVE && cp /data/dump.rdb %q", file), file
	default:
		file := filepath.Join(dumpDir, "dump.raw")
		return fmt.Sprintf("echo 'unsupported db kind %q' > %q", t.DBKind, file), file
	}
}

func stringsJoin(ss ...string) string { return strings.Join(ss, " ") }
