package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/docker/docker/client"

	"github.com/polarfoxDev/marina/internal/backend"
	"github.com/polarfoxDev/marina/internal/config"
	dockerd "github.com/polarfoxDev/marina/internal/docker"
	"github.com/polarfoxDev/marina/internal/runner"
)

func main() {
	ctx := context.Background()

	// Load configuration from config.yml
	cfg, err := config.Load(envDefault("CONFIG_FILE", "config.yml"))
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Build map of destinations from config
	destinations := make(map[string]*backend.BackupDestination)
	for _, dest := range cfg.Destinations {
		destinations[dest.ID] = &backend.BackupDestination{
			ID:         dest.ID,
			Repository: dest.Repository,
			Env:        dest.Env,
		}
		log.Printf("loaded destination: %s -> %s", dest.ID, dest.Repository)
	}

	if len(destinations) == 0 {
		log.Fatal("no destinations configured in config.yml")
	}

	// Initialize each destination repository (idempotent: checks snapshots first)
	for id, d := range destinations {
		if err := d.Init(ctx); err != nil {
			log.Fatalf("init destination %s: %v", id, err)
		}
		log.Printf("destination %s initialized", id)
	}

	// Discover backup targets from Docker labels
	disc, err := dockerd.NewDiscoverer()
	if err != nil {
		log.Fatalf("docker: %v", err)
	}

	targets, err := disc.Discover(ctx)
	if err != nil {
		log.Fatalf("discover: %v", err)
	}
	log.Printf("discovered %d targets", len(targets))

	// Validate that all targets reference valid destinations
	for _, t := range targets {
		if _, ok := destinations[string(t.Destination)]; !ok {
			log.Printf("WARNING: target %s references unknown destination %q, skipping", t.ID, t.Destination)
		}
	}

	// Create Docker client
	dcli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("docker client: %v", err)
	}

	// Create runner with all destinations
	r := runner.New(
		destinations,
		dcli,
		envDefault("VOLUME_ROOT", "/var/lib/docker/volumes"),
		envDefault("STAGING_DIR", "/backup/tmp"),
		log.Printf,
	)

	// Schedule all targets
	for _, t := range targets {
		if err := r.ScheduleTarget(t); err != nil {
			log.Printf("schedule %s: %v", t.ID, err)
		} else {
			log.Printf("scheduled %s (destination: %s, schedule: %s)", t.ID, t.Destination, t.Schedule)
		}
	}

	// Start the scheduler
	r.Start()
	log.Printf("scheduler started, waiting for jobs...")

	// Keep running until context is cancelled
	<-ctx.Done()

	// Graceful stop
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.Stop(stopCtx)
	log.Printf("scheduler stopped")
}

func envDefault(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}
