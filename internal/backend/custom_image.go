package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/polarfoxDev/marina/internal/logging"
)

// lineWriter writes log lines to the logger in real-time
type lineWriter struct {
	logger  *logging.JobLogger
	allLogs *[]string
	mu      sync.Mutex
	buffer  []byte
}

func (w *lineWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buffer = append(w.buffer, p...)

	// Process complete lines
	for {
		idx := bytes.IndexByte(w.buffer, '\n')
		if idx == -1 {
			break
		}

		line := string(w.buffer[:idx])
		line = strings.TrimRight(line, "\r")

		*w.allLogs = append(*w.allLogs, line)
		if w.logger != nil {
			w.logger.Debug("%s", line)
		}

		w.buffer = w.buffer[idx+1:]
	}

	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.buffer) > 0 {
		line := string(w.buffer)
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			*w.allLogs = append(*w.allLogs, line)
			if w.logger != nil {
				w.logger.Debug("%s", line)
			}
		}
		w.buffer = nil
	}
}

// CustomImageBackend implements the Backend interface using a custom Docker image
type CustomImageBackend struct {
	ID             string
	CustomImage    string
	Env            map[string]string
	Hostname       string
	HostBackupPath string
	dockerClient   *client.Client
	logger         *logging.JobLogger
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

func (b *CustomImageBackend) SetLogger(logger *logging.JobLogger) {
	b.logger = logger
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

	// Start the container first
	if err := b.dockerClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start backup container: %w", err)
	}

	// Create shared slice for all logs
	var allLogs []string

	// Create line-by-line streaming writers
	stdoutWriter := &lineWriter{
		logger:  b.logger,
		allLogs: &allLogs,
	}
	stderrWriter := &lineWriter{
		logger:  b.logger,
		allLogs: &allLogs,
	}

	// Attach to logs after container is started
	logStream, err := b.dockerClient.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: false,
	})
	if err != nil {
		return "", fmt.Errorf("attach to container logs: %w", err)
	}

	// Demultiplex Docker logs in a goroutine
	errChan := make(chan error, 1)
	go func() {
		defer logStream.Close()
		// StdCopy demultiplexes the Docker log stream
		_, err := stdcopy.StdCopy(stdoutWriter, stderrWriter, logStream)
		if err != nil && err != io.EOF {
			errChan <- fmt.Errorf("demultiplex logs: %w", err)
		}
		close(errChan)
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

	// Wait for log streaming to complete and flush any remaining buffered data
	if err := <-errChan; err != nil {
		_ = b.dockerClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
		return "", err
	}

	// Flush any remaining buffered lines
	stdoutWriter.flush()
	stderrWriter.flush()

	_ = b.dockerClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})

	// Check exit code - only return logs on error
	if exitCode != 0 {
		logsStr := ""
		for _, line := range allLogs {
			logsStr += line + "\n"
		}
		return logsStr, fmt.Errorf("backup container exited with code %d", exitCode)
	}

	// Success - logs were already streamed in real-time, no need to return them
	return "", nil
}

// DeleteOldSnapshots is a no-op for custom images - they handle their own retention
// The retention policy is informational only
func (b *CustomImageBackend) DeleteOldSnapshots(ctx context.Context, daily, weekly, monthly int) (string, error) {
	return "", nil
}

// Close cleans up resources
func (b *CustomImageBackend) Close() error {
	if b.dockerClient != nil {
		return b.dockerClient.Close()
	}
	return nil
}
