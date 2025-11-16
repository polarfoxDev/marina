package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/polarfoxDev/marina/internal/model"
)

// Client handles fetching data from peer Marina instances
type Client struct {
	peers      []string
	httpClient *http.Client
	timeout    time.Duration
	password   string // Password for mesh authentication

	// Per-peer token cache with mutex for thread-safe access
	tokensMu sync.RWMutex
	tokens   map[string]string // peerURL -> token

	// Circuit breaker: track failed peers to avoid repeated timeouts
	failuresMu   sync.RWMutex
	failures     map[string]int       // peerURL -> consecutive failure count
	backoffUntil map[string]time.Time // peerURL -> time to retry
	inFlight     map[string]bool      // peerURL -> whether a request is currently in flight
}

// NewClient creates a new mesh client with the specified peer URLs and auth password
func NewClient(peers []string, password string) *Client {
	client := &Client{
		peers:        peers,
		password:     password,
		tokens:       make(map[string]string),
		failures:     make(map[string]int),
		backoffUntil: make(map[string]time.Time),
		inFlight:     make(map[string]bool),
		httpClient: &http.Client{
			Timeout: 15 * time.Second, // Increased for reliability
		},
		timeout: 8 * time.Second, // Increased to allow time for auth + request
	}

	// Pre-authenticate with all peers if password is set
	// This avoids blocking the first request
	if password != "" {
		for _, peer := range peers {
			go client.getTokenForPeer(peer)
		}
	}

	return client
}

// PeerSchedules represents schedules from a specific peer node
type PeerSchedules struct {
	NodeURL   string
	NodeName  string
	Schedules []*model.InstanceBackupScheduleView
	Error     error
}

// FetchAllSchedules fetches schedules from all peer nodes concurrently
func (c *Client) FetchAllSchedules(ctx context.Context) []PeerSchedules {
	if len(c.peers) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	results := make([]PeerSchedules, len(c.peers))

	for i, peer := range c.peers {
		wg.Add(1)
		go func(idx int, peerURL string) {
			defer wg.Done()

			// Check if peer is in backoff period or already has a request in flight
			c.failuresMu.RLock()
			backoffUntil, inBackoff := c.backoffUntil[peerURL]
			isInFlight := c.inFlight[peerURL]
			c.failuresMu.RUnlock()

			// Skip if already in backoff
			if inBackoff && time.Now().Before(backoffUntil) {
				// Skip this peer - it's in backoff
				results[idx] = PeerSchedules{
					NodeURL: peerURL,
					Error:   fmt.Errorf("peer in backoff until %s", backoffUntil.Format("15:04:05")),
				}
				return
			}

			// Skip if a request is already in flight - prevents request stampede
			if isInFlight {
				results[idx] = PeerSchedules{
					NodeURL: peerURL,
					Error:   fmt.Errorf("request already in flight to peer"),
				}
				return
			}

			// Atomically check and set in-flight flag under write lock (double-checked locking)
			c.failuresMu.Lock()
			if c.inFlight[peerURL] {
				// Another goroutine set it between our read and write lock
				c.failuresMu.Unlock()
				results[idx] = PeerSchedules{
					NodeURL: peerURL,
					Error:   fmt.Errorf("request already in flight to peer"),
				}
				return
			}
			c.inFlight[peerURL] = true
			c.failuresMu.Unlock()

			// Ensure we clear the in-flight flag when done
			defer func() {
				c.failuresMu.Lock()
				delete(c.inFlight, peerURL)
				c.failuresMu.Unlock()
			}()

			result := c.fetchSchedulesFromPeer(ctx, peerURL)

			// Update failure tracking
			if result.Error != nil {
				c.recordFailure(peerURL)
			} else {
				c.recordSuccess(peerURL)
			}

			results[idx] = result
		}(i, peer)
	}

	wg.Wait()
	return results
}

