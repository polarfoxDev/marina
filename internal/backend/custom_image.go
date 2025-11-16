package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

// CustomImageBackend implements the Backend interface using a custom Docker image
type CustomImageBackend struct {
	ID             string
	CustomImage    string
	Env            map[string]string
	Hostname       string
	HostBackupPath string
	dockerClient   *client.Client
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
func (b *CustomImageBackend) Backup(ctx context.Context, paths []string, tags []string, excludes []string) (string, error) {
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
			_ = b.dockerClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			return logs, fmt.Errorf("backup container exited with code %d", status.StatusCode)
		}
	}

	// Get container logs
	logs, err := b.getContainerLogs(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("get container logs: %w", err)
	}

	_ = b.dockerClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})

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
