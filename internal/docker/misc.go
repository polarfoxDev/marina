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
