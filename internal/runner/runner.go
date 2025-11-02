package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/robfig/cron/v3"

	"github.com/polarfoxDev/marina/internal/docker"
	"github.com/polarfoxDev/marina/internal/model"
	"github.com/polarfoxDev/marina/internal/restic"
)

type Runner struct {
	Cron       *cron.Cron
	Repo       *restic.RepoConfig
	Docker     *client.Client
	VolumeRoot string // usually /var/lib/docker/volumes
	StagingDir string // e.g. /backup/tmp
	Logf       func(string, ...any)
}

func New(repo *restic.RepoConfig, docker *client.Client, volRoot, staging string, logf func(string, ...any)) *Runner {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Runner{
		Cron: cron.New(cron.WithParser(cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow))),
		Repo: repo, Docker: docker, VolumeRoot: volRoot, StagingDir: staging, Logf: logf,
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

func (r *Runner) backupVolume(ctx context.Context, t model.BackupTarget) error {
	// Optionally call pre hook in first attached container
	if t.PreHook != "" && len(t.AttachedCtrs) > 0 {
		if _, err := docker.ExecInContainer(ctx, r.Docker, t.AttachedCtrs[0], []string{"/bin/sh", "-lc", t.PreHook}); err != nil {
			return fmt.Errorf("prehook: %w", err)
		}
		defer func() {
			_, _ = docker.ExecInContainer(ctx, r.Docker, t.AttachedCtrs[0], []string{"/bin/sh", "-lc", t.PostHook})
		}()
	}

	if t.StopAttached && len(t.AttachedCtrs) > 0 {
		// stop all attached containers
		for _, ctr := range t.AttachedCtrs {
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
	for _, p := range t.Paths {
		paths = append(paths, filepath.Join(r.VolumeRoot, t.VolumeName, "_data", p))
	}
	_, err := r.Repo.Backup(ctx, string(t.Repo), paths, t.Tags, t.Exclude)
	if err != nil {
		return err
	}
	_, _ = r.Repo.ForgetPrune(ctx, string(t.Repo), t.Retention.KeepDaily, t.Retention.KeepWeekly, t.Retention.KeepMonthly)
	return nil
}

func (r *Runner) backupDB(ctx context.Context, t model.BackupTarget) error {
	// Pre hook
	if t.PreHook != "" {
		if _, err := docker.ExecInContainer(ctx, r.Docker, t.ContainerID, []string{"/bin/sh", "-lc", t.PreHook}); err != nil {
			return fmt.Errorf("prehook: %w", err)
		}
		defer func() {
			_, _ = docker.ExecInContainer(ctx, r.Docker, t.ContainerID, []string{"/bin/sh", "-lc", t.PostHook})
		}()
	}

	// Dump inside the DB container into staging mounted via bind (or use `docker cp` as fallback)
	ts := time.Now().UTC().Format("20060102-150405")
	dumpDir := filepath.Join(r.StagingDir, "db", t.Name, ts)
	mk := fmt.Sprintf("mkdir -p %q", dumpDir)
	if _, err := docker.ExecInContainer(ctx, r.Docker, t.ContainerID, []string{"/bin/sh", "-lc", mk}); err != nil {
		return fmt.Errorf("prepare staging: %w", err)
	}

	dumpCmd, dumpFile := buildDumpCmd(t, dumpDir)
	if _, err := docker.ExecInContainer(ctx, r.Docker, t.ContainerID, []string{"/bin/sh", "-lc", dumpCmd}); err != nil {
		return fmt.Errorf("dump failed: %w", err)
	}

	// restic backup
	_, err := r.Repo.Backup(ctx, string(t.Repo), []string{dumpFile}, t.Tags, t.Exclude)
	if err != nil {
		return err
	}
	_, _ = r.Repo.ForgetPrune(ctx, string(t.Repo), t.Retention.KeepDaily, t.Retention.KeepWeekly, t.Retention.KeepMonthly)

	// Cleanup (best-effort)
	_, _ = docker.ExecInContainer(ctx, r.Docker, t.ContainerID, []string{"/bin/sh", "-lc", fmt.Sprintf("rm -rf %q", dumpDir)})
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
