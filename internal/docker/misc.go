package docker

import (
	"context"
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
