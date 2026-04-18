// Package device implements the OAuth 2.0 Device Authorization Grant against
// the Inlinr server (same flow as GitHub's CLI login).
package device

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type InitRequest struct {
	ClientName string `json:"client_name"`
	Editor     string `json:"editor"`
	Platform   string `json:"platform"`
}

type InitResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	Device      struct {
		ID         string `json:"id"`
		Editor     string `json:"editor"`
		ClientName string `json:"clientName"`
		Platform   string `json:"platform"`
	} `json:"device"`
	User struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
		Plan  string `json:"plan"`
	} `json:"user"`
}

var ErrPending = errors.New("authorization_pending")

// Init calls POST /api/auth/device and returns the codes needed to display to
// the user + poll on.
func Init(ctx context.Context, baseURL string, req InitRequest) (*InitResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/auth/device", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device init: HTTP %d", resp.StatusCode)
	}
	var out InitResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Poll calls POST /api/auth/device/token once. Returns ErrPending if the user
// hasn't approved yet; the caller should sleep `interval` seconds and retry.
func Poll(ctx context.Context, baseURL, deviceCode string) (*TokenResponse, error) {
	body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/auth/device/token", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsed struct {
		Error string `json:"error"`
		TokenResponse
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK && parsed.AccessToken != "" {
		return &parsed.TokenResponse, nil
	}
	if parsed.Error == "authorization_pending" {
		return nil, ErrPending
	}
	return nil, fmt.Errorf("device token: HTTP %d %s", resp.StatusCode, parsed.Error)
}

// PollUntil loops Poll until the user approves, expires, or ctx is cancelled.
func PollUntil(ctx context.Context, baseURL, deviceCode string, interval, expiresIn int) (*TokenResponse, error) {
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	for time.Now().Before(deadline) {
		tr, err := Poll(ctx, baseURL, deviceCode)
		if err == nil {
			return tr, nil
		}
		if !errors.Is(err, ErrPending) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(interval) * time.Second):
		}
	}
	return nil, errors.New("device code expired")
}
