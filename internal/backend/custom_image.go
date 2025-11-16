package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

// CustomImageBackend implements the Backend interface using a custom Docker image
type CustomImageBackend struct {
	ID           string
	CustomImage  string
	Env          map[string]string
	Hostname     string
	dockerClient *client.Client
}

// NewCustomImageBackend creates a new custom image backend
func NewCustomImageBackend(id, customImage string, env map[string]string, hostname string) (*CustomImageBackend, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	return &CustomImageBackend{
		ID:           id,
		CustomImage:  customImage,
		Env:          env,
		Hostname:     hostname,
		dockerClient: cli,
	}, nil
}

// Init initializes the backend by pulling the custom image if needed
func (b *CustomImageBackend) Init(ctx context.Context) error {
	// Pull the image to ensure it's available
	out, err := b.dockerClient.ImagePull(ctx, b.CustomImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull custom image %s: %w", b.CustomImage, err)
	}
	defer out.Close()
	
	// Drain the response to complete the pull
	_, _ = io.Copy(io.Discard, out)
	
	return nil
}

// Backup performs the backup by starting a container with the custom image
func (b *CustomImageBackend) Backup(ctx context.Context, paths []string, tags []string, excludes []string) (string, error) {
	// Find Marina's staging volume
	stagingVolume, err := b.findStagingVolume(ctx)
	if err != nil {
		return "", fmt.Errorf("find staging volume: %w", err)
	}

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

	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: stagingVolume,
				Target: "/backup",
			},
		},
		AutoRemove: true,
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

	// Start the container
	if err := b.dockerClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start backup container: %w", err)
	}

	// Wait for container to complete
	statusCh, errCh := b.dockerClient.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return "", fmt.Errorf("wait for container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			// Get logs to show what went wrong
			logs, _ := b.getContainerLogs(ctx, containerID)
			return logs, fmt.Errorf("backup container exited with code %d", status.StatusCode)
		}
	}

	// Get container logs
	logs, err := b.getContainerLogs(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("get container logs: %w", err)
	}

	return logs, nil
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

// getContainerLogs retrieves stdout and stderr from a container
func (b *CustomImageBackend) getContainerLogs(ctx context.Context, containerID string) (string, error) {
	out, err := b.dockerClient.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return "", err
	}
	defer out.Close()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, out)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// findStagingVolume finds Marina's staging volume by inspecting Marina's own container
func (b *CustomImageBackend) findStagingVolume(ctx context.Context) (string, error) {
	// Find Marina's container ID
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("get hostname: %w", err)
	}

	// Try to inspect using hostname (which is usually the container ID)
	inspect, err := b.dockerClient.ContainerInspect(ctx, hostname)
	if err != nil {
		return "", fmt.Errorf("inspect marina container: %w", err)
	}

	// Find the volume mounted at /backup
	for _, mount := range inspect.Mounts {
		if mount.Destination == "/backup" && mount.Type == "volume" {
			return mount.Name, nil
		}
	}

	return "", fmt.Errorf("no volume mounted at /backup in marina container")
}
