// Package api wraps the Inlinr HTTP ingest endpoint.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/commits"
	"github.com/inlinrhq/inlinr-cli/internal/heartbeat"
)

// Sentinel errors returned by SendHeartbeats. Callers use these to decide
// whether to discard (ErrBadRequest), stop (ErrAuth), or requeue (ErrTransient).
var (
	ErrAuth       = errors.New("unauthorized")
	ErrBadRequest = errors.New("bad request")
	ErrTransient  = errors.New("transient server error")
)

type Client struct {
	BaseURL     string
	DeviceToken string
	UserAgent   string
	HTTP        *http.Client
}

func New(baseURL, deviceToken, userAgent string) *Client {
	return &Client{
		BaseURL:     baseURL,
		DeviceToken: deviceToken,
		UserAgent:   userAgent,
		HTTP:        &http.Client{Timeout: 20 * time.Second},
	}
}

// BulkResponse mirrors the server's per-beat status array:
//
//	{ "responses": [[{"id":"hb_0"}, 201], ...], "accepted": N }
type BulkResponse struct {
	Responses [][]json.RawMessage `json:"responses"`
	Accepted  int                 `json:"accepted"`
}

// RevokeDevice POSTs /api/auth/device/revoke. The server sets the Device's
// revokedAt, invalidating the token. Returns nil on 204, else an error.
func (c *Client) RevokeDevice(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/auth/device/revoke", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.DeviceToken)
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return ErrAuth
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: HTTP %d %s", ErrTransient, resp.StatusCode, string(body))
	}
}

// SendHeartbeats POSTs a batch to /api/v1/heartbeats. Returns the parsed
// response on success, or one of the sentinel errors above.
func (c *Client) SendHeartbeats(ctx context.Context, beats []heartbeat.Heartbeat) (*BulkResponse, error) {
	body, err := json.Marshal(beats)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/heartbeats", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.DeviceToken)
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransient, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusAccepted:
		var r BulkResponse
		if err := json.Unmarshal(respBody, &r); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		return &r, nil
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrAuth
	case resp.StatusCode == http.StatusBadRequest:
		return nil, fmt.Errorf("%w: %s", ErrBadRequest, string(respBody))
	default:
		return nil, fmt.Errorf("%w: HTTP %d %s", ErrTransient, resp.StatusCode, string(respBody))
	}
}

// CommitPayload is the body of POST /api/v1/commits.
type CommitPayload struct {
	ProjectGitRemote string           `json:"project_git_remote"`
	Commits          []commits.Commit `json:"commits"`
}

// CommitResponse reports what the server did with them.
type CommitResponse struct {
	Accepted   int    `json:"accepted"`
	Backfilled int    `json:"backfilled"`
	Reason     string `json:"reason,omitempty"`
}

// SendCommits posts local git line counts.
//
// These fill in what the GitHub push webhook cannot: its payload carries file
// names, never diff stats, so webhook-created rows have zero lines until this
// arrives. Only counts are sent — never paths, never content.
func (c *Client) SendCommits(ctx context.Context, remote string, list []commits.Commit) (*CommitResponse, error) {
	body, err := json.Marshal(CommitPayload{ProjectGitRemote: remote, Commits: list})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/commits", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.DeviceToken)
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransient, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 500 {
		// Transient so the caller can retry; a 4xx is our bug or a bad token and
		// retrying it just burns requests.
		return nil, fmt.Errorf("%w: commits returned %d", ErrTransient, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("commits returned %d: %s", resp.StatusCode, string(respBody))
	}
	var out CommitResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decode commits response: %w", err)
	}
	return &out, nil
}
