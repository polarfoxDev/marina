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

	// Start the scheduler
	r.Start()
	log.Printf("scheduler started")

	// Initial discovery and scheduling
	targets, err = disc.Discover(ctx)
	if err != nil {
		log.Fatalf("initial discover: %v", err)
	}
	log.Printf("discovered %d targets", len(targets))
	r.SyncTargets(targets)

	// Function to trigger rediscovery
	triggerDiscovery := func() {
		log.Printf("triggering rediscovery...")
		targets, err := disc.Discover(ctx)
		if err != nil {
			log.Printf("rediscovery failed: %v", err)
			return
		}
		r.SyncTargets(targets)
	}

	// Start Docker event listener for real-time updates (if enabled)
	enableEvents := envDefault("ENABLE_EVENTS", "true") == "true"
	if enableEvents {
		eventListener := dockerd.NewEventListener(dcli, triggerDiscovery, log.Printf)
		if err := eventListener.Start(ctx); err != nil {
			log.Printf("failed to start event listener: %v", err)
		} else {
			log.Printf("docker event listener started")
		}
	}

	// Start periodic rediscovery to handle dynamic changes
	rediscoveryInterval := envDefaultDuration("DISCOVERY_INTERVAL", 30*time.Second)
	log.Printf("starting periodic discovery (interval: %v)", rediscoveryInterval)

	ticker := time.NewTicker(rediscoveryInterval)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				triggerDiscovery()
			}
		}
	}()

	log.Printf("marina is running, press Ctrl+C to stop...")

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

func envDefaultDuration(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("invalid duration for %s: %v, using default %v", k, err, def)
		return def
	}
	return d
}
