package backend

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

type BackupInstance struct {
	ID         string
	Repository string
	Env        map[string]string
	Hostname   string
}

func (instance *BackupInstance) Close() error { return nil }

func (instance *BackupInstance) runRestic(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "restic", args...)
	// Set repository
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+instance.Repository)
	// Add custom environment variables
	for k, v := range instance.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("restic %v failed: %w\n%s", args, err, stderr.String())
	}
	return stdout.String(), nil
}

func (instance *BackupInstance) Init(ctx context.Context) error {
	// Check if already initialized by running 'restic snapshots'
	_, err := instance.runRestic(ctx, "snapshots")
	if err == nil {
		// Repository is initialized
		return nil
	}
	// If not initialized, run 'restic init'
	_, err = instance.runRestic(ctx, "init")
	return err
}

func (instance *BackupInstance) Backup(ctx context.Context, paths []string, tags []string, excludes []string) (string, error) {
	args := []string{"backup"}
	// Set hostname if configured
	if instance.Hostname != "" {
		args = append(args, "--host", instance.Hostname)
	}
	args = append(args, paths...)
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	for _, e := range excludes {
		args = append(args, "--exclude", e)
	}
	return instance.runRestic(ctx, args...)
}

func (instance *BackupInstance) DeleteOldSnapshots(ctx context.Context, daily, weekly, monthly int) (string, error) {
	args := []string{"forget", "--prune"}
	if daily > 0 {
		args = append(args, "--keep-daily", fmt.Sprint(daily))
	}
	if weekly > 0 {
		args = append(args, "--keep-weekly", fmt.Sprint(weekly))
	}
	if monthly > 0 {
		args = append(args, "--keep-monthly", fmt.Sprint(monthly))
	}
	return instance.runRestic(ctx, args...)
}
