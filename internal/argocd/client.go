// Package argocd talks to an Argo CD server over its HTTP API. The package
// only exposes what cluster-tv needs: a flat list of applications with sync
// and health state.
package argocd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Application is the trimmed-down view of an Argo CD application that
// downstream code uses. We deliberately drop everything else from the
// upstream JSON — the API surface is large and we are only consuming
// a tiny stable subset.
type Application struct {
	Name   string
	Sync   string // "Synced" | "OutOfSync" | "Unknown"
	Health string // "Healthy" | "Degraded" | "Progressing" | "Suspended" | "Missing" | "Unknown"
}

// Client is an Argo CD API client. Construct with NewClient.
type Client struct {
	baseURL string
	token   string
	hc      *http.Client
}

// NewClient builds a Client. baseURL is the Argo CD server root (e.g.
// "https://argocd.example.com" or the in-cluster service URL); token is a
// bearer token from a local Argo CD account with cluster-wide
// `applications, get/list, */*, allow`. hc may be nil to use http.DefaultClient.
func NewClient(baseURL, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		hc:      hc,
	}
}

// ListApplications calls GET /api/v1/applications and returns one Application
// per item. The context controls the request timeout; ctx.Err() is wrapped
// rather than returned bare so callers can pattern-match cleanly.
func (c *Client) ListApplications(ctx context.Context) ([]Application, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/applications", nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("argocd request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("argocd: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var raw struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Sync struct {
					Status string `json:"status"`
				} `json:"sync"`
				Health struct {
					Status string `json:"status"`
				} `json:"health"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("argocd: decode body: %w", err)
	}

	apps := make([]Application, 0, len(raw.Items))
	for _, it := range raw.Items {
		apps = append(apps, Application{
			Name:   it.Metadata.Name,
			Sync:   it.Status.Sync.Status,
			Health: it.Status.Health.Status,
		})
	}
	return apps, nil
}
