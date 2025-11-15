package mesh

import (
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
}

// NewClient creates a new mesh client with the specified peer URLs
func NewClient(peers []string) *Client {
return &Client{
peers: peers,
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

resp, err := c.httpClient.Do(req)
if err != nil {
result.Error = fmt.Errorf("fetch schedules: %w", err)
return result
}
defer resp.Body.Close()

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

resp, err := c.httpClient.Do(req)
if err != nil {
result.Error = fmt.Errorf("fetch status: %w", err)
return result
}
defer resp.Body.Close()

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
