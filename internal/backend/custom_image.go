package backend

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

// LogWriter is an interface for logging output during backup operations
type LogWriter interface {
	Debug(format string, args ...any)
}

// CustomImageBackend implements the Backend interface using a custom Docker image
type CustomImageBackend struct {
	ID             string
	CustomImage    string
	Env            map[string]string
	Hostname       string
	HostBackupPath string
	dockerClient   *client.Client
	logger         LogWriter
}

// SetLogger sets the logger for streaming output
func (b *CustomImageBackend) SetLogger(logger LogWriter) {
	b.logger = logger
}

// NewCustomImageBackend creates a new custom image backend
func NewCustomImageBackend(id, customImage string, env map[string]string, hostname, hostBackupPath string) (*CustomImageBackend, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	return &CustomImageBackend{
		ID:             id,
		CustomImage:    customImage,
		Env:            env,
		Hostname:       hostname,
		HostBackupPath: hostBackupPath,
		dockerClient:   cli,
	}, nil
}

func (b *CustomImageBackend) GetType() BackendType {
	return BackendTypeCustomImage
}

func (b *CustomImageBackend) GetImage() string {
	return b.CustomImage
}

func (b *CustomImageBackend) GetResticTimeout() string {
	return "N/A" // Custom images don't have configurable timeouts
}

// Init initializes the backend by pulling the custom image if needed
func (b *CustomImageBackend) Init(ctx context.Context) error {
	// Always try to pull to get latest; fallback to local image if pull fails.
	rc, err := b.dockerClient.ImagePull(ctx, b.CustomImage, image.PullOptions{})
	if err != nil {
		// Check if image exists locally
		_, inspectErr := b.dockerClient.ImageInspect(ctx, b.CustomImage)
		if inspectErr != nil {
			return fmt.Errorf("pull custom image %s failed: %w (also not present locally: %v)", b.CustomImage, err, inspectErr)
		}
		// Local image found; proceed without error
		return nil
	}
	defer rc.Close()
	_, _ = io.Copy(io.Discard, rc)
	return nil
}

// Backup performs the backup by starting a container with the custom image
func (b *CustomImageBackend) Backup(ctx context.Context, paths []string, tags []string) (string, error) {
	// Build environment variables
	envVars := []string{}
	for k, v := range b.Env {
		envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
	}

	// Add metadata as environment variables
	envVars = append(envVars, fmt.Sprintf("MARINA_HOSTNAME=%s", b.Hostname))
	envVars = append(envVars, fmt.Sprintf("MARINA_INSTANCE_ID=%s", b.ID))

	// Create container configuration
	config := &container.Config{
		Image: b.CustomImage,
		Cmd:   []string{"/backup.sh"},
		Env:   envVars,
	}

	// Mount only this instance's subfolder to isolate backup data
	instanceStagingPath := fmt.Sprintf("%s/%s", b.HostBackupPath, b.ID)

	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: instanceStagingPath,
				Target: "/backup",
			},
		},
		AutoRemove: false,
	}

	// Create container with unique name
	containerName := fmt.Sprintf("marina-custom-%s-%d", b.ID, time.Now().UnixNano())
	resp, err := b.dockerClient.ContainerCreate(ctx, config, hostConfig, nil, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("create backup container: %w", err)
	}
	containerID := resp.ID

	// Ensure cleanup on error
	defer func() {
		timeout := 2
		_ = b.dockerClient.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})
	}()

	// Start streaming logs before starting the container
	logStream, err := b.dockerClient.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: false,
	})
	if err != nil {
		return "", fmt.Errorf("attach to container logs: %w", err)
	}

	// Start the container
	if err := b.dockerClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		logStream.Close()
		return "", fmt.Errorf("start backup container: %w", err)
	}

	// Stream logs in a goroutine
	logChan := make(chan string, 100)
	errChan := make(chan error, 1)
	go func() {
		defer logStream.Close()
		defer close(logChan)
		
		// Use a scanner to read logs line by line
		scanner := bufio.NewScanner(logStream)
		for scanner.Scan() {
			line := scanner.Text()
			// Docker logs may have 8-byte headers, strip them if present
			if len(line) > 8 {
				line = line[8:]
			}
			logChan <- line
		}
		if err := scanner.Err(); err != nil && err != io.EOF {
			errChan <- fmt.Errorf("read logs: %w", err)
		}
	}()

	// Stream logs to the logger as they arrive
	var allLogs []string
	logDone := false
	go func() {
		for line := range logChan {
			allLogs = append(allLogs, line)
			if b.logger != nil {
				b.logger.Debug("%s", line)
			}
		}
		logDone = true
	}()

	// Wait for container to complete
	statusCh, waitErrCh := b.dockerClient.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	var exitCode int64
	select {
	case err := <-waitErrCh:
		if err != nil {
			return "", fmt.Errorf("wait for container: %w", err)
		}
	case status := <-statusCh:
		exitCode = status.StatusCode
	}

	// Wait for log streaming to complete
	for !logDone {
		time.Sleep(50 * time.Millisecond)
	}

	// Check for any log reading errors
	select {
	case err := <-errChan:
		if err != nil {
			_ = b.dockerClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			return "", err
		}
	default:
	}

	_ = b.dockerClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})

	// Check exit code
	if exitCode != 0 {
		logsStr := ""
		for _, line := range allLogs {
			logsStr += line + "\n"
		}
		return logsStr, fmt.Errorf("backup container exited with code %d", exitCode)
	}

	// Clean up staging directory after successful backup
	if err := b.cleanupStagingDir(instanceStagingPath); err != nil {
		// Log warning but don't fail the backup
		if b.logger != nil {
			b.logger.Debug("warning: failed to cleanup staging directory: %v", err)
		}
	}

	// Return all collected logs
	logsStr := ""
	for _, line := range allLogs {
		logsStr += line + "\n"
	}
	return logsStr, nil
}

// DeleteOldSnapshots is a no-op for custom images - they handle their own retention
// The retention policy is informational only
func (b *CustomImageBackend) DeleteOldSnapshots(ctx context.Context, daily, weekly, monthly int) (string, error) {
	// Custom images are expected to handle their own retention policy
	// We don't enforce it from Marina's side
	return "", nil
}

// Close cleans up resources
func (b *CustomImageBackend) Close() error {
	if b.dockerClient != nil {
		return b.dockerClient.Close()
	}
	return nil
}

// cleanupStagingDir removes timestamp directories from the staging area
// but keeps the instance directory itself for the next backup
func (b *CustomImageBackend) cleanupStagingDir(instancePath string) error {
	entries, err := os.ReadDir(instancePath)
	if err != nil {
		return fmt.Errorf("read staging directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			// Remove timestamp directories (format: YYYYMMDD-HHMMSS)
			dirPath := filepath.Join(instancePath, entry.Name())
			if err := os.RemoveAll(dirPath); err != nil {
				return fmt.Errorf("remove staging directory %s: %w", dirPath, err)
			}
		}
	}

	return nil
}