// fetchSchedulesFromPeer fetches schedules from a single peer
func (c *Client) fetchSchedulesFromPeer(ctx context.Context, peerURL string) PeerSchedules {
	result := PeerSchedules{
		NodeURL: peerURL,
	}

	// Create request with context timeout
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/schedules/", peerURL)
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		result.Error = fmt.Errorf("create request: %w", err)
		return result
	}
	// Mark this as a mesh request to prevent recursion
	req.Header.Set("X-Marina-Mesh", "true")
	c.addAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		result.Error = fmt.Errorf("fetch schedules: %w", err)
		return result
	}
	defer resp.Body.Close()

	// If we get 401, the token might be expired - clear it and retry once
	if resp.StatusCode == http.StatusUnauthorized && c.password != "" {
		resp.Body.Close() // Close the first response

		// Clear the cached token for this peer
		baseURL := req.URL.Scheme + "://" + req.URL.Host
		c.tokensMu.Lock()
		delete(c.tokens, baseURL)
		c.tokensMu.Unlock()

		// Create a new request with fresh context
		reqCtx2, cancel2 := context.WithTimeout(ctx, c.timeout)
		defer cancel2()

		req2, err := http.NewRequestWithContext(reqCtx2, "GET", url, nil)
		if err != nil {
			result.Error = fmt.Errorf("create retry request: %w", err)
			return result
		}
		// Mark this as a mesh request to prevent recursion
		req2.Header.Set("X-Marina-Mesh", "true")
		c.addAuthHeader(req2) // This will get a fresh token

		resp, err = c.httpClient.Do(req2)
		if err != nil {
			result.Error = fmt.Errorf("fetch schedules (retry): %w", err)
			return result
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Errorf("unexpected status: %d", resp.StatusCode)
		return result
	}

	var schedules []*model.InstanceBackupScheduleView
	if err := json.NewDecoder(resp.Body).Decode(&schedules); err != nil {
		result.Error = fmt.Errorf("decode response: %w", err)
		return result
	}

	result.Schedules = schedules

	// Try to fetch node name from health endpoint
	result.NodeName = c.fetchNodeName(ctx, peerURL)
	if result.NodeName == "" {
		result.NodeName = peerURL // Fallback to URL if can't get name
	}

	return result
}

