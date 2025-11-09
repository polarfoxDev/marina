package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/docker/docker/client"

	"github.com/polarfoxDev/marina/internal/backend"
	dockerd "github.com/polarfoxDev/marina/internal/docker"
	"github.com/polarfoxDev/marina/internal/runner"
)

func main() {
	ctx := context.Background()

	// repo aliases & env (pull from env/secrets in your compose)
	destination := &backend.BackupDestination{
		Env: map[string]string{
			"RESTIC_REPOSITORY":     os.Getenv("RESTIC_REPOSITORY"),
			"RESTIC_PASSWORD_FILE":  os.Getenv("RESTIC_PASSWORD_FILE"),
			"AWS_ACCESS_KEY_ID":     os.Getenv("AWS_ACCESS_KEY_ID"),
			"AWS_SECRET_ACCESS_KEY": os.Getenv("AWS_SECRET_ACCESS_KEY"),
			// S3 endpoint, region, etc if needed
		},
	}

	disc, err := dockerd.NewDiscoverer()
	if err != nil {
		log.Fatalf("docker: %v", err)
	}

	targets, err := disc.Discover(ctx)
	if err != nil {
		log.Fatalf("discover: %v", err)
	}
	log.Printf("discovered %d targets", len(targets))

	dcli, _ := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	r := runner.New(*destination, dcli,
		envDefault("VOLUME_ROOT", "/var/lib/docker/volumes"),
		envDefault("STAGING_DIR", "/backup/tmp"),
		log.Printf,
	)

	for _, t := range targets {
		if err := r.ScheduleTarget(t); err != nil {
			log.Printf("schedule %s: %v", t.ID, err)
		}
	}

	// Example: simple HTTP to trigger a job now could be added here.
	r.Start()
	log.Printf("scheduler started")
	// keep running
	<-ctx.Done()
	// graceful stop
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.Stop(stopCtx)
}

func envDefault(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}
