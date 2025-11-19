package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/polarfoxDev/marina/internal/auth"
	"github.com/polarfoxDev/marina/internal/config"
	"github.com/polarfoxDev/marina/internal/database"
	"github.com/polarfoxDev/marina/internal/logging"
	"github.com/polarfoxDev/marina/internal/peer"
	"github.com/polarfoxDev/marina/internal/version"
)

// API server to expose job status information and serve frontend
func main() {
	versionFlag := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("marina api version %s\n", version.Version)
		os.Exit(0)
	}

	// Load configuration to get peer federation settings
	cfg, err := config.Load(envDefault("CONFIG_FILE", "/config.yml"))
	if err != nil {
		log.Printf("Warning: could not load config: %v", err)
		cfg = &config.Config{} // Use empty config if not available
	}

	// Determine node name from config (top-level field)
	nodeName := cfg.NodeName
	if nodeName == "" {
		hn, err := os.Hostname()
		if err != nil {
			log.Printf("Warning: failed to get hostname: %v", err)
			hn = "unknown"
		}
		nodeName = hn
	}
	log.Printf("Node name: %s", nodeName)

	// Initialize authentication from config (top-level field)
	authPassword := cfg.AuthPassword
	authHandler := auth.New(authPassword)
	if authHandler.IsEnabled() {
		log.Printf("Authentication enabled")
	}

	// Initialize peer federation client if peers are configured
	// Pass the password so client can authenticate with peers
	var peerClient *peer.Client
	if len(cfg.Peers) > 0 {
		peerClient = peer.NewClient(cfg.Peers, authPassword)
		log.Printf("Peer federation enabled with %d peer(s)", len(cfg.Peers))
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
		"http://127.0.0.1:3000",
		"http://127.0.0.1:5173",
	}

	// Add additional origins from config
	if len(cfg.CorsOrigins) > 0 {
		for _, origin := range cfg.CorsOrigins {
			origin = strings.TrimSpace(origin)
			// Validate that it's a valid URL
			if _, err := url.Parse(origin); err == nil && origin != "" {
				corsOrigins = append(corsOrigins, origin)
			}
		}
	}

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   corsOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Public routes (no auth required)
	r.Group(func(r chi.Router) {
		r.Post("/api/auth/login", handleLogin(authHandler))
		r.Post("/api/auth/logout", handleLogout(authHandler))
		r.Get("/api/auth/check", handleAuthCheck(authHandler))
	})

	// Protected API routes (auth required if enabled)
	r.Group(func(r chi.Router) {
		// Apply auth middleware only if auth is enabled
		if authHandler.IsEnabled() {
			r.Use(authHandler.Middleware)
		}

		r.Route("/api", func(r chi.Router) {
			r.Get("/health", handleHealth())
			r.Get("/info", handleInfo(nodeName))

			r.Route("/status", func(r chi.Router) {
				r.Get("/{instanceID}", handleGetJobStatus(db, peerClient, nodeName))
			})

			r.Route("/schedules", func(r chi.Router) {
				r.Get("/", handleGetSchedules(db, peerClient, nodeName))
			})

			r.Route("/logs", func(r chi.Router) {
				r.Get("/job/{id}", handleGetJobLogs(logger, peerClient))
				r.Get("/system", handleGetSystemLogs(logger, peerClient, nodeName))
			})
		})
	})

	// Serve static files for React app (no auth required - login page needs to be accessible)
	staticDir := envDefault("STATIC_DIR", "/app/web")
	log.Printf("Serving static files from %s", staticDir)
	fileServer(r, "/", http.Dir(staticDir))

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

// Info endpoint - returns node information
func handleInfo(nodeName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, map[string]interface{}{
			"nodeName": nodeName,
			"version":  version.Version,
		})
	}
}

