package restic

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type RepoConfig struct {
	// repo alias -> full restic repo URL (e.g. "s3:default" -> "s3:https://.../bucket/prefix")
	Aliases map[string]string

	// env to pass when invoking restic (e.g. AWS creds, RESTIC_PASSWORD_FILE, RESTIC_PASSWORD)
	Env map[string]string
}

type Result struct {
	SnapshotID string
	Stdout     string
	Stderr     string
	BytesAdded int64 // optionally parse from stdout
	FilesNew   int64
}

func (c *RepoConfig) repoURL(alias string) (string, error) {
	if alias == "" {
		return "", fmt.Errorf("missing repo alias")
	}
	if url, ok := c.Aliases[alias]; ok {
		return url, nil
	}
	// allow raw restic url if not found
	if strings.Contains(alias, ":") {
		return alias, nil
	}
	return "", fmt.Errorf("unknown repo alias: %s", alias)
}

func (c *RepoConfig) runRestic(ctx context.Context, args ...string) (Result, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "restic", args...)
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		return res, fmt.Errorf("restic %v failed: %w\n%s", args, err, res.Stderr)
	}
	return res, nil
}

func (c *RepoConfig) Backup(ctx context.Context, repoAlias string, paths []string, tags []string, excludes []string) (Result, error) {
	url, err := c.repoURL(repoAlias)
	if err != nil {
		return Result{}, err
	}
	args := []string{"-r", url, "backup"}
	args = append(args, paths...)
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	for _, e := range excludes {
		args = append(args, "--exclude", e)
	}
	return c.runRestic(ctx, args...)
}

func (c *RepoConfig) ForgetPrune(ctx context.Context, repoAlias string, daily, weekly, monthly int) (Result, error) {
	url, err := c.repoURL(repoAlias)
	if err != nil {
		return Result{}, err
	}
	args := []string{"-r", url, "forget", "--prune"}
	if daily > 0 {
		args = append(args, "--keep-daily", fmt.Sprint(daily))
	}
	if weekly > 0 {
		args = append(args, "--keep-weekly", fmt.Sprint(weekly))
	}
	if monthly > 0 {
		args = append(args, "--keep-monthly", fmt.Sprint(monthly))
	}
	return c.runRestic(ctx, args...)
}
