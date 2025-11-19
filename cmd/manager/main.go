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
	"github.com/polarfoxDev/marina/internal/scheduler"
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

	// Load configuration from config.yml
	cfg, err := config.Load(envDefault("CONFIG_FILE", "/config.yml"))
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Initialize unified database for both job status and logs
	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = "/var/lib/marina/marina.db"
	}
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

	// Determine node name from config (top-level field)
	nodeName := cfg.NodeName
	if nodeName == "" {
		hn, err := os.Hostname()
		if err != nil {
			logger.Warn("failed to get hostname: %v", err)
			hn = "unknown"
		}
		nodeName = hn
	}
	logger.Info("using node name %s for backups", nodeName)

	// Build map of instances from config
	instances := make(map[model.InstanceID]backend.Backend)
	for _, dest := range cfg.Instances {
		var backendInstance backend.Backend
		var backendErr error

		// Parse restic timeout (instance-specific or global default)
		timeoutStr := dest.ResticTimeout
		if timeoutStr == "" {
			timeoutStr = cfg.ResticTimeout
		}
		var resticTimeout time.Duration
		if timeoutStr != "" {
			resticTimeout, err = time.ParseDuration(timeoutStr)
			if err != nil {
				log.Fatalf("invalid restic timeout %q for instance %s: %v", timeoutStr, dest.ID, err)
			}
		}

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
				Timeout:    resticTimeout,
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

	// Build backup schedules from config (no discovery needed)
	schedules, err := scheduler.BuildSchedulesFromConfig(cfg)
	if err != nil {
		log.Fatalf("build schedules from config: %v", err)
	}
	logger.Info("loaded %d backup schedules from config", len(schedules))

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

	// Schedule all configured backups
	r.SyncBackups(schedules)

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
