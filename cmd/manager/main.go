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

	// Build map of instances from config
	instances := make(map[string]*backend.BackupInstance)
	for _, dest := range cfg.Instances {
		instances[dest.ID] = &backend.BackupInstance{
			ID:         dest.ID,
			Repository: dest.Repository,
			Env:        dest.Env,
		}
		log.Printf("loaded instance: %s -> %s", dest.ID, dest.Repository)
	}

	if len(instances) == 0 {
		log.Fatal("no instances configured in config.yml")
	}

	// Initialize each instance repository (idempotent: checks snapshots first)
	for id, instance := range instances {
		if err := instance.Init(ctx); err != nil {
			log.Fatalf("init instance %s: %v", id, err)
		}
		log.Printf("instance %s initialized", id)
	}

	// Discover backup targets from Docker labels
	disc, err := dockerd.NewDiscoverer(cfg)
	if err != nil {
		log.Fatalf("docker: %v", err)
	}

	targets, err := disc.Discover(ctx)
	if err != nil {
		log.Fatalf("discover: %v", err)
	}
	log.Printf("discovered %d targets", len(targets))

	// Validate that all targets reference valid instances
	for _, t := range targets {
		if _, ok := instances[string(t.InstanceID)]; !ok {
			log.Printf("WARNING: target %s references unknown instance %q, skipping", t.ID, t.InstanceID)
		}
	}

	// Create Docker client
	dcli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("docker client: %v", err)
	}

	// Create runner with all instances
	r := runner.New(
		instances,
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
			log.Printf("scheduled %s (id: %s, instance: %s, schedule: %s)", t.Name, t.ID, t.InstanceID, t.Schedule)
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
