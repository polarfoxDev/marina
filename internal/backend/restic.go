package backend

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// ResticBackend implements the Backend interface using Restic
type ResticBackend struct {
	ID         string
	Repository string
	Env        map[string]string
	Hostname   string
	Timeout    time.Duration // Timeout for restic operations (default 5 minutes)
}

func (instance *ResticBackend) GetType() BackendType {
	return BackendTypeRestic
}

func (instance *ResticBackend) GetImage() string {
	return ""
}

func (instance *ResticBackend) GetResticTimeout() string {
	timeout := instance.Timeout
	if timeout == 0 {
		timeout = 60 * time.Minute
	}
	return timeout.String()
}

func (instance *ResticBackend) Close() error { return nil }

func (instance *ResticBackend) runRestic(ctx context.Context, args ...string) (string, error) {
	// Determine timeout (use configured timeout or default to 60 minutes)
	timeout := instance.Timeout
	if timeout == 0 {
		timeout = 60 * time.Minute
	}

	// Create a timeout context to prevent infinite hangs
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Prepend global flags to all restic commands
	fullArgs := append([]string{"--cleanup-cache"}, args...)
	cmd := exec.CommandContext(timeoutCtx, "restic", fullArgs...)
	// Set repository and cleanup-cache flag to handle corrupted cache
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+instance.Repository)
	// Add custom environment variables
	for k, v := range instance.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Use pipes to avoid buffer deadlock issues
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("create stderr pipe: %w", err)
	}
	// Open /dev/null and set it as stdin to prevent restic from trying to read input
	devNull, err := os.Open("/dev/null")
	if err != nil {
		return "", fmt.Errorf("open /dev/null: %w", err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull

	// Start the command
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start restic: %w", err)
	}

	// Read output in separate goroutines to prevent blocking
	stdoutChan := make(chan string, 1)
	stderrChan := make(chan string, 1)

	go func() {
		data, _ := io.ReadAll(stdoutPipe)
		stdoutChan <- string(data)
	}()

	go func() {
		data, _ := io.ReadAll(stderrPipe)
		stderrChan <- string(data)
	}()

	// Wait for command to complete
	cmdErr := cmd.Wait()

	// Collect output
	stdout := <-stdoutChan
	stderr := <-stderrChan

	if cmdErr != nil {
		return "", fmt.Errorf("restic %v failed: %w\nstderr: %s\nstdout: %s", args, cmdErr, stderr, stdout)
	}

	// Return combined output for logging
	combined := stdout
	if stderr != "" {
		combined += "\nstderr: " + stderr
	}
	return combined, nil
}

func (instance *ResticBackend) Init(ctx context.Context) error {
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

func (instance *ResticBackend) Backup(ctx context.Context, paths []string, tags []string, excludes []string) (string, error) {
	// First, unlock the repository to clear any stale locks from previous runs
	// This is safe - it only removes locks from processes that no longer exist
	_, unlockErr := instance.runRestic(ctx, "unlock")
	if unlockErr != nil {
		// Log but don't fail - unlock might error if there are no locks to remove
		// The actual backup will fail if there's a real locking issue
	}

	args := []string{"backup", "--verbose"}
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

func (instance *ResticBackend) DeleteOldSnapshots(ctx context.Context, daily, weekly, monthly int) (string, error) {
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
