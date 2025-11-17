package docker

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/polarfoxDev/marina/internal/logging"
)

func ExecInContainer(ctx context.Context, cli *client.Client, containerID string, cmd []string) (string, error) {
	options := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	execIDResp, err := cli.ContainerExecCreate(ctx, containerID, options)
	if err != nil {
		return "", err
	}

	resp, err := cli.ContainerExecAttach(ctx, execIDResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer resp.Close()

	outputBuilder := &strings.Builder{}
	buf := make([]byte, 1024)
	for {
		n, err := resp.Reader.Read(buf)
		if n > 0 {
			outputBuilder.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	return outputBuilder.String(), nil
}

func CopyFileFromContainer(ctx context.Context, cli *client.Client, containerID, pathInContainer, hostDir string, onProgress func(expected, written int64)) (string, error) {
	reader, stat, err := cli.CopyFromContainer(ctx, containerID, pathInContainer)
	if err != nil {
		return "", fmt.Errorf("copy dump: %w", err)
	}
	defer reader.Close()

	tr := tar.NewReader(reader)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read dump stream: %w", err)
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		targetName := filepath.Base(hdr.Name)
		outPath := filepath.Join(hostDir, targetName)
		fh, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
		if err != nil {
			return "", fmt.Errorf("create dump file: %w", err)
		}
		written, copyErr := io.Copy(fh, tr)
		closeErr := fh.Close()
		if copyErr != nil {
			return "", fmt.Errorf("write dump: %w", copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close dump: %w", closeErr)
		}
		if onProgress != nil {
			onProgress(stat.Size, written)
		}
		return outPath, nil
	}
	return "", fmt.Errorf("copy dump: file not found in archive")
}

func StopContainer(ctx context.Context, cli *client.Client, containerID string) error {
	timeout := 10 // seconds
	return cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})
}

func StartContainer(ctx context.Context, cli *client.Client, containerID string) error {
	return cli.ContainerStart(ctx, containerID, container.StartOptions{})
}

func IsContainerRunning(ctx context.Context, cli *client.Client, containerID string) (bool, error) {
	ctrJSON, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, err
	}
	return ctrJSON.State.Running, nil
}

// CopyVolumeToStaging starts a temporary container with the specified volume mounted read-only,
// copies the data from the specified paths within the volume to a staging directory.
// The staging directory must be mounted at /backup as a host bind mount.
// hostBackupPath is the actual path on the host that /backup is mounted from.
// Returns the paths to the staged data.
// NOTE: Caller is responsible for cleaning up the staging directory after backup completes.
func CopyVolumeToStaging(ctx context.Context, cli *client.Client, hostBackupPath, instanceID, timestamp, volumeName string, paths []string, logger *logging.JobLogger) ([]string, error) {
	// Create a unique subdirectory in staging for this volume backup
	stagingSubdir := fmt.Sprintf("%s/%s/volume/%s", instanceID, timestamp, volumeName)
	stagingPath := filepath.Join("/backup", stagingSubdir)

	// Ensure staging directory exists in Marina's filesystem
	if err := os.MkdirAll(stagingPath, 0755); err != nil {
		return nil, fmt.Errorf("create staging dir: %w", err)
	}

	// Start temporary alpine container with both volumes mounted
	config := &container.Config{
		Image: "alpine:3.20",
		Cmd:   []string{"sh", "-c", "sleep 300"}, // Keep container alive
	}

	// ensure config.Image is available locally
	_, inspectErr := cli.ImageInspect(ctx, config.Image)
	if inspectErr != nil {
		rc, err := cli.ImagePull(ctx, config.Image, image.PullOptions{})
		if err != nil {
			return nil, fmt.Errorf("pull alpine image: %w", err)
		}
		defer rc.Close()
		if _, err := io.Copy(io.Discard, rc); err != nil {
			return nil, fmt.Errorf("read image pull response: %w", err)
		}
	}

	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:     mount.TypeVolume,
				Source:   volumeName,
				Target:   "/source",
				ReadOnly: true,
			},
			{
				Type:   mount.TypeBind,
				Source: hostBackupPath,
				Target: "/backup",
			},
		},
		AutoRemove: true,
	}

	containerName := fmt.Sprintf("marina-copy-%d", time.Now().UnixNano())
	resp, err := cli.ContainerCreate(ctx, config, hostConfig, nil, nil, containerName)
	if err != nil {
		return nil, fmt.Errorf("create copy container: %w", err)
	}
	containerID := resp.ID
	logger.Debug("started copy container %s for volume %s", containerName, volumeName)

	// Ensure cleanup even if something goes wrong
	defer func() {
		timeout := 2
		_ = cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})
	}()

	if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("start copy container: %w", err)
	}

	// Copy each path from the volume to the staging directory
	stagedPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		// Normalize path (remove leading slash if present)
		cleanPath := strings.TrimPrefix(path, "/")
		if cleanPath == "" {
			cleanPath = "."
		}

		sourcePath := filepath.Join("/source", cleanPath)
		targetPath := filepath.Join("/backup", stagingSubdir, cleanPath)

		// Create parent directory structure in staging
		mkdirCmd := []string{"sh", "-c", fmt.Sprintf("mkdir -p $(dirname %s)", targetPath)}
		if _, err := ExecInContainer(ctx, cli, containerID, mkdirCmd); err != nil {
			return nil, fmt.Errorf("create target dir for %s: %w", path, err)
		}

		copyCommand := fmt.Sprintf("cp -a '%s/.' '%s'", sourcePath, targetPath)
		logger.Debug("executing copy command in container %s: %s", containerName, copyCommand)
		copyCmd := []string{"sh", "-c", copyCommand}
		if _, err := ExecInContainer(ctx, cli, containerID, copyCmd); err != nil {
			return nil, fmt.Errorf("copy %s: %w", path, err)
		}

		// Add the staged path (absolute path in Marina's filesystem)
		stagedPaths = append(stagedPaths, filepath.Join(stagingPath, cleanPath))
	}

	// Stop container (will be auto-removed due to AutoRemove)
	timeout := 2
	if err := cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		return nil, fmt.Errorf("stop copy container: %w", err)
	}

	return stagedPaths, nil
}

// GetBackupHostPath inspects Marina's own container to find the actual host path
// for the /backup mount. This is needed to create bind mounts in temporary containers.
func GetBackupHostPath(ctx context.Context, cli *client.Client) (string, error) {
	// Get Marina's container ID from hostname
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("get hostname: %w", err)
	}

	// Inspect Marina's container
	inspect, err := cli.ContainerInspect(ctx, hostname)
	if err != nil {
		return "", fmt.Errorf("inspect marina container: %w", err)
	}

	// Find the mount for /backup
	for _, mount := range inspect.Mounts {
		if mount.Destination == "/backup" {
			if mount.Type == "bind" {
				return mount.Source, nil
			}
			// If it's a volume, we can't use it for bind mounts in other containers
			return "", fmt.Errorf("/backup is mounted as volume %s, must be a bind mount from host", mount.Source)
		}
	}

	return "", fmt.Errorf("no mount found at /backup in marina container")
}
