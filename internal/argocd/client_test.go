package argocd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListApplications_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth header = %q", got)
		}
		if r.URL.Path != "/api/v1/applications" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"items": [
				{"metadata":{"name":"foo"},"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}},
				{"metadata":{"name":"bar"},"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Degraded"}}},
				{"metadata":{"name":"baz"},"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}
			]
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-token", srv.Client())
	apps, err := c.ListApplications(context.Background())
	if err != nil {
		t.Fatalf("ListApplications: %v", err)
	}
	if len(apps) != 3 {
		t.Fatalf("len(apps) = %d, want 3", len(apps))
	}
	if apps[1].Name != "bar" || apps[1].Sync != "OutOfSync" || apps[1].Health != "Degraded" {
		t.Errorf("apps[1] = %+v", apps[1])
	}
}

func TestListApplications_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "bad", srv.Client())
	_, err := c.ListApplications(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want to contain 401", err)
	}
}

func TestListApplications_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "x", srv.Client())
	_, err := c.ListApplications(context.Background())
	if err == nil {
		t.Fatal("expected JSON error, got nil")
	}
}

func TestListApplications_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "x", srv.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := c.ListApplications(ctx)
	if err == nil {
		t.Fatal("expected context deadline error, got nil")
	}
}
