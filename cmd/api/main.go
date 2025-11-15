package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/polarfoxDev/marina/internal/database"
	"github.com/polarfoxDev/marina/internal/logging"
)

// API server to expose job status information and serve frontend
func main() {
	// Initialize unified database for both job status and logs
	dbPath := envDefault("DB_PATH", "/var/lib/marina/marina.db")
	db, err := database.InitDB(dbPath)
	if err != nil {
		log.Fatalf("init database: %v", err)
	}
	defer db.Close()

	// Initialize logger (for reading logs via API) using the unified database
	logger, err := logging.New(db.GetDB(), nil)
	if err != nil {
		log.Fatalf("init logger: %v", err)
	}

	// Create router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// CORS configuration
	// In production behind a reverse proxy, the frontend is served from the same origin
	// so CORS isn't needed, but we allow localhost origins for development
	corsOrigins := []string{
		"http://localhost:3000",
		"http://localhost:5173",
		"http://localhost:8080",
		"http://127.0.0.1:3000",
		"http://127.0.0.1:5173",
		"http://127.0.0.1:8080",
	}

	// Allow additional origins from environment variable (comma-separated)
	// Example: CORS_ORIGINS=https://marina.example.com,https://backup.example.com
	if extraOrigins := os.Getenv("CORS_ORIGINS"); extraOrigins != "" {
		corsOrigins = append(corsOrigins, strings.Split(extraOrigins, ",")...)
	}

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   corsOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// API routes
	r.Route("/api", func(r chi.Router) {
		r.Get("/health", handleHealth())

		r.Route("/status", func(r chi.Router) {
			r.Get("/{instanceID}", handleGetJobStatus(db))
		})

		r.Route("/schedules", func(r chi.Router) {
			r.Get("/", handleGetSchedules(db))
		})

		r.Route("/logs", func(r chi.Router) {
			r.Get("/job/{id}", handleGetJobLogs(logger))
		})
	})

	// Serve static files for React app (will be added later)
	staticDir := envDefault("STATIC_DIR", "/app/web")
	indexPath := filepath.Join(staticDir, "index.html")

	// Only use file server if index.html exists
	if _, err := os.Stat(indexPath); err == nil {
		log.Printf("Serving static files from %s", staticDir)
		fileServer(r, "/", http.Dir(staticDir))
	} else {
		// Placeholder for when no frontend is built yet
		log.Printf("No frontend found at %s, serving placeholder", indexPath)
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Marina Status</title></head>
<body>
	<h1>Marina Backup Status API</h1>
	<p>API is running. Frontend will be available here once built.</p>
	<h2>Available Endpoints:</h2>
	<ul>
		<li><a href="/api/health">/api/health</a> - Health check</li>
		<li><a href="/api/schedules">/api/schedules</a> - Backup schedules</li>
		<li><a href="/api/status/{instanceID}">/api/status/{instanceID}</a> - Job statuses for an instance</li>
		<li><a href="/api/logs/job/{id}">/api/logs/job/{id}</a> - Logs for a specific job (by job status ID)</li>
	</ul>
</body>
</html>`)
		})
	}

	// Start server
	port := envDefault("API_PORT", "8080")
	addr := fmt.Sprintf(":%s", port)
	log.Printf("Starting Marina API server on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

// fileServer conveniently sets up a http.FileServer handler to serve
// static files from a http.FileSystem with SPA support (serves index.html for routes)
func fileServer(r chi.Router, path string, root http.FileSystem) {
	if strings.ContainsAny(path, "{}*") {
		panic("FileServer does not permit any URL parameters.")
	}

	if path != "/" && path[len(path)-1] != '/' {
		r.Get(path, http.RedirectHandler(path+"/", http.StatusMovedPermanently).ServeHTTP)
		path += "/"
	}
	path += "*"

	r.Get(path, func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.RouteContext(r.Context())
		pathPrefix := strings.TrimSuffix(rctx.RoutePattern(), "/*")
		fs := http.StripPrefix(pathPrefix, http.FileServer(root))

		// Try to serve the file
		filePath := filepath.Join(".", r.URL.Path)
		if _, err := root.Open(filePath); err != nil {
			// File not found, serve index.html for SPA routing
			indexFile, err := root.Open("index.html")
			if err == nil {
				indexFile.Close()
				r.URL.Path = "/"
				fs.ServeHTTP(w, r)
				return
			}
		}

		fs.ServeHTTP(w, r)
	})
}

// Health check endpoint
func handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, map[string]interface{}{
			"status": "ok",
			"time":   time.Now().UTC(),
		})
	}
}

// GET /api/schedules - Get all backup schedules
func handleGetSchedules(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()
		schedules, err := db.GetAllSchedules(ctx)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get schedules: %v", err), http.StatusInternalServerError)
			return
		}

		respondJSON(w, schedules)
	}
}

// GET /api/status/{instanceID} - Get statuses for a specific instance
func handleGetJobStatus(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := chi.URLParam(r, "instanceID")
		if instanceID == "" {
			http.Error(w, "Instance ID required", http.StatusBadRequest)
			return
		}

		ctx := context.Background()
		statuses, err := db.GetJobStatus(ctx, instanceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get statuses: %v", err), http.StatusInternalServerError)
			return
		}

		respondJSON(w, statuses)
	}
}

// GET /api/logs/job/{id} - Get logs for a specific job status ID
func handleGetJobLogs(logger *logging.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		if idStr == "" {
			http.Error(w, "Job ID required", http.StatusBadRequest)
			return
		}

		jobID, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, "Invalid job ID", http.StatusBadRequest)
			return
		}

		// Get limit from query parameter (default: 1000)
		limitStr := r.URL.Query().Get("limit")
		limit := 1000
		if limitStr != "" {
			if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
				limit = parsedLimit
			}
		}

		logs, err := logger.QueryByJobID(jobID, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get logs: %v", err), http.StatusInternalServerError)
			return
		}

		respondJSON(w, logs)
	}
}

// Helper to respond with JSON
func respondJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("Failed to encode JSON: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func envDefault(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}