// GET /api/schedules - Get all backup schedules (local + mesh peers)
func handleGetSchedules(db *database.DB, peerClient *peer.Client, nodeName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()

		// Check if this is a request from another Marina mesh node (prevent recursion)
		// Marina mesh clients set X-Marina-Mesh header
		if r.Header.Get("X-Marina-Mesh") == "true" {
			// This is a mesh peer requesting data - only return local data
			schedules, err := db.GetAllSchedules(ctx)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to get schedules: %v", err), http.StatusInternalServerError)
				return
			}

			// Add node name to local schedules
			for _, schedule := range schedules {
				schedule.NodeName = nodeName
			}

			respondJSON(w, schedules)
			return
		}

		// This is a regular user request - return local + mesh data
		// Fetch local schedules
		schedules, err := db.GetAllSchedules(ctx)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get schedules: %v", err), http.StatusInternalServerError)
			return
		}

		// Add node name to local schedules
		for _, schedule := range schedules {
			schedule.NodeName = nodeName
		}

		// Fetch schedules from mesh peers if configured
		if peerClient != nil {
			peerResults := peerClient.FetchAllSchedules(ctx)
			for _, peerResult := range peerResults {
				if peerResult.Error != nil {
					// Don't log backoff errors as warnings - they're expected
					errMsg := peerResult.Error.Error()
					if !strings.Contains(errMsg, "peer in backoff") {
						log.Printf("Warning: failed to fetch schedules from peer %s: %v", peerResult.NodeURL, peerResult.Error)
					}
					continue
				}
				// Add peer schedules with their node name
				for _, peerSchedule := range peerResult.Schedules {
					peerSchedule.NodeName = peerResult.NodeName
					schedules = append(schedules, peerSchedule)
				}
			}
		}

		respondJSON(w, schedules)
	}
}

// GET /api/status/{instanceID} - Get statuses for a specific instance (local + mesh peers)
func handleGetJobStatus(db *database.DB, peerClient *peer.Client, nodeName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instanceID := chi.URLParam(r, "instanceID")
		if instanceID == "" {
			http.Error(w, "Instance ID required", http.StatusBadRequest)
			return
		}

		ctx := context.Background()

		// Check if this is a request from another Marina mesh node (prevent recursion)
		if r.Header.Get("X-Marina-Mesh") == "true" {
			// This is a mesh peer requesting data - only return local data
			statuses, err := db.GetJobStatus(ctx, instanceID)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to get statuses: %v", err), http.StatusInternalServerError)
				return
			}

			// Add node information to local statuses
			for i := range statuses {
				statuses[i].NodeName = nodeName
				statuses[i].NodeURL = "" // Empty for local node
			}

			respondJSON(w, statuses)
			return
		}

		// This is a regular user request - return local + mesh data
		// Fetch local statuses
		statuses, err := db.GetJobStatus(ctx, instanceID)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get statuses: %v", err), http.StatusInternalServerError)
			return
		}

		// Add node information to local statuses
		for i := range statuses {
			statuses[i].NodeName = nodeName
			statuses[i].NodeURL = "" // Empty for local node
		}

		// Fetch statuses from mesh peers if configured
		if peerClient != nil {
			peerResults := peerClient.FetchJobStatusFromPeers(ctx, instanceID)
			for _, peerResult := range peerResults {
				if peerResult.Error != nil {
					// Don't log backoff errors as warnings - they're expected
					errMsg := peerResult.Error.Error()
					if !strings.Contains(errMsg, "peer in backoff") {
						log.Printf("Warning: failed to fetch job statuses from peer %s: %v", peerResult.NodeURL, peerResult.Error)
					}
					continue
				}
				// Add peer statuses with their node information
				for _, peerStatus := range peerResult.Statuses {
					peerStatus.NodeName = peerResult.NodeName
					peerStatus.NodeURL = peerResult.NodeURL
					statuses = append(statuses, peerStatus)
				}
			}
		}

		respondJSON(w, statuses)
	}
}

// GET /api/logs/job/{id} - Get logs for a specific job status ID
// Supports fetching logs from remote nodes via query parameter nodeUrl
func handleGetJobLogs(logger *logging.Logger, peerClient *peer.Client) http.HandlerFunc {
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

		// Check if this is a request for remote node logs
		nodeURL := r.URL.Query().Get("nodeUrl")
		if nodeURL != "" && peerClient != nil {
			// Fetch logs from remote node
			ctx := context.Background()
			peerLogs := peerClient.FetchJobLogs(ctx, nodeURL, jobID, limit)
			if peerLogs.Error != nil {
				http.Error(w, fmt.Sprintf("Failed to fetch logs from peer: %v", peerLogs.Error), http.StatusInternalServerError)
				return
			}
			respondJSON(w, peerLogs.Logs)
			return
		}

		// Fetch local logs
		logs, err := logger.QueryByJobID(jobID, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get logs: %v", err), http.StatusInternalServerError)
			return
		}

		respondJSON(w, logs)
	}
}

// SystemLogEntryWithNode wraps a log entry with node information for mesh mode
type SystemLogEntryWithNode struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	NodeName  string    `json:"nodeName"`
}