// fetchNodeName attempts to get the node name from the peer's health/info endpoint
func (c *Client) fetchNodeName(ctx context.Context, peerURL string) string {
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/api/info", peerURL)
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		return ""
	}

	c.addAuthHeader(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var info struct {
		NodeName string `json:"nodeName"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return ""
	}

	return info.NodeName
}

// PeerJobStatuses represents job statuses from a specific peer node
type PeerJobStatuses struct {
	NodeURL    string
	NodeName   string
	InstanceID string
	Statuses   []*model.JobStatus
	Error      error
}

// FetchJobStatusFromPeers fetches job statuses for a specific instance from all peers
func (c *Client) FetchJobStatusFromPeers(ctx context.Context, instanceID string) []PeerJobStatuses {
	if len(c.peers) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	results := make([]PeerJobStatuses, len(c.peers))

	for i, peer := range c.peers {
		wg.Add(1)
		go func(idx int, peerURL string) {
			defer wg.Done()

			// Check if peer is in backoff period or already has a request in flight
			c.failuresMu.RLock()
			backoffUntil, inBackoff := c.backoffUntil[peerURL]
			isInFlight := c.inFlight[peerURL]
			c.failuresMu.RUnlock()

			// Skip if already in backoff
			if inBackoff && time.Now().Before(backoffUntil) {
				results[idx] = PeerJobStatuses{
					NodeURL:    peerURL,
					InstanceID: instanceID,
					Error:      fmt.Errorf("peer in backoff until %s", backoffUntil.Format("15:04:05")),
				}
				return
			}

			// Skip if a request is already in flight - prevents request stampede
			if isInFlight {
				results[idx] = PeerJobStatuses{
					NodeURL:    peerURL,
					InstanceID: instanceID,
					Error:      fmt.Errorf("request already in flight to peer"),
				}
				return
			}

			// Atomically check and set in-flight flag under write lock (double-checked locking)
			c.failuresMu.Lock()
			if c.inFlight[peerURL] {
				// Another goroutine set it between our read and write lock
				c.failuresMu.Unlock()
				results[idx] = PeerJobStatuses{
					NodeURL:    peerURL,
					InstanceID: instanceID,
					Error:      fmt.Errorf("request already in flight to peer"),
				}
				return
			}
			c.inFlight[peerURL] = true
			c.failuresMu.Unlock()

			// Ensure we clear the in-flight flag when done
			defer func() {
				c.failuresMu.Lock()
				delete(c.inFlight, peerURL)
				c.failuresMu.Unlock()
			}()

			result := c.fetchJobStatusFromPeer(ctx, peerURL, instanceID)

			// Update failure tracking
			if result.Error != nil {
				c.recordFailure(peerURL)
			} else {
				c.recordSuccess(peerURL)
			}

			results[idx] = result
		}(i, peer)
	}

	wg.Wait()
	return results
}

// fetchJobStatusFromPeer fetches job statuses from a single peer
func (c *Client) fetchJobStatusFromPeer(ctx context.Context, peerURL, instanceID string) PeerJobStatuses {
	result := PeerJobStatuses{
		NodeURL:    peerURL,
		InstanceID: instanceID,
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/status/%s", peerURL, instanceID)
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		result.Error = fmt.Errorf("create request: %w", err)
		return result
	}
	// Mark this as a mesh request to prevent recursion
	req.Header.Set("X-Marina-Mesh", "true")
	c.addAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		result.Error = fmt.Errorf("fetch status: %w", err)
		return result
	}
	defer resp.Body.Close()

	// If we get 401, the token might be expired - clear it and retry once
	if resp.StatusCode == http.StatusUnauthorized && c.password != "" {
		resp.Body.Close()

		baseURL := req.URL.Scheme + "://" + req.URL.Host
		c.tokensMu.Lock()
		delete(c.tokens, baseURL)
		c.tokensMu.Unlock()

		reqCtx2, cancel2 := context.WithTimeout(ctx, c.timeout)
		defer cancel2()

		req2, err := http.NewRequestWithContext(reqCtx2, "GET", url, nil)
		if err != nil {
			result.Error = fmt.Errorf("create retry request: %w", err)
			return result
		}
		// Mark this as a mesh request to prevent recursion
		req2.Header.Set("X-Marina-Mesh", "true")
		c.addAuthHeader(req2)

		resp, err = c.httpClient.Do(req2)
		if err != nil {
			result.Error = fmt.Errorf("fetch status (retry): %w", err)
			return result
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Errorf("unexpected status: %d", resp.StatusCode)
		return result
	}

	var statuses []*model.JobStatus
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		result.Error = fmt.Errorf("decode response: %w", err)
		return result
	}

	result.Statuses = statuses

	// Try to fetch node name
	result.NodeName = c.fetchNodeName(ctx, peerURL)
	if result.NodeName == "" {
		result.NodeName = peerURL
	}

	return result
}

// PeerLogs represents logs from a specific peer node
type PeerLogs struct {
	NodeURL string
	JobID   int
	Logs    []LogEntry
	Error   error
}

// LogEntry represents a log entry (mirroring the logging package structure)
type LogEntry struct {
	ID           int64  `json:"id"`
	Timestamp    string `json:"timestamp"`
	Level        string `json:"level"`
	Message      string `json:"message"`
	InstanceID   string `json:"instanceId"`
	TargetID     string `json:"targetId"`
	JobStatusID  int    `json:"jobStatusId"`
	JobStatusIID int    `json:"jobStatusIid"`
}

// FetchJobLogs fetches logs for a specific job from a peer node
func (c *Client) FetchJobLogs(ctx context.Context, peerURL string, jobID int, limit int) PeerLogs {
	result := PeerLogs{
		NodeURL: peerURL,
		JobID:   jobID,
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/logs/job/%d?limit=%d", peerURL, jobID, limit)
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		result.Error = fmt.Errorf("create request: %w", err)
		return result
	}
	c.addAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		result.Error = fmt.Errorf("fetch logs: %w", err)
		return result
	}
	defer resp.Body.Close()

	// If we get 401, the token might be expired - clear it and retry once
	if resp.StatusCode == http.StatusUnauthorized && c.password != "" {
		resp.Body.Close()

		baseURL := req.URL.Scheme + "://" + req.URL.Host
		c.tokensMu.Lock()
		delete(c.tokens, baseURL)
		c.tokensMu.Unlock()

		reqCtx2, cancel2 := context.WithTimeout(ctx, c.timeout)
		defer cancel2()

		req2, err := http.NewRequestWithContext(reqCtx2, "GET", url, nil)
		if err != nil {
			result.Error = fmt.Errorf("create retry request: %w", err)
			return result
		}
		c.addAuthHeader(req2)

		resp, err = c.httpClient.Do(req2)
		if err != nil {
			result.Error = fmt.Errorf("fetch logs (retry): %w", err)
			return result
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Errorf("unexpected status: %d", resp.StatusCode)
		return result
	}

	var logs []LogEntry
	if err := json.NewDecoder(resp.Body).Decode(&logs); err != nil {
		result.Error = fmt.Errorf("decode response: %w", err)
		return result
	}

	result.Logs = logs
	return result
}

// PeerSystemLogs represents system logs from a specific peer node
type PeerSystemLogs struct {
	NodeURL  string
	NodeName string
	Logs     []LogEntry
	Error    error
}

// FetchAllSystemLogs fetches system logs from all peer nodes concurrently
func (c *Client) FetchAllSystemLogs(ctx context.Context, level string, limit int) []PeerSystemLogs {
	if len(c.peers) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	results := make([]PeerSystemLogs, len(c.peers))

	for i, peer := range c.peers {
		wg.Add(1)
		go func(idx int, peerURL string) {
			defer wg.Done()

			// Check if peer is in backoff period or already has a request in flight
			c.failuresMu.RLock()
			backoffUntil, inBackoff := c.backoffUntil[peerURL]
			isInFlight := c.inFlight[peerURL]
			c.failuresMu.RUnlock()

			// Skip if already in backoff
			if inBackoff && time.Now().Before(backoffUntil) {
				results[idx] = PeerSystemLogs{
					NodeURL: peerURL,
					Error:   fmt.Errorf("peer in backoff until %s", backoffUntil.Format("15:04:05")),
				}
				return
			}

			// Skip if a request is already in flight
			if isInFlight {
				results[idx] = PeerSystemLogs{
					NodeURL: peerURL,
					Error:   fmt.Errorf("request already in flight to peer"),
				}
				return
			}

			// Atomically check and set in-flight flag under write lock (double-checked locking)
			c.failuresMu.Lock()
			if c.inFlight[peerURL] {
				c.failuresMu.Unlock()
				results[idx] = PeerSystemLogs{
					NodeURL: peerURL,
					Error:   fmt.Errorf("request already in flight to peer"),
				}
				return
			}
			c.inFlight[peerURL] = true
			c.failuresMu.Unlock()

			// Ensure we clear the in-flight flag when done
			defer func() {
				c.failuresMu.Lock()
				delete(c.inFlight, peerURL)
				c.failuresMu.Unlock()
			}()

			result := c.fetchSystemLogsFromPeer(ctx, peerURL, level, limit)

			// Update failure tracking
			if result.Error != nil {
				c.recordFailure(peerURL)
			} else {
				c.recordSuccess(peerURL)
			}

			results[idx] = result
		}(i, peer)
	}

	wg.Wait()
	return results
}

// fetchSystemLogsFromPeer fetches system logs from a single peer
func (c *Client) fetchSystemLogsFromPeer(ctx context.Context, peerURL string, level string, limit int) PeerSystemLogs {
	result := PeerSystemLogs{
		NodeURL: peerURL,
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	url := fmt.Sprintf("%s/api/logs/system?limit=%d", peerURL, limit)
	if level != "" {
		url += "&level=" + level
	}
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		result.Error = fmt.Errorf("create request: %w", err)
		return result
	}
	// Mark this as a mesh request to prevent recursion
	req.Header.Set("X-Marina-Mesh", "true")
	c.addAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		result.Error = fmt.Errorf("fetch system logs: %w", err)
		return result
	}
	defer resp.Body.Close()

	// If we get 401, the token might be expired - clear it and retry once
	if resp.StatusCode == http.StatusUnauthorized && c.password != "" {
		resp.Body.Close()

		baseURL := req.URL.Scheme + "://" + req.URL.Host
		c.tokensMu.Lock()
		delete(c.tokens, baseURL)
		c.tokensMu.Unlock()

		reqCtx2, cancel2 := context.WithTimeout(ctx, c.timeout)
		defer cancel2()

		req2, err := http.NewRequestWithContext(reqCtx2, "GET", url, nil)
		if err != nil {
			result.Error = fmt.Errorf("create retry request: %w", err)
			return result
		}
		// Mark this as a mesh request to prevent recursion
		req2.Header.Set("X-Marina-Mesh", "true")
		c.addAuthHeader(req2)

		resp, err = c.httpClient.Do(req2)
		if err != nil {
			result.Error = fmt.Errorf("fetch system logs (retry): %w", err)
			return result
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Errorf("unexpected status: %d", resp.StatusCode)
		return result
	}

	var logs []LogEntry
	if err := json.NewDecoder(resp.Body).Decode(&logs); err != nil {
		result.Error = fmt.Errorf("decode response: %w", err)
		return result
	}

	result.Logs = logs

	// Try to fetch node name
	result.NodeName = c.fetchNodeName(ctx, peerURL)
	if result.NodeName == "" {
		result.NodeName = peerURL
	}

	return result
}

// addAuthHeader adds authentication header to the request
func (c *Client) addAuthHeader(req *http.Request) {
	if c.password == "" {
		return // No auth configured
	}

	// Extract the base URL from the request
	baseURL := req.URL.Scheme + "://" + req.URL.Host

	// Check if we have a cached token for this peer
	c.tokensMu.RLock()
	token, exists := c.tokens[baseURL]
	c.tokensMu.RUnlock()

	if exists && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		return
	}

	// Need to get a token - do this outside the request context to avoid timeout
	// This is done synchronously but only once per peer (or when token expires)
	token = c.getTokenForPeer(baseURL)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// getTokenForPeer authenticates with a peer and returns a token
// This method handles its own synchronization to prevent multiple simultaneous auth attempts
func (c *Client) getTokenForPeer(peerURL string) string {
	// Double-check if another goroutine already got the token
	c.tokensMu.RLock()
	if token, exists := c.tokens[peerURL]; exists && token != "" {
		c.tokensMu.RUnlock()
		return token
	}
	c.tokensMu.RUnlock()

	// Acquire write lock to authenticate
	c.tokensMu.Lock()
	defer c.tokensMu.Unlock()

	// Check again in case another goroutine got it while we waited for the lock
	if token, exists := c.tokens[peerURL]; exists && token != "" {
		return token
	}

	// Try to login and get a token with a separate timeout
	loginURL := peerURL + "/api/auth/login"

	loginData := map[string]string{"password": c.password}
	jsonData, err := json.Marshal(loginData)
	if err != nil {
		return ""
	}

	// Create a separate context with timeout for login (not tied to request context)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	loginReq, err := http.NewRequestWithContext(ctx, "POST", loginURL, bytes.NewReader(jsonData))
	if err != nil {
		return ""
	}
	loginReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(loginReq)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}

	// Cache the token for this specific peer
	c.tokens[peerURL] = result.Token
	return result.Token
}

// recordFailure increments the failure count for a peer and applies backoff if needed
func (c *Client) recordFailure(peerURL string) {
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()

	c.failures[peerURL]++
	failCount := c.failures[peerURL]

	// Apply exponential backoff after 3 failures
	// 3 failures = 30s, 4 = 60s, 5 = 120s, 6+ = 300s
	if failCount >= 3 {
		backoffSeconds := 30
		if failCount == 4 {
			backoffSeconds = 60
		} else if failCount == 5 {
			backoffSeconds = 120
		} else if failCount >= 6 {
			backoffSeconds = 300
		}
		backoffUntil := time.Now().Add(time.Duration(backoffSeconds) * time.Second)
		c.backoffUntil[peerURL] = backoffUntil

		// Log circuit breaker activation
		fmt.Printf("Circuit breaker ACTIVATED: peer %s in backoff for %ds (failures: %d, until: %s)\n",
			peerURL, backoffSeconds, failCount, backoffUntil.Format("15:04:05"))
	} else {
		// Log failure count building up
		fmt.Printf("Circuit breaker: peer %s failure count: %d/3\n", peerURL, failCount)
	}
}

// recordSuccess resets the failure count for a peer
func (c *Client) recordSuccess(peerURL string) {
	c.failuresMu.Lock()
	defer c.failuresMu.Unlock()

	delete(c.failures, peerURL)
	delete(c.backoffUntil, peerURL)
}
