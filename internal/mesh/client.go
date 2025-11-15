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
}

// NewClient creates a new mesh client with the specified peer URLs and auth password
func NewClient(peers []string, password string) *Client {
	return &Client{
		peers:    peers,
		password: password,
		tokens:   make(map[string]string),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		timeout: 5 * time.Second,
	}
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
			results[idx] = c.fetchSchedulesFromPeer(ctx, peerURL)
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
			results[idx] = c.fetchJobStatusFromPeer(ctx, peerURL, instanceID)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

// doRequestWithRetry performs an HTTP request and retries once on 401 with fresh auth
func (c *Client) doRequestWithRetry(ctx context.Context, method, url string) (*http.Response, error) {
reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
defer cancel()

req, err := http.NewRequestWithContext(reqCtx, method, url, nil)
if err != nil {
return nil, fmt.Errorf("create request: %w", err)
}
c.addAuthHeader(req)

resp, err := c.httpClient.Do(req)
if err != nil {
return nil, err
}

// If we get 401 and have auth configured, retry once with fresh token
if resp.StatusCode == http.StatusUnauthorized && c.password != "" {
resp.Body.Close()

// Clear the cached token for this peer
baseURL := req.URL.Scheme + "://" + req.URL.Host
c.tokensMu.Lock()
delete(c.tokens, baseURL)
c.tokensMu.Unlock()

// Retry with fresh context
reqCtx2, cancel2 := context.WithTimeout(ctx, c.timeout)
defer cancel2()

req2, err := http.NewRequestWithContext(reqCtx2, method, url, nil)
if err != nil {
return nil, fmt.Errorf("create retry request: %w", err)
}
c.addAuthHeader(req2)

resp, err = c.httpClient.Do(req2)
if err != nil {
return nil, fmt.Errorf("retry: %w", err)
}
}

return resp, nil
}
