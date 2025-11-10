package docker

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
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
// It automatically detects the staging volume by inspecting Marina's own container mounts.
// Returns the paths to the staged data.
// NOTE: Caller is responsible for cleaning up the staging directory after backup completes.
func CopyVolumeToStaging(ctx context.Context, cli *client.Client, instanceID, timestamp, volumeName string, paths []string) ([]string, error) {
	// Find Marina's container ID and staging volume
	marinaContainer, err := findMarinaContainer(ctx, cli)
	if err != nil {
		return nil, fmt.Errorf("find marina container: %w", err)
	}

	stagingVolume, err := findStagingVolume(ctx, cli, marinaContainer)
	if err != nil {
		return nil, fmt.Errorf("find staging volume: %w", err)
	}

	// Create a unique subdirectory in staging for this volume backup
	stagingSubdir := fmt.Sprintf("%s/%s/vol/%s", instanceID, timestamp, volumeName)
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

	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:     mount.TypeVolume,
				Source:   volumeName,
				Target:   "/source",
				ReadOnly: true,
			},
			{
				Type:   mount.TypeVolume,
				Source: stagingVolume,
				Target: "/backup",
			},
		},
		AutoRemove: true,
	}

	resp, err := cli.ContainerCreate(ctx, config, hostConfig, nil, nil, fmt.Sprintf("marina-copy-%s", timestamp))
	if err != nil {
		return nil, fmt.Errorf("create copy container: %w", err)
	}
	containerID := resp.ID

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

		// Use cp to preserve attributes and handle both files and directories
		copyCmd := []string{"sh", "-c", fmt.Sprintf("cp -a %s %s", sourcePath, targetPath)}
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

// findMarinaContainer finds the current Marina container by hostname
// In Docker, the hostname defaults to the container ID (short form)
func findMarinaContainer(ctx context.Context, cli *client.Client) (string, error) {
	// First try: use hostname (which is typically the short container ID)
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("get hostname: %w", err)
	}

	// Verify this is a valid container by trying to inspect it
	_, err = cli.ContainerInspect(ctx, hostname)
	if err == nil {
		return hostname, nil
	}

	// Fallback: try cgroup parsing (works on older Docker versions)
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("read cgroup: %w", err)
	}

	// Parse container ID from cgroup (works for cgroup v1)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.Contains(line, "docker") {
			parts := strings.Split(line, "/")
			for i, part := range parts {
				if part == "docker" && i+1 < len(parts) {
					containerID := parts[i+1]
					// Try to verify it's valid
					_, err := cli.ContainerInspect(ctx, containerID)
					if err == nil {
						return containerID, nil
					}
				}
			}
		}
		// For cgroup v2 with full path
		if strings.HasPrefix(line, "0::/") && strings.Contains(line, "docker") {
			parts := strings.Split(line, "/")
			for _, part := range parts {
				// Container IDs are 64 characters or start with docker-
				if len(part) == 64 || (len(part) > 10 && strings.HasPrefix(part, "docker-")) {
					containerID := strings.TrimPrefix(part, "docker-")
					_, err := cli.ContainerInspect(ctx, containerID)
					if err == nil {
						return containerID, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("could not find container ID (hostname: %s)", hostname)
}

// findStagingVolume inspects Marina's mounts to find the volume mounted at /backup
func findStagingVolume(ctx context.Context, cli *client.Client, containerID string) (string, error) {
	inspect, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("inspect container: %w", err)
	}

	for _, mount := range inspect.Mounts {
		if mount.Destination == "/backup" && mount.Type == "volume" {
			return mount.Name, nil
		}
	}

	return "", fmt.Errorf("no volume mounted at /backup in marina container")
}