// GET /api/logs/system - Get system logs (logs without job_status_id) from local + mesh peers
func handleGetSystemLogs(logger *logging.Logger, peerClient *peer.Client, nodeName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()

		// Get limit from query parameter (default: 1000, max: 5000)
		limitStr := r.URL.Query().Get("limit")
		limit := 1000
		if limitStr != "" {
			if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
				limit = min(parsedLimit, 5000)
			}
		}

		// Get level filter from query parameter (optional)
		levelStr := r.URL.Query().Get("level")
		var level logging.LogLevel
		if levelStr != "" {
			level = logging.LogLevel(levelStr)
		}

		// Check if this is a request from another Marina mesh node (prevent recursion)
		if r.Header.Get("X-Marina-Mesh") == "true" {
			// This is a mesh peer requesting data - only return local data
			logs, err := logger.QuerySystemLogs(level, limit)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to get system logs: %v", err), http.StatusInternalServerError)
				return
			}
			respondJSON(w, logs)
			return
		}

		// This is a regular user request - return local + mesh data with node info
		logs, err := logger.QuerySystemLogs(level, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get system logs: %v", err), http.StatusInternalServerError)
			return
		}

		// Convert to SystemLogEntryWithNode format and add local node name
		result := make([]SystemLogEntryWithNode, 0, len(logs))
		for _, log := range logs {
			result = append(result, SystemLogEntryWithNode{
				ID:        nodeName + ":" + fmt.Sprint(log.ID), // Prefix ID with node name to ensure uniqueness
				Timestamp: log.Timestamp,
				Level:     string(log.Level),
				Message:   log.Message,
				NodeName:  nodeName,
			})
		}

		// Fetch system logs from mesh peers if configured
		if peerClient != nil {
			peerResults := peerClient.FetchAllSystemLogs(ctx, levelStr, limit)
			for _, peerResult := range peerResults {
				if peerResult.Error != nil {
					// Don't log backoff errors as warnings - they're expected
					errMsg := peerResult.Error.Error()
					if !strings.Contains(errMsg, "peer in backoff") {
						log.Printf("Warning: failed to fetch system logs from peer %s: %v", peerResult.NodeURL, peerResult.Error)
					}
					continue
				}
				// Add peer logs with their node name
				for _, peerLog := range peerResult.Logs {
					// Parse timestamp string to time.Time
					ts, err := time.Parse(time.RFC3339, peerLog.Timestamp)
					if err != nil {
						// Fallback to current time if parsing fails
						ts = time.Now()
					}
					result = append(result, SystemLogEntryWithNode{
						ID:        peerResult.NodeName + ":" + fmt.Sprint(peerLog.ID), // Prefix ID with node name
						Timestamp: ts,
						Level:     peerLog.Level,
						Message:   peerLog.Message,
						NodeName:  peerResult.NodeName,
					})
				}
			}
		}

		// sort result by timestamp descending
		sort.Slice(result, func(i, j int) bool {
			return result[i].Timestamp.After(result[j].Timestamp)
		})

		// Trim to limit
		if len(result) > limit {
			result = result[:limit]
		}

		respondJSON(w, result)
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

// POST /api/auth/login - Login endpoint
func handleLogin(authHandler *auth.Auth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// If auth is not enabled, always succeed
		if !authHandler.IsEnabled() {
			respondJSON(w, map[string]interface{}{
				"success": true,
				"message": "Authentication not required",
			})
			return
		}

		var req struct {
			Password string `json:"password"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if !authHandler.ValidatePassword(req.Password) {
			http.Error(w, "Invalid password", http.StatusUnauthorized)
			return
		}

		// Generate token
		token, err := authHandler.GenerateToken()
		if err != nil {
			http.Error(w, "Failed to generate token", http.StatusInternalServerError)
			return
		}

		// Set cookie
		http.SetCookie(w, &http.Cookie{
			Name:     auth.CookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(auth.TokenExpiry.Seconds()),
		})

		respondJSON(w, map[string]interface{}{
			"success": true,
			"token":   token,
		})
	}
}

// POST /api/auth/logout - Logout endpoint
func handleLogout(authHandler *auth.Auth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := authHandler.GetTokenFromRequest(r)
		if token != "" {
			authHandler.InvalidateToken(token)
		}

		// Clear cookie
		http.SetCookie(w, &http.Cookie{
			Name:     auth.CookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
		})

		respondJSON(w, map[string]interface{}{
			"success": true,
		})
	}
}

// GET /api/auth/check - Check if authentication is required and if user is authenticated
func handleAuthCheck(authHandler *auth.Auth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"authRequired":  authHandler.IsEnabled(),
			"authenticated": false,
		}

		if authHandler.IsEnabled() {
			token := authHandler.GetTokenFromRequest(r)
			if token != "" && authHandler.ValidateToken(token) {
				response["authenticated"] = true
			}
		} else {
			// If auth is not required, consider user authenticated
			response["authenticated"] = true
		}

		respondJSON(w, response)
	}
}
