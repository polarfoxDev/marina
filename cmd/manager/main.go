package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/docker/docker/client"

	"github.com/polarfoxDev/marina/internal/backend"
	"github.com/polarfoxDev/marina/internal/config"
	"github.com/polarfoxDev/marina/internal/database"
	dockerd "github.com/polarfoxDev/marina/internal/docker"
	"github.com/polarfoxDev/marina/internal/logging"
	"github.com/polarfoxDev/marina/internal/model"
	"github.com/polarfoxDev/marina/internal/runner"
	"github.com/polarfoxDev/marina/internal/version"
)

func main() {
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("marina manager version %s\n", version.Version)
		os.Exit(0)
	}

	ctx := context.Background()

	// Initialize unified database for both job status and logs
	dbPath := envDefault("DB_PATH", "/var/lib/marina/marina.db")
	db, err := database.InitDB(dbPath)
	if err != nil {
		log.Fatalf("init database: %v", err)
	}
	defer db.Close()

	// Initialize structured logger using the unified database
	logger, err := logging.New(db.GetDB(), os.Stdout)
	if err != nil {
		log.Fatalf("init logger: %v", err)
	}

	logger.Info("marina starting (version %s)...", version.Version)
	logger.Info("database initialized: %s", dbPath)

	// Cleanup any jobs that were interrupted by restart
	cleaned, err := db.CleanupInterruptedJobs(ctx)
	if err != nil {
		log.Fatalf("cleanup interrupted jobs: %v", err)
	}
	if cleaned > 0 {
		logger.Info("marked %d interrupted job(s) as aborted", cleaned)
	}

	// Load configuration from config.yml
	cfg, err := config.Load(envDefault("CONFIG_FILE", "config.yml"))
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Build map of instances from config
	instances := make(map[model.InstanceID]backend.Backend)
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		hn, err := os.Hostname()
		if err != nil {
			logger.Warn("failed to get hostname: %v", err)
			hn = "unknown"
		}
		nodeName = hn
	}
	logger.Info("using hostname %s for backups", nodeName)
	for _, dest := range cfg.Instances {
		var backendInstance backend.Backend
		var backendErr error

		if dest.CustomImage != "" {
			// Use custom Docker image backend (hostBackupPath will be set after detection)
			backendInstance, backendErr = backend.NewCustomImageBackend(dest.ID, dest.CustomImage, dest.Env, nodeName, "")
			if backendErr != nil {
				log.Fatalf("create custom image backend for %s: %v", dest.ID, backendErr)
			}
			logger.Info("loaded instance: %s -> custom image: %s", dest.ID, dest.CustomImage)
		} else {
			// Use Restic backend
			backendInstance = &backend.ResticBackend{
				ID:         dest.ID,
				Repository: dest.Repository,
				Env:        dest.Env,
				Hostname:   nodeName,
			}
			logger.Info("loaded instance: %s -> restic: %s", dest.ID, dest.Repository)
		}

		instances[model.InstanceID(dest.ID)] = backendInstance
	}

	if len(instances) == 0 {
		log.Fatal("no instances configured in config.yml")
	}

	// Initialize each instance repository (idempotent: checks snapshots first)
	for id, instance := range instances {
		if err := instance.Init(ctx); err != nil {
			log.Fatalf("init instance %s: %v", id, err)
		}
		logger.Info("instance %s initialized", id)
	}

	// Discover backup targets from Docker labels
	disc, err := dockerd.NewDiscoverer(cfg)
	if err != nil {
		log.Fatalf("docker: %v", err)
	}

	schedules, err := disc.Discover(ctx)
	if err != nil {
		log.Fatalf("discover: %v", err)
	}
	logger.Info("discovered %d backup schedules", len(schedules))
	// Create Docker client
	dcli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("docker client: %v", err)
	}

	// Detect the actual host path for /backup mount
	hostBackupPath, err := dockerd.GetBackupHostPath(ctx, dcli)
	if err != nil {
		log.Fatalf("detect host backup path: %v", err)
	}
	logger.Info("detected host backup path: %s", hostBackupPath)

	// Update custom image backends with host backup path
	for id, instance := range instances {
		if customBackend, ok := instance.(*backend.CustomImageBackend); ok {
			customBackend.HostBackupPath = hostBackupPath
			logger.Debug("updated instance %s with host backup path", id)
		}
	}

	// Create runner with all instances
	r := runner.New(
		instances,
		dcli,
		logger,
		db,
		hostBackupPath,
	)

	// Start the scheduler
	r.Start()
	logger.Info("scheduler started")

	// Initial discovery and scheduling
	schedules, err = disc.Discover(ctx)
	if err != nil {
		log.Fatalf("initial discover: %v", err)
	}
	logger.Info("discovered %d backup schedules", len(schedules))
	r.SyncBackups(schedules)

	// Function to trigger rediscovery
	triggerDiscovery := func() {
		logger.Info("triggering rediscovery...")
		schedules, err := disc.Discover(ctx)
		if err != nil {
			logger.Error("rediscovery failed: %v", err)
			return
		}
		r.SyncBackups(schedules)
	}

	// Start Docker event listener for real-time updates (if enabled)
	enableEvents := envDefault("ENABLE_EVENTS", "true") == "true"
	if enableEvents {
		eventListener := dockerd.NewEventListener(dcli, triggerDiscovery, logger.Logf)
		if err := eventListener.Start(ctx); err != nil {
			logger.Error("failed to start event listener: %v", err)
		} else {
			logger.Info("docker event listener started")
		}
	}

	// Start periodic rediscovery to handle dynamic changes
	rediscoveryInterval := envDefaultDuration("DISCOVERY_INTERVAL", 30*time.Second)
	logger.Info("starting periodic discovery (interval: %v)", rediscoveryInterval)

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

	logger.Info("marina is running...")

	// Keep running until context is cancelled
	<-ctx.Done()

	// Graceful stop
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.Stop(stopCtx)
	logger.Info("scheduler stopped")
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
