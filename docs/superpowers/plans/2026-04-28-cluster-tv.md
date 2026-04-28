# Cluster-TV Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bootstrap the public `madic-creates/homelab-toys` Go monorepo and ship `cluster-tv` — a single-binary Kubernetes wall-display that aggregates ArgoCD app health, Longhorn volume state, cert-manager expiry, and pod restart counts into one CRT-styled glance view.

**Architecture:** Single binary `cmd/cluster-tv` with one polling goroutine per upstream source (ArgoCD HTTP API, Prometheus, kube-apiserver via client-go), shared `State` struct guarded by `sync.RWMutex`, server-rendered HTML using `html/template`, browser polls `/api/state` every 30 s. Stateless, 1-replica Deployment in `monitoring` namespace. Distroless image, GitHub Actions release workflow, Renovate-driven digest pinning in `k3s-git-ops`.

**Tech Stack:** Go 1.26 (toolchain matches `claude-alert-analyzer`), `net/http`, `html/template`, `log/slog`, `k8s.io/client-go`, `k8s.io/client-go/dynamic` (for cert-manager.io CRDs — avoids pulling in cert-manager-io as a typed dep), `prometheus/client_golang` for `/metrics`. No web framework, no JS bundler, handwritten CSS ≤ 200 lines per theme.

**Repo conventions reused from `claude-alert-analyzer`:**
- Multi-stage Dockerfile (`golang:1.26-alpine` builder → `scratch` runtime, `USER 65534:65534`)
- `.golangci.yml` v2 with errcheck/govet/staticcheck/unused/ineffassign
- CI: separate `test` + `lint` jobs gating per-binary `build-*` jobs that push `ghcr.io/madic-creates/<image>:{sha,latest}`

**Repo conventions reused from `k3s-git-ops`:**
- `apps/<name>/` with `kustomization.yaml`, `k8s.deployment.yaml`, `k8s.service.yaml`, `k8s.rbac.yaml`, `k8s.np.<name>.yaml` (CiliumNetworkPolicy), `secrets.enc.yaml`, `kustomize-secret-generator.yaml` (ksops)
- Plain `Ingress` with Traefik annotations (not `IngressRoute` — repo uses Ingress + middleware annotations; the spec used "IngressRoute" loosely)
- ArgoCD Application manifest in `apps/argo-cd-apps/<NN>-<name>.yaml`, sync-wave annotation
- Sync-wave conventions: ServiceAccount/RBAC `"1"`, NetworkPolicy `"5"`, workload `"10"`

**Out of scope for this plan:** anything in the Pod-Tamagotchi spec (`cmd/tamagotchi/`, `internal/health/`, `internal/kube/nodes.go`). Tamagotchi adds a parallel CI job and an additive `nodes.go` later — this plan must leave `internal/kube/` ready to be extended without modification.

---

## File Structure (target end state)

```
homelab-toys/
├── .github/workflows/release.yaml     # CI: test, lint, build-cluster-tv
├── .golangci.yml                      # linter config
├── .gitignore                         # already exists
├── README.md                          # repo intro + per-binary section
├── Dockerfile.cluster-tv              # multi-stage, scratch runtime
├── go.mod                             # module github.com/madic-creates/homelab-toys
├── go.sum
├── cmd/
│   └── cluster-tv/
│       ├── main.go                    # wire-up, flags, signal handling
│       ├── state.go                   # State struct + RWMutex + AllGreen
│       ├── state_test.go
│       ├── aggregator.go              # one goroutine per source, ticker, recover
│       ├── aggregator_test.go
│       ├── handlers.go                # /, /api/state, /healthz, /metrics
│       └── handlers_test.go
├── internal/
│   ├── kube/
│   │   ├── client.go                  # rest.InClusterConfig + Clientset/DynamicClient
│   │   └── client_test.go             # build-time only (no fakes — see Task 4)
│   ├── argocd/
│   │   ├── client.go                  # HTTP client for /api/v1/applications
│   │   └── client_test.go             # httptest server table tests
│   ├── prom/
│   │   ├── client.go                  # generic instant-query helper
│   │   └── client_test.go             # httptest server table tests
│   └── certs/
│       ├── lister.go                  # dynamic client list of cert-manager.io/v1 Certificates
│       └── lister_test.go             # fake dynamic client table tests
└── web/
    └── cluster-tv/
        ├── index.html.tmpl            # html/template root
        ├── tile.html.tmpl             # tile partial
        ├── crt.css                    # CRT theme (~150 lines)
        └── modern.css                 # modern theme (~100 lines)
```

In `k3s-git-ops` (separate repo, separate branch/PR):

```
apps/
├── cluster-tv/
│   ├── kustomization.yaml
│   ├── k8s.deployment.yaml
│   ├── k8s.service.yaml
│   ├── k8s.rbac.yaml
│   ├── k8s.np.cluster-tv-default-deny.yaml
│   ├── k8s.np.cluster-tv.yaml
│   ├── secrets.enc.yaml                          # SOPS-encrypted
│   ├── kustomize-secret-generator.yaml
│   └── cluster-tv.ingress.enc.yaml               # SOPS-encrypted
└── argo-cd-apps/
    └── 90-cluster-tv.yaml                        # new + entry in kustomization.yaml
```

---

## Phase 1 — Repo Bootstrap

### Task 1: `go.mod`, `.golangci.yml`, `.gitignore`, `README.md`

**Files:**
- Create: `go.mod`
- Create: `.golangci.yml`
- Modify: `.gitignore` (currently 7 bytes — likely just `.envrc` or empty-ish)
- Create: `README.md`

- [ ] **Step 1: Initialize the Go module**

Run from repo root:

```bash
cd /home/mne-adm/Git/personal/github.com/homelab-toys
go mod init github.com/madic-creates/homelab-toys
go mod edit -go=1.26
```

Expected: creates `go.mod` with module path and Go directive.

- [ ] **Step 2: Add the runtime + test dependencies**

Run:

```bash
go get github.com/prometheus/client_golang@v1.23.2
go get k8s.io/api@v0.35.4
go get k8s.io/apimachinery@v0.35.4
go get k8s.io/client-go@v0.35.4
go mod tidy
```

Expected: `go.sum` populated; `go mod tidy` exits 0.

- [ ] **Step 3: Write `.golangci.yml`**

Create `/home/mne-adm/Git/personal/github.com/homelab-toys/.golangci.yml`:

```yaml
version: "2"

run:
  timeout: 5m

linters:
  enable:
    - errcheck
    - govet
    - staticcheck
    - unused
    - ineffassign

  settings:
    errcheck:
      exclude-functions:
        - (net/http.ResponseWriter).Write
        - (*net/http.Response).Body.Close
        - fmt.Fprint
        - fmt.Fprintf
        - fmt.Fprintln
        - io.Copy

  exclusions:
    paths:
      - docs
    rules:
      - path: _test\.go
        linters:
          - errcheck
```

- [ ] **Step 4: Update `.gitignore`**

Read current contents, then ensure these lines are present (append if missing):

```
.envrc
/bin/
/dist/
*.test
*.out
.idea/
.vscode/
```

- [ ] **Step 5: Write `README.md`**

Create with minimal content — the repo houses small, single-purpose tools that share a `kube/argocd/prom/certs` core. Follow this structure:

```markdown
# homelab-toys

Small, single-purpose tools for `madic-creates`'s homelab cluster.
Each binary lives in `cmd/<name>/` and reuses the shared packages in `internal/`.

## Tools

### cluster-tv
Single-page wall-display that aggregates ArgoCD application health, Longhorn
volume state, cert-manager expiry, and pod restart counts. CRT and modern
themes selectable via `?theme=...`. Image: `ghcr.io/madic-creates/cluster-tv`.

## Layout

- `cmd/<name>/` — one directory per binary
- `internal/` — shared packages: `kube`, `argocd`, `prom`, `certs`
- `web/<name>/` — server-rendered HTML templates and CSS for each tool

## Build / test

    go test -race ./...
    go vet ./...
    golangci-lint run

CI builds and pushes per-binary images on every push to `main`.
```

- [ ] **Step 6: Verify Go module compiles**

Run:

```bash
go build ./...
```

Expected: succeeds with no output (no `cmd/` or `internal/` packages exist yet, so the command does nothing — just verifies the module is well-formed).

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum .golangci.yml .gitignore README.md
git commit -m "chore: bootstrap homelab-toys go module"
```

---

### Task 2: `Dockerfile.cluster-tv`

**Files:**
- Create: `Dockerfile.cluster-tv`
- Create: `.dockerignore`

- [ ] **Step 1: Write `.dockerignore`**

Create:

```
.git
.github
docs
README.md
*.md
*.test
*.out
.idea
.vscode
```

- [ ] **Step 2: Write `Dockerfile.cluster-tv`**

Multi-stage, mirrors `claude-alert-analyzer`'s pattern but binary-specific:

```dockerfile
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache ca-certificates
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/ web/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /cluster-tv ./cmd/cluster-tv/

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /cluster-tv /cluster-tv
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/cluster-tv"]
```

Note: the binary embeds templates via `embed.FS` (Task 11), so the runtime image needs no separate template copy.

- [ ] **Step 3: Verify Dockerfile syntax**

Run:

```bash
docker buildx build --check -f Dockerfile.cluster-tv . 2>&1 | head -20
```

Expected: no syntax warnings (the build will fail at the `COPY cmd/` step because the directory doesn't exist yet — `--check` only validates syntax, so this is the expected outcome).

If `docker` is unavailable locally, skip this step — the CI run in Task 3 will catch syntax problems.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile.cluster-tv .dockerignore
git commit -m "chore: add Dockerfile.cluster-tv"
```

---

### Task 3: GitHub Actions release workflow

**Files:**
- Create: `.github/workflows/release.yaml`

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/release.yaml`. Pattern lifted from `claude-alert-analyzer/.github/workflows/build.yaml` but renamed and simplified for one binary (Pod-Tamagotchi will add a parallel `build-tamagotchi` job later — keep `test`/`lint` shared so its addition stays additive):

```yaml
---
name: Release Images

on:
  push:
    branches: [main]
    paths:
      - "cmd/**"
      - "internal/**"
      - "web/**"
      - "Dockerfile.*"
      - "go.mod"
      - "go.sum"
      - ".github/workflows/release.yaml"
  workflow_dispatch:

permissions:
  contents: read
  packages: write

concurrency:
  group: ${{ github.workflow }}
  cancel-in-progress: false

env:
  REGISTRY: ghcr.io/${{ github.repository_owner }}

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - run: go vet ./...
      - run: go test -race -count=1 ./...

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - uses: golangci/golangci-lint-action@v9

  build-cluster-tv:
    needs: [test, lint]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6
      - uses: docker/setup-buildx-action@v4
      - uses: docker/login-action@v4
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - id: meta
        uses: docker/metadata-action@v6
        with:
          images: ${{ env.REGISTRY }}/cluster-tv
          tags: |
            type=sha,format=short,prefix=
            type=raw,value=latest,enable={{is_default_branch}}
      - uses: docker/build-push-action@v7
        with:
          context: .
          file: Dockerfile.cluster-tv
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

- [ ] **Step 2: Validate YAML syntax**

Run:

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yaml'))"
```

Expected: exits 0 with no output.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yaml
git commit -m "ci: add release workflow for cluster-tv"
```

CI will fail at this point (no `cmd/cluster-tv/main.go` yet) — that is expected. The first green run lands at the end of Phase 3.

---

## Phase 2 — Internal Packages (TDD)

### Task 4: `internal/kube` — shared client factory

This package gives `cmd/cluster-tv` (and later `cmd/tamagotchi`) a single way to construct `kubernetes.Interface` and `dynamic.Interface` from in-cluster config. There is no real logic worth unit-testing — the `rest.InClusterConfig()` call only works inside a Pod. The "test" is a build-time check that the package compiles and the constructor returns the expected interface types.

**Files:**
- Create: `internal/kube/client.go`
- Create: `internal/kube/client_test.go`

- [ ] **Step 1: Write the failing build-time test**

Create `internal/kube/client_test.go`:

```go
package kube

import (
	"testing"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// TestNewClientsFromConfigSignature is a compile-time assertion that
// NewClientsFromConfig returns the interface types we depend on.
// It does not call rest.InClusterConfig (which only works inside a Pod).
func TestNewClientsFromConfigSignature(t *testing.T) {
	cfg := &rest.Config{Host: "https://example.invalid"}
	cs, dyn, err := NewClientsFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewClientsFromConfig: %v", err)
	}
	var _ kubernetes.Interface = cs
	var _ dynamic.Interface = dyn
}
```

- [ ] **Step 2: Run test, expect failure**

Run:

```bash
go test ./internal/kube/...
```

Expected: build error — `undefined: NewClientsFromConfig`.

- [ ] **Step 3: Implement `client.go`**

Create `internal/kube/client.go`:

```go
// Package kube provides a shared factory for Kubernetes clientsets used by
// the homelab-toys binaries. It centralises the in-cluster config loading
// so that callers do not duplicate the rest.InClusterConfig boilerplate.
package kube

import (
	"fmt"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// NewInCluster loads in-cluster config and returns a typed clientset and a
// dynamic client. Use this from main(); fail fast on error.
func NewInCluster() (kubernetes.Interface, dynamic.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("in-cluster config: %w", err)
	}
	return NewClientsFromConfig(cfg)
}

// NewClientsFromConfig builds clients from an arbitrary rest.Config. Split
// out so tests (and any future out-of-cluster path) can inject a config.
func NewClientsFromConfig(cfg *rest.Config) (kubernetes.Interface, dynamic.Interface, error) {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("kubernetes clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("dynamic client: %w", err)
	}
	return cs, dyn, nil
}
```

- [ ] **Step 4: Run test, expect pass**

Run:

```bash
go test ./internal/kube/...
```

Expected: `ok  github.com/madic-creates/homelab-toys/internal/kube`.

- [ ] **Step 5: Commit**

```bash
git add internal/kube/
git commit -m "feat(kube): shared in-cluster client factory"
```

---

### Task 5: `internal/argocd` — ArgoCD applications client

Spec source: `GET /api/v1/applications` with `Authorization: Bearer <token>`. Token is loaded from env elsewhere (Task 12) — the package only takes config in.

**Files:**
- Create: `internal/argocd/client.go`
- Create: `internal/argocd/client_test.go`

- [ ] **Step 1: Write the failing test (table-driven, httptest)**

Create `internal/argocd/client_test.go`:

```go
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
```

- [ ] **Step 2: Run test, expect failure**

Run:

```bash
go test ./internal/argocd/...
```

Expected: build error — `undefined: NewClient`.

- [ ] **Step 3: Implement `client.go`**

Create `internal/argocd/client.go`:

```go
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
```

- [ ] **Step 4: Run tests, expect pass**

Run:

```bash
go test -race ./internal/argocd/...
```

Expected: `ok  ...  (cached)` — all four sub-tests pass under `-race`.

- [ ] **Step 5: Commit**

```bash
git add internal/argocd/
git commit -m "feat(argocd): list-applications client"
```

---

### Task 6: `internal/prom` — Prometheus instant-query helper

The package only does instant queries (`GET /api/v1/query?query=...`) and returns the parsed `data.result` items. Both Longhorn-volume counts and pod-restart counts use this — we don't need range queries.

**Files:**
- Create: `internal/prom/client.go`
- Create: `internal/prom/client_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/prom/client_test.go`:

```go
package prom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestQuery_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("query"); got != `up{job="prometheus"}` {
			t.Errorf("query = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "success",
			"data": {
				"resultType": "vector",
				"result": [
					{"metric":{"job":"prometheus"},"value":[1714000000,"1"]},
					{"metric":{"job":"node"},"value":[1714000000,"0"]}
				]
			}
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	res, err := c.Query(context.Background(), `up{job="prometheus"}`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(res))
	}
	if res[0].Metric["job"] != "prometheus" || res[0].Value != "1" {
		t.Errorf("res[0] = %+v", res[0])
	}
}

func TestQuery_PrometheusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"unexpected token"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	_, err := c.Query(context.Background(), "broken{")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQuery_HTTPNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	_, err := c.Query(context.Background(), "up")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQuery_URLEncodes(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	if _, err := c.Query(context.Background(), `count by (state) (longhorn_volume_robustness == 1)`); err != nil {
		t.Fatalf("Query: %v", err)
	}
	parsed, err := url.ParseQuery(got)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if parsed.Get("query") != `count by (state) (longhorn_volume_robustness == 1)` {
		t.Errorf("query encoded as %q", parsed.Get("query"))
	}
}
```

- [ ] **Step 2: Run test, expect failure**

```bash
go test ./internal/prom/...
```

Expected: build error — `undefined: NewClient`.

- [ ] **Step 3: Implement `client.go`**

Create `internal/prom/client.go`:

```go
// Package prom is a tiny wrapper around the Prometheus instant-query API.
// It does not pull in github.com/prometheus/common/model or the upstream
// API client — both of those add a lot of surface area for the two queries
// we actually run.
package prom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Sample is one element of the `data.result` vector. Value is the string
// form Prometheus emits — callers parse with strconv.ParseFloat or read
// it as-is for "is this exactly 1?" checks.
type Sample struct {
	Metric map[string]string
	Value  string
}

type Client struct {
	baseURL string
	hc      *http.Client
}

func NewClient(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), hc: hc}
}

// Query runs a Prometheus instant query and returns the result vector.
// Range queries are deliberately unsupported — cluster-tv has no use for them.
func (c *Client) Query(ctx context.Context, q string) ([]Sample, error) {
	v := url.Values{}
	v.Set("query", q)
	u := c.baseURL + "/api/v1/query?" + v.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prom request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prom: status %d", resp.StatusCode)
	}

	var raw struct {
		Status    string `json:"status"`
		ErrorType string `json:"errorType,omitempty"`
		Error     string `json:"error,omitempty"`
		Data      struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("prom: decode: %w", err)
	}
	if raw.Status != "success" {
		return nil, fmt.Errorf("prom: %s: %s", raw.ErrorType, raw.Error)
	}

	out := make([]Sample, 0, len(raw.Data.Result))
	for _, r := range raw.Data.Result {
		val, _ := r.Value[1].(string)
		out = append(out, Sample{Metric: r.Metric, Value: val})
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests, expect pass**

```bash
go test -race ./internal/prom/...
```

Expected: all four sub-tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/prom/
git commit -m "feat(prom): instant-query helper"
```

---

### Task 7: `internal/certs` — cert-manager Certificate lister

Uses the dynamic client (so the binary doesn't need to import `cert-manager.io` Go modules just for a `notAfter` field). Returns one entry per Certificate whose `status.notAfter` is set and lies before `now+window`. Certificates without `notAfter` (not yet issued / failed) are skipped silently — the spec calls this out explicitly.

**Files:**
- Create: `internal/certs/lister.go`
- Create: `internal/certs/lister_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/certs/lister_test.go`:

```go
package certs

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newCert(ns, name string, notAfter *time.Time) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"})
	u.SetNamespace(ns)
	u.SetName(name)
	if notAfter != nil {
		_ = unstructured.SetNestedField(u.Object, notAfter.UTC().Format(time.RFC3339), "status", "notAfter")
	}
	return u
}

func TestExpiringSoon(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	in7d := now.Add(7 * 24 * time.Hour)
	in40d := now.Add(40 * 24 * time.Hour)
	expired := now.Add(-2 * 24 * time.Hour)

	scheme := runtime.NewScheme()
	gvr := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
	listKinds := map[schema.GroupVersionResource]string{gvr: "CertificateList"}

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds,
		newCert("default", "soon", &in7d),
		newCert("kube-system", "later", &in40d),
		newCert("monitoring", "expired", &expired),
		newCert("default", "no-notafter", nil),
	)

	l := NewLister(dyn)
	res, err := l.ExpiringSoon(context.Background(), now, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("ExpiringSoon: %v", err)
	}

	got := map[string]bool{}
	for _, c := range res {
		got[c.Namespace+"/"+c.Name] = true
	}
	if !got["default/soon"] {
		t.Errorf("expected default/soon")
	}
	if !got["monitoring/expired"] {
		t.Errorf("expected monitoring/expired (already expired counts as expiring)")
	}
	if got["kube-system/later"] {
		t.Errorf("did not expect kube-system/later (40d > 30d window)")
	}
	if got["default/no-notafter"] {
		t.Errorf("did not expect default/no-notafter (skip cert without notAfter)")
	}
	if len(res) != 2 {
		t.Errorf("len = %d, want 2", len(res))
	}

	for _, c := range res {
		if c.Namespace == "default" && c.Name == "soon" {
			if !c.NotAfter.Equal(in7d) {
				t.Errorf("notAfter for default/soon = %v, want %v", c.NotAfter, in7d)
			}
		}
	}
}

func TestExpiringSoon_EmptyList(t *testing.T) {
	scheme := runtime.NewScheme()
	gvr := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{gvr: "CertificateList"})
	l := NewLister(dyn)
	res, err := l.ExpiringSoon(context.Background(), time.Now(), 30*24*time.Hour)
	if err != nil {
		t.Fatalf("ExpiringSoon: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("len = %d, want 0", len(res))
	}
}

// silence unused-import warning when go test --short is used
var _ = metav1.ListOptions{}
```

- [ ] **Step 2: Run test, expect failure**

```bash
go test ./internal/certs/...
```

Expected: build error — `undefined: NewLister`.

- [ ] **Step 3: Implement `lister.go`**

Create `internal/certs/lister.go`:

```go
// Package certs lists cert-manager Certificate resources cluster-wide via
// the dynamic client. We use the dynamic client (rather than the typed
// cert-manager-io Go module) to keep the homelab-toys dep graph small —
// only status.notAfter is needed, and that field is stable.
package certs

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// Cert is the trimmed view of a cert-manager Certificate.
type Cert struct {
	Namespace string
	Name      string
	NotAfter  time.Time
}

// CertificatesGVR is the GroupVersionResource of cert-manager certificates.
var CertificatesGVR = schema.GroupVersionResource{
	Group:    "cert-manager.io",
	Version:  "v1",
	Resource: "certificates",
}

type Lister struct {
	dyn dynamic.Interface
}

func NewLister(dyn dynamic.Interface) *Lister {
	return &Lister{dyn: dyn}
}

// ExpiringSoon returns certs whose status.notAfter is set and is strictly
// before now+window. Already-expired certs are included (they count as
// "expiring"); certs without a populated status.notAfter are skipped — the
// spec is explicit on this.
func (l *Lister) ExpiringSoon(ctx context.Context, now time.Time, window time.Duration) ([]Cert, error) {
	list, err := l.dyn.Resource(CertificatesGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list certificates: %w", err)
	}

	cutoff := now.Add(window)
	out := make([]Cert, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		t, ok := readNotAfter(item)
		if !ok {
			continue
		}
		if t.Before(cutoff) {
			out = append(out, Cert{
				Namespace: item.GetNamespace(),
				Name:      item.GetName(),
				NotAfter:  t,
			})
		}
	}
	return out, nil
}

func readNotAfter(u *unstructured.Unstructured) (time.Time, bool) {
	s, found, err := unstructured.NestedString(u.Object, "status", "notAfter")
	if err != nil || !found || s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
```

- [ ] **Step 4: Run tests, expect pass**

```bash
go test -race ./internal/certs/...
```

Expected: both sub-tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/certs/
git commit -m "feat(certs): cert-manager expiry lister via dynamic client"
```

---

## Phase 3 — `cmd/cluster-tv`

### Task 8: `state.go` — shared State + RWMutex + AllGreen

The `State` struct is the single shared in-memory snapshot. Goroutines write per-source slots; handlers read snapshots. Each slot carries `Data`, `LastSuccess`, `LastFailure`, `LastError`. Use small per-source structs rather than `any` so the JSON shape is stable across releases (a constraint the spec calls out).

**Files:**
- Create: `cmd/cluster-tv/state.go`
- Create: `cmd/cluster-tv/state_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/cluster-tv/state_test.go`:

```go
package main

import (
	"sync"
	"testing"
	"time"
)

func TestState_SnapshotIsCopy(t *testing.T) {
	s := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)

	s.SetArgoCD(ArgoCDData{Healthy: 5, Degraded: 1, OutOfSync: 0,
		Bad: []ArgoCDApp{{Name: "foo", Sync: "Synced", Health: "Degraded"}}}, now)

	snap := s.Snapshot()
	if snap.ArgoCD.Data.Healthy != 5 {
		t.Fatalf("snapshot.ArgoCD.Data.Healthy = %d", snap.ArgoCD.Data.Healthy)
	}
	// Mutating the snapshot must not leak back to the live State.
	snap.ArgoCD.Data.Bad[0].Name = "mutated"
	snap2 := s.Snapshot()
	if snap2.ArgoCD.Data.Bad[0].Name != "foo" {
		t.Errorf("snapshot mutation leaked back: %q", snap2.ArgoCD.Data.Bad[0].Name)
	}
}

func TestState_AllGreen(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 10}, now)
	s.SetLonghorn(LonghornData{Healthy: 12}, now)
	s.SetCerts(CertsData{Total: 0}, now)
	s.SetRestarts(RestartsData{Total: 0}, now)
	if !s.AllGreen(now) {
		t.Errorf("AllGreen = false, want true")
	}

	// One Degraded ArgoCD app → not green.
	s.SetArgoCD(ArgoCDData{Degraded: 1}, now)
	if s.AllGreen(now) {
		t.Errorf("AllGreen with Degraded ArgoCD = true, want false")
	}
}

func TestState_AllGreen_StaleSourceSkipped(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	old := now.Add(-10 * time.Minute) // > 5 minutes
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 10}, old) // stale (loaded but old)
	s.SetLonghorn(LonghornData{Healthy: 12}, now)
	s.SetCerts(CertsData{Total: 0}, now)
	s.SetRestarts(RestartsData{Total: 0}, now)
	// Stale source is excluded from AllGreen but the rest must still be green.
	if !s.AllGreen(now) {
		t.Errorf("AllGreen with one stale-but-otherwise-green source = false, want true")
	}
}

func TestState_AllGreen_FalseDuringInit(t *testing.T) {
	// Spec: "Init phase = first 30s. All sources start in a 'loading' state,
	// tiles show skeleton boxes, bad-news-mode is suppressed." Translated:
	// AllGreen must NOT return true while sources are still unloaded —
	// otherwise the page would briefly flash "CLUSTER OK" at startup.
	s := NewState()
	if s.AllGreen(time.Now()) {
		t.Errorf("AllGreen on a fresh State = true, want false (no source loaded yet)")
	}
}

func TestState_AllGreen_RequiresAtLeastOneFreshSource(t *testing.T) {
	// All four sources loaded once but every one of them has gone stale.
	// AllGreen must be false: we have no recent evidence of cluster health.
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	old := now.Add(-10 * time.Minute)
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 10}, old)
	s.SetLonghorn(LonghornData{Healthy: 12}, old)
	s.SetCerts(CertsData{Total: 0}, old)
	s.SetRestarts(RestartsData{Total: 0}, old)
	if s.AllGreen(now) {
		t.Errorf("AllGreen with every source stale = true, want false")
	}
}

func TestState_ConcurrentAccess(t *testing.T) {
	s := NewState()
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.SetArgoCD(ArgoCDData{Healthy: 1}, now)
		}()
		go func() {
			defer wg.Done()
			_ = s.Snapshot()
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run test, expect failure**

```bash
go test ./cmd/cluster-tv/...
```

Expected: build error — `undefined: NewState`, `ArgoCDData`, etc.

- [ ] **Step 3: Implement `state.go`**

Create `cmd/cluster-tv/state.go`:

```go
package main

import (
	"sync"
	"time"
)

// stalenessWindow is the threshold beyond which a source's data is
// considered stale. Stale sources are still rendered (greyed out) but do
// not contribute to AllGreen — the spec calls this out explicitly so a
// brief ArgoCD outage doesn't flip the screen to bad-news.
const stalenessWindow = 5 * time.Minute

// ---------- per-source data shapes ----------

type ArgoCDApp struct {
	Name   string `json:"name"`
	Sync   string `json:"sync"`
	Health string `json:"health"`
}

type ArgoCDData struct {
	Healthy   int         `json:"healthy"`
	Degraded  int         `json:"degraded"`
	OutOfSync int         `json:"out_of_sync"`
	Bad       []ArgoCDApp `json:"bad,omitempty"`
}

type LonghornData struct {
	Healthy  int `json:"healthy"`
	Degraded int `json:"degraded"`
	Faulted  int `json:"faulted"`
	Unknown  int `json:"unknown"`
}

type CertEntry struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	NotAfter  string `json:"not_after"` // RFC3339
}

type CertsData struct {
	Total   int         `json:"total"`
	Expiring []CertEntry `json:"expiring,omitempty"`
}

type RestartingPod struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
	Restarts  int    `json:"restarts"`
}

type RestartsData struct {
	Total int             `json:"total"`
	Pods  []RestartingPod `json:"pods,omitempty"`
}

// ---------- generic slot ----------

// Slot[T] holds the most recent successful payload plus heartbeat metadata
// for one source. Splitting LastSuccess and LastFailure lets the UI show
// "last update X ago" while keeping a separate "last error" message.
type Slot[T any] struct {
	Data        T         `json:"data"`
	LastSuccess time.Time `json:"last_success"`
	LastFailure time.Time `json:"last_failure,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	Loaded      bool      `json:"loaded"` // false until the first successful poll
}

func (s Slot[T]) IsStale(now time.Time) bool {
	if !s.Loaded {
		return true
	}
	return now.Sub(s.LastSuccess) > stalenessWindow
}

// ---------- aggregate State ----------

type Snapshot struct {
	ArgoCD   Slot[ArgoCDData]   `json:"argocd"`
	Longhorn Slot[LonghornData] `json:"longhorn"`
	Certs    Slot[CertsData]    `json:"certs"`
	Restarts Slot[RestartsData] `json:"restarts"`
}

type State struct {
	mu   sync.RWMutex
	snap Snapshot
}

func NewState() *State {
	return &State{}
}

func (s *State) SetArgoCD(d ArgoCDData, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.ArgoCD.Data = d
	s.snap.ArgoCD.LastSuccess = now
	s.snap.ArgoCD.LastError = ""
	s.snap.ArgoCD.Loaded = true
}

func (s *State) SetArgoCDError(err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.ArgoCD.LastFailure = now
	s.snap.ArgoCD.LastError = err.Error()
}

func (s *State) SetLonghorn(d LonghornData, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Longhorn.Data = d
	s.snap.Longhorn.LastSuccess = now
	s.snap.Longhorn.LastError = ""
	s.snap.Longhorn.Loaded = true
}

func (s *State) SetLonghornError(err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Longhorn.LastFailure = now
	s.snap.Longhorn.LastError = err.Error()
}

func (s *State) SetCerts(d CertsData, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Certs.Data = d
	s.snap.Certs.LastSuccess = now
	s.snap.Certs.LastError = ""
	s.snap.Certs.Loaded = true
}

func (s *State) SetCertsError(err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Certs.LastFailure = now
	s.snap.Certs.LastError = err.Error()
}

func (s *State) SetRestarts(d RestartsData, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Restarts.Data = d
	s.snap.Restarts.LastSuccess = now
	s.snap.Restarts.LastError = ""
	s.snap.Restarts.Loaded = true
}

func (s *State) SetRestartsError(err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Restarts.LastFailure = now
	s.snap.Restarts.LastError = err.Error()
}

// Snapshot returns a deep copy of the current state. Slices are cloned so
// callers can mutate freely without holding the lock.
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.snap
	if len(out.ArgoCD.Data.Bad) > 0 {
		out.ArgoCD.Data.Bad = append([]ArgoCDApp(nil), out.ArgoCD.Data.Bad...)
	}
	if len(out.Certs.Data.Expiring) > 0 {
		out.Certs.Data.Expiring = append([]CertEntry(nil), out.Certs.Data.Expiring...)
	}
	if len(out.Restarts.Data.Pods) > 0 {
		out.Restarts.Data.Pods = append([]RestartingPod(nil), out.Restarts.Data.Pods...)
	}
	return out
}

// AllGreen reports true iff at least one source is fresh AND every fresh
// source is green. We require ≥ 1 fresh source so the UI never flashes
// "CLUSTER OK" while sources are still loading — the spec's init-phase
// rule. Stale-but-loaded sources are excluded so a brief outage doesn't
// flip the screen to bad-news.
func (s *State) AllGreen(now time.Time) bool {
	snap := s.Snapshot()

	freshCount := 0
	check := func(stale bool, bad bool) bool {
		if stale {
			return true // skip stale, doesn't influence verdict
		}
		freshCount++
		return !bad
	}

	ok := check(snap.ArgoCD.IsStale(now),
		snap.ArgoCD.Data.Degraded > 0 || snap.ArgoCD.Data.OutOfSync > 0) &&
		check(snap.Longhorn.IsStale(now),
			snap.Longhorn.Data.Degraded > 0 || snap.Longhorn.Data.Faulted > 0) &&
		check(snap.Certs.IsStale(now),
			snap.Certs.Data.Total > 0) &&
		check(snap.Restarts.IsStale(now),
			snap.Restarts.Data.Total > 0)

	return ok && freshCount > 0
}

// StaleCount counts stale loaded sources, for the "N source(s) stale" banner.
func (s *State) StaleCount(now time.Time) int {
	snap := s.Snapshot()
	c := 0
	for _, slot := range []struct{ stale bool }{
		{snap.ArgoCD.IsStale(now) && snap.ArgoCD.Loaded},
		{snap.Longhorn.IsStale(now) && snap.Longhorn.Loaded},
		{snap.Certs.IsStale(now) && snap.Certs.Loaded},
		{snap.Restarts.IsStale(now) && snap.Restarts.Loaded},
	} {
		if slot.stale {
			c++
		}
	}
	return c
}
```

- [ ] **Step 4: Run tests, expect pass**

```bash
go test -race ./cmd/cluster-tv/...
```

Expected: all four sub-tests pass under `-race`.

- [ ] **Step 5: Commit**

```bash
git add cmd/cluster-tv/state.go cmd/cluster-tv/state_test.go
git commit -m "feat(cluster-tv): shared State with deep-copy snapshots and AllGreen"
```

---

### Task 9: `aggregator.go` — per-source goroutines

Each source goroutine runs a 20-second `time.Ticker` loop, polls its source, calls the right `state.Set*` method, and emits Prometheus metrics. Panics are `recover()`-ed and the goroutine is restarted after a 10-second backoff. The aggregator interface is small so handlers/tests can fake it.

**Files:**
- Create: `cmd/cluster-tv/aggregator.go`
- Create: `cmd/cluster-tv/aggregator_test.go`

- [ ] **Step 1: Write the failing test**

The aggregator test focuses on the conversion logic from raw upstream payloads to State updates — `runSource` itself just composes a poller, a setter, and a ticker, which is integration-level glue.

Create `cmd/cluster-tv/aggregator_test.go`:

```go
package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/madic-creates/homelab-toys/internal/argocd"
	"github.com/madic-creates/homelab-toys/internal/prom"
)

func TestArgoPayloadToData(t *testing.T) {
	in := []argocd.Application{
		{Name: "a", Sync: "Synced", Health: "Healthy"},
		{Name: "b", Sync: "OutOfSync", Health: "Healthy"},
		{Name: "c", Sync: "Synced", Health: "Degraded"},
		{Name: "d", Sync: "OutOfSync", Health: "Degraded"},
	}
	got := argoPayloadToData(in)
	if got.Healthy != 1 {
		t.Errorf("Healthy = %d, want 1", got.Healthy)
	}
	if got.OutOfSync != 2 {
		t.Errorf("OutOfSync = %d, want 2 (b, d)", got.OutOfSync)
	}
	if got.Degraded != 2 {
		t.Errorf("Degraded = %d, want 2 (c, d)", got.Degraded)
	}
	if len(got.Bad) != 3 {
		t.Errorf("len(Bad) = %d, want 3", len(got.Bad))
	}
}

func TestLonghornSamplesToData(t *testing.T) {
	in := []prom.Sample{
		{Metric: map[string]string{"state": "healthy"}, Value: "10"},
		{Metric: map[string]string{"state": "degraded"}, Value: "2"},
		{Metric: map[string]string{"state": "faulted"}, Value: "1"},
		{Metric: map[string]string{"state": "unknown"}, Value: "0"},
	}
	got := longhornSamplesToData(in)
	if got.Healthy != 10 || got.Degraded != 2 || got.Faulted != 1 || got.Unknown != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestRestartSamplesToData(t *testing.T) {
	in := []prom.Sample{
		{Metric: map[string]string{"namespace": "default", "pod": "p1", "container": "app"}, Value: "7"},
		{Metric: map[string]string{"namespace": "kube-system", "pod": "p2", "container": "core"}, Value: "12"},
	}
	got := restartSamplesToData(in)
	if got.Total != 2 {
		t.Errorf("Total = %d, want 2", got.Total)
	}
	if len(got.Pods) != 2 {
		t.Errorf("len(Pods) = %d, want 2", len(got.Pods))
	}
	if got.Pods[0].Restarts != 7 || got.Pods[1].Restarts != 12 {
		t.Errorf("Pods = %+v", got.Pods)
	}
}

func TestRunSource_FirstTickWritesState(t *testing.T) {
	s := NewState()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pollCalls := 0
	poll := func(_ context.Context) error {
		pollCalls++
		s.SetArgoCD(ArgoCDData{Healthy: 42}, time.Now())
		return nil
	}

	done := make(chan struct{})
	go func() {
		runSource(ctx, "argocd", poll, 5*time.Millisecond)
		close(done)
	}()

	// Wait for at least one poll, then cancel.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if s.Snapshot().ArgoCD.Data.Healthy == 42 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if pollCalls < 1 {
		t.Errorf("pollCalls = %d, want >= 1", pollCalls)
	}
	if s.Snapshot().ArgoCD.Data.Healthy != 42 {
		t.Errorf("Healthy = %d, want 42", s.Snapshot().ArgoCD.Data.Healthy)
	}
}

func TestRunSource_RecoversFromPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	poll := func(_ context.Context) error {
		calls++
		if calls == 1 {
			panic("boom")
		}
		return nil
	}

	done := make(chan struct{})
	go func() {
		// Use a short backoff for the test.
		runSourceWithBackoff(ctx, "test", poll, 5*time.Millisecond, 20*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if calls >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if calls < 2 {
		t.Errorf("calls = %d, want >= 2 (panic should be recovered and the goroutine restarted)", calls)
	}
}

func TestRunSource_PollErrorKeepsRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	poll := func(_ context.Context) error {
		calls++
		return errors.New("upstream down")
	}

	done := make(chan struct{})
	go func() {
		runSource(ctx, "test", poll, 5*time.Millisecond)
		close(done)
	}()

	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done

	if calls < 3 {
		t.Errorf("calls = %d, want >= 3 (errors must not stop the loop)", calls)
	}
	// We don't assert on log output; this test is about not exiting.
	_ = strings.Contains
}
```

- [ ] **Step 2: Run test, expect failure**

```bash
go test ./cmd/cluster-tv/...
```

Expected: build error — `argoPayloadToData` etc. undefined.

- [ ] **Step 3: Implement `aggregator.go`**

Create `cmd/cluster-tv/aggregator.go`:

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/madic-creates/homelab-toys/internal/argocd"
	"github.com/madic-creates/homelab-toys/internal/certs"
	"github.com/madic-creates/homelab-toys/internal/prom"
)

// pollFunc is what each source goroutine actually runs. Errors are logged
// and surfaced via the State's per-source LastError field.
type pollFunc func(ctx context.Context) error

const (
	defaultTickInterval = 20 * time.Second
	defaultBackoff      = 10 * time.Second
)

// runSource is the production wrapper around runSourceWithBackoff, using
// the spec's 10-second post-panic backoff.
func runSource(ctx context.Context, name string, poll pollFunc, interval time.Duration) {
	runSourceWithBackoff(ctx, name, poll, interval, defaultBackoff)
}

// runSourceWithBackoff is split out so tests can inject a short backoff.
func runSourceWithBackoff(ctx context.Context, name string, poll pollFunc, interval, backoff time.Duration) {
	for ctx.Err() == nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("source panic",
						"source", name, "panic", fmt.Sprint(r))
				}
			}()
			tickOnce(ctx, name, poll)
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					tickOnce(ctx, name, poll)
				}
			}
		}()
		// The goroutine returned because of either ctx-cancel (we exit the
		// outer loop on the next iteration) or a recovered panic (we sleep
		// and retry).
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func tickOnce(ctx context.Context, name string, poll pollFunc) {
	if err := poll(ctx); err != nil {
		slog.Warn("source poll failed", "source", name, "error", err)
	}
}

// ---------- per-source pollFunc factories ----------

// MakeArgoCDPoll returns a pollFunc that lists ArgoCD apps and writes the
// resulting ArgoCDData into the State.
func MakeArgoCDPoll(c *argocd.Client, s *State, now func() time.Time) pollFunc {
	return func(ctx context.Context) error {
		apps, err := c.ListApplications(ctx)
		if err != nil {
			s.SetArgoCDError(err, now())
			return err
		}
		s.SetArgoCD(argoPayloadToData(apps), now())
		return nil
	}
}

func argoPayloadToData(apps []argocd.Application) ArgoCDData {
	out := ArgoCDData{}
	for _, a := range apps {
		bad := false
		if a.Health == "Degraded" {
			out.Degraded++
			bad = true
		}
		if a.Sync == "OutOfSync" {
			out.OutOfSync++
			bad = true
		}
		if !bad {
			if a.Health == "Healthy" && a.Sync == "Synced" {
				out.Healthy++
			}
			continue
		}
		out.Bad = append(out.Bad, ArgoCDApp{Name: a.Name, Sync: a.Sync, Health: a.Health})
	}
	return out
}

// MakeLonghornPoll returns a pollFunc that issues the longhorn-volume
// state Prometheus query.
func MakeLonghornPoll(c *prom.Client, s *State, now func() time.Time) pollFunc {
	const q = `count(longhorn_volume_robustness == 1) by (state)`
	return func(ctx context.Context) error {
		samples, err := c.Query(ctx, q)
		if err != nil {
			s.SetLonghornError(err, now())
			return err
		}
		s.SetLonghorn(longhornSamplesToData(samples), now())
		return nil
	}
}

func longhornSamplesToData(samples []prom.Sample) LonghornData {
	out := LonghornData{}
	for _, s := range samples {
		n, _ := strconv.Atoi(s.Value)
		switch s.Metric["state"] {
		case "healthy":
			out.Healthy = n
		case "degraded":
			out.Degraded = n
		case "faulted":
			out.Faulted = n
		case "unknown":
			out.Unknown = n
		}
	}
	return out
}

// MakeRestartsPoll returns a pollFunc for the pod-restart Prometheus query.
func MakeRestartsPoll(c *prom.Client, s *State, now func() time.Time) pollFunc {
	const q = `increase(kube_pod_container_status_restarts_total[24h]) > 5`
	return func(ctx context.Context) error {
		samples, err := c.Query(ctx, q)
		if err != nil {
			s.SetRestartsError(err, now())
			return err
		}
		s.SetRestarts(restartSamplesToData(samples), now())
		return nil
	}
}

func restartSamplesToData(samples []prom.Sample) RestartsData {
	out := RestartsData{Total: len(samples)}
	for _, s := range samples {
		n, _ := strconv.Atoi(s.Value)
		out.Pods = append(out.Pods, RestartingPod{
			Namespace: s.Metric["namespace"],
			Pod:       s.Metric["pod"],
			Container: s.Metric["container"],
			Restarts:  n,
		})
	}
	return out
}

// MakeCertsPoll returns a pollFunc for cert-manager certificate expiry.
func MakeCertsPoll(l *certs.Lister, window time.Duration, s *State, now func() time.Time) pollFunc {
	return func(ctx context.Context) error {
		t := now()
		expiring, err := l.ExpiringSoon(ctx, t, window)
		if err != nil {
			s.SetCertsError(err, t)
			return err
		}
		out := CertsData{Total: len(expiring)}
		for _, c := range expiring {
			out.Expiring = append(out.Expiring, CertEntry{
				Namespace: c.Namespace,
				Name:      c.Name,
				NotAfter:  c.NotAfter.UTC().Format(time.RFC3339),
			})
		}
		s.SetCerts(out, t)
		return nil
	}
}
```

- [ ] **Step 4: Run tests, expect pass (with -race)**

```bash
go test -race ./cmd/cluster-tv/...
```

Expected: all sub-tests in both `state_test.go` and `aggregator_test.go` pass under `-race`.

- [ ] **Step 5: Commit**

```bash
git add cmd/cluster-tv/aggregator.go cmd/cluster-tv/aggregator_test.go
git commit -m "feat(cluster-tv): per-source aggregator goroutines with panic recovery"
```

---

### Task 10: `handlers.go` — `/`, `/api/state`, `/healthz`, `/metrics`

The handlers operate on `*State` and a small set of pre-parsed templates. Metrics use `prometheus/client_golang`. The `healthz` rule is "every loaded source has a heartbeat ≤ 90 s old; sources that have never loaded are tolerated for the first 30 s after process start" — so we need `processStart` injected.

**Files:**
- Create: `cmd/cluster-tv/handlers.go`
- Create: `cmd/cluster-tv/handlers_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/cluster-tv/handlers_test.go`:

```go
package main

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// minimal stand-in templates so handler tests don't depend on the real
// embedded files (those are loaded via embed.FS in main.go).
func testTemplates(t *testing.T) *template.Template {
	t.Helper()
	tpl, err := template.New("index").Parse(`<!doctype html><html data-theme="{{.Theme}}"><body><h1>cluster-tv</h1>
<div id="argocd">{{.S.ArgoCD.Data.Healthy}} healthy</div>
{{if .AllGreen}}<div id="ok">CLUSTER OK</div>{{end}}
</body></html>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return tpl
}

func TestHandleAPIState_AlwaysReturnsJSON(t *testing.T) {
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 5}, time.Now())
	h := NewHandlers(s, testTemplates(t), time.Now)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	h.HandleAPIState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("content-type = %q", got)
	}
	var snap Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.ArgoCD.Data.Healthy != 5 {
		t.Errorf("ArgoCD.Healthy = %d, want 5", snap.ArgoCD.Data.Healthy)
	}
}

func TestHandleHealthz_OKWhenAllFresh(t *testing.T) {
	now := time.Now()
	s := NewState()
	s.SetArgoCD(ArgoCDData{}, now)
	s.SetLonghorn(LonghornData{}, now)
	s.SetCerts(CertsData{}, now)
	s.SetRestarts(RestartsData{}, now)

	h := NewHandlers(s, testTemplates(t), func() time.Time { return now })
	h.processStart = now.Add(-10 * time.Minute)

	rec := httptest.NewRecorder()
	h.HandleHealthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleHealthz_503WhenSourceStaleBeyond90s(t *testing.T) {
	now := time.Now()
	s := NewState()
	s.SetArgoCD(ArgoCDData{}, now.Add(-2*time.Minute)) // > 90s
	s.SetLonghorn(LonghornData{}, now)
	s.SetCerts(CertsData{}, now)
	s.SetRestarts(RestartsData{}, now)

	h := NewHandlers(s, testTemplates(t), func() time.Time { return now })
	h.processStart = now.Add(-10 * time.Minute)

	rec := httptest.NewRecorder()
	h.HandleHealthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestHandleHealthz_GraceWithin30sOfStart(t *testing.T) {
	now := time.Now()
	// State has never loaded any source. Process started 5s ago.
	s := NewState()
	h := NewHandlers(s, testTemplates(t), func() time.Time { return now })
	h.processStart = now.Add(-5 * time.Second)

	rec := httptest.NewRecorder()
	h.HandleHealthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status during init grace = %d, want 200", rec.Code)
	}
}

func TestHandleIndex_ThemeQuery(t *testing.T) {
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 7}, time.Now())
	h := NewHandlers(s, testTemplates(t), time.Now)

	for _, theme := range []string{"crt", "modern"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/?theme="+theme, nil)
		h.HandleIndex(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("theme=%s: status = %d", theme, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `data-theme="`+theme+`"`) {
			t.Errorf("theme=%s: body missing data-theme attribute. body=%q", theme, rec.Body.String())
		}
	}
}

func TestHandleIndex_DefaultThemeIsCRT(t *testing.T) {
	s := NewState()
	h := NewHandlers(s, testTemplates(t), time.Now)
	rec := httptest.NewRecorder()
	h.HandleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), `data-theme="crt"`) {
		t.Errorf("default theme not crt. body=%q", rec.Body.String())
	}
}

func TestHandleIndex_InvalidThemeFallsBackToCRT(t *testing.T) {
	s := NewState()
	h := NewHandlers(s, testTemplates(t), time.Now)
	rec := httptest.NewRecorder()
	h.HandleIndex(rec, httptest.NewRequest(http.MethodGet, "/?theme=neon", nil))
	if !strings.Contains(rec.Body.String(), `data-theme="crt"`) {
		t.Errorf("invalid theme should fall back to crt. body=%q", rec.Body.String())
	}
}

func TestHandleIndex_AllGreenShowsBanner(t *testing.T) {
	now := time.Now()
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 1}, now)
	s.SetLonghorn(LonghornData{Healthy: 1}, now)
	s.SetCerts(CertsData{Total: 0}, now)
	s.SetRestarts(RestartsData{Total: 0}, now)

	h := NewHandlers(s, testTemplates(t), func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.HandleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), "CLUSTER OK") {
		t.Errorf("AllGreen banner missing. body=%q", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test, expect failure**

```bash
go test ./cmd/cluster-tv/...
```

Expected: build error — `undefined: NewHandlers`.

- [ ] **Step 3: Implement `handlers.go`**

Create `cmd/cluster-tv/handlers.go`:

```go
package main

import (
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// healthzWindow is the heartbeat freshness threshold for /healthz. The
// stalenessWindow constant in state.go (5 min) is for "is this tile
// trustworthy"; healthz is a tighter "is the binary alive" signal.
const healthzWindow = 90 * time.Second

// initGrace is how long after process start /healthz tolerates sources
// that have not yet loaded. Matches the 30s init phase in the spec.
const initGrace = 30 * time.Second

type Handlers struct {
	state        *State
	tpl          *template.Template
	now          func() time.Time
	processStart time.Time

	// metrics
	pollTotal       *prometheus.CounterVec
	lastSuccessSecs *prometheus.GaugeVec
	renderDuration  prometheus.Histogram
}

func NewHandlers(s *State, tpl *template.Template, now func() time.Time) *Handlers {
	return &Handlers{
		state:        s,
		tpl:          tpl,
		now:          now,
		processStart: now(),
		pollTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "cluster_tv_source_poll_total"},
			[]string{"source", "result"},
		),
		lastSuccessSecs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "cluster_tv_source_last_success_seconds"},
			[]string{"source"},
		),
		renderDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{Name: "cluster_tv_render_duration_seconds"},
		),
	}
}

// Register registers metric collectors with the given registry. Tests can
// pass a fresh registry to avoid global-state collisions.
func (h *Handlers) Register(reg prometheus.Registerer) {
	reg.MustRegister(h.pollTotal, h.lastSuccessSecs, h.renderDuration)
}

// MetricsHandler returns the http.Handler for /metrics. Use the same
// registry that was passed to Register.
func (h *Handlers) MetricsHandler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

// PollTotal lets aggregator goroutines record their success/failure counts.
func (h *Handlers) PollTotal(source, result string) {
	h.pollTotal.WithLabelValues(source, result).Inc()
}

// LastSuccessSeconds publishes the per-source heartbeat age.
func (h *Handlers) LastSuccessSeconds(source string, seconds float64) {
	h.lastSuccessSecs.WithLabelValues(source).Set(seconds)
}

// ---------- /api/state ----------

func (h *Handlers) HandleAPIState(w http.ResponseWriter, _ *http.Request) {
	snap := h.state.Snapshot()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snap)
}

// ---------- /healthz ----------

// HandleHealthz returns 200 iff every loaded source has a heartbeat newer
// than healthzWindow OR the process is still in its init grace and the
// source has never loaded. 503 otherwise.
func (h *Handlers) HandleHealthz(w http.ResponseWriter, _ *http.Request) {
	now := h.now()
	snap := h.state.Snapshot()
	inGrace := now.Sub(h.processStart) < initGrace

	stale := func(slot interface {
		// We can't reference the generic Slot[T] without specifying T; build
		// a small inline check by reading the same fields by name.
	}) bool { return false }
	_ = stale

	check := func(loaded bool, last time.Time) bool {
		if !loaded {
			return inGrace
		}
		return now.Sub(last) <= healthzWindow
	}

	ok := check(snap.ArgoCD.Loaded, snap.ArgoCD.LastSuccess) &&
		check(snap.Longhorn.Loaded, snap.Longhorn.LastSuccess) &&
		check(snap.Certs.Loaded, snap.Certs.LastSuccess) &&
		check(snap.Restarts.Loaded, snap.Restarts.LastSuccess)

	if !ok {
		http.Error(w, "stale source", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

// ---------- / ----------

type indexData struct {
	Theme    string
	S        Snapshot
	AllGreen bool
	Stale    int
	Now      time.Time
}

func (h *Handlers) HandleIndex(w http.ResponseWriter, r *http.Request) {
	start := h.now()
	defer func() { h.renderDuration.Observe(time.Since(start).Seconds()) }()

	theme := r.URL.Query().Get("theme")
	if theme != "modern" {
		theme = "crt" // default + fallback for invalid values
	}

	now := h.now()
	data := indexData{
		Theme:    theme,
		S:        h.state.Snapshot(),
		AllGreen: h.state.AllGreen(now),
		Stale:    h.state.StaleCount(now),
		Now:      now,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 4: Run tests, expect pass**

```bash
go test -race ./cmd/cluster-tv/...
```

Expected: all sub-tests pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/cluster-tv/handlers.go cmd/cluster-tv/handlers_test.go
git commit -m "feat(cluster-tv): HTTP handlers (/, /api/state, /healthz, /metrics)"
```

---

### Task 11: `web/cluster-tv/` — templates and CSS

The page is server-rendered HTML with a tiny vanilla JS poll loop. Two CSS themes; theme switching driven entirely by the `data-theme` attribute on `<html>`. JS only has to fetch `/api/state` and replace tile bodies — no DOM diffing.

**Files:**
- Create: `web/cluster-tv/embed.go`
- Create: `web/cluster-tv/index.html.tmpl`
- Create: `web/cluster-tv/crt.css`
- Create: `web/cluster-tv/modern.css`

- [ ] **Step 0: Add the `web/cluster-tv` embed package**

`go:embed` cannot reach across `..`, so we put a tiny package inside `web/cluster-tv/` whose only job is to expose the embedded files. This keeps the spec's `web/<name>/` layout (compatible with the Pod-Tamagotchi follow-up that adds `web/tamagotchi/` later) while letting `cmd/cluster-tv/main.go` consume them via a normal import.

Create `web/cluster-tv/embed.go`:

```go
// Package webclustertv exposes the static assets for cluster-tv as an
// embed.FS. Keeping the embed in this package — rather than in cmd/cluster-tv —
// means the asset directory tree stays under web/, matching the layout of
// every other tool in this repo.
package webclustertv

import "embed"

//go:embed index.html.tmpl crt.css modern.css
var FS embed.FS
```

- [ ] **Step 1: Write `index.html.tmpl`**

Create `web/cluster-tv/index.html.tmpl`:

```html
<!doctype html>
<html lang="en" data-theme="{{.Theme}}">
<head>
<meta charset="utf-8">
<title>cluster-tv</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="icon" href="data:,">
<style>{{template "crt-css" .}}{{template "modern-css" .}}</style>
</head>
<body>
<header>
  <h1>cluster-tv</h1>
  <nav>
    <a href="?theme=crt"  class="theme-link" data-theme-target="crt">CRT</a>
    <a href="?theme=modern" class="theme-link" data-theme-target="modern">MODERN</a>
  </nav>
</header>

{{if .AllGreen}}
<section id="ok-banner" class="banner ok">
  <h2>CLUSTER OK</h2>
  <p>All signals nominal · {{.Now.Format "15:04:05 MST"}}</p>
</section>
{{else}}
<main id="grid" class="grid">
  <section class="tile" data-source="argocd">
    <h2>ArgoCD{{if .S.ArgoCD.LastError}} <span class="warn" title="{{.S.ArgoCD.LastError}}">⚠</span>{{end}}</h2>
    <div class="counts">
      <span class="ok">{{.S.ArgoCD.Data.Healthy}} healthy</span>
      <span class="bad">{{.S.ArgoCD.Data.Degraded}} degraded</span>
      <span class="bad">{{.S.ArgoCD.Data.OutOfSync}} out-of-sync</span>
    </div>
    {{if .S.ArgoCD.Data.Bad}}<ul class="badlist">{{range .S.ArgoCD.Data.Bad}}<li>{{.Name}} ({{.Sync}} / {{.Health}})</li>{{end}}</ul>{{end}}
  </section>

  <section class="tile" data-source="longhorn">
    <h2>Longhorn{{if .S.Longhorn.LastError}} <span class="warn" title="{{.S.Longhorn.LastError}}">⚠</span>{{end}}</h2>
    <div class="counts">
      <span class="ok">{{.S.Longhorn.Data.Healthy}} healthy</span>
      <span class="bad">{{.S.Longhorn.Data.Degraded}} degraded</span>
      <span class="bad">{{.S.Longhorn.Data.Faulted}} faulted</span>
    </div>
  </section>

  <section class="tile" data-source="certs">
    <h2>Certs{{if .S.Certs.LastError}} <span class="warn" title="{{.S.Certs.LastError}}">⚠</span>{{end}}</h2>
    <div class="counts">
      <span class="{{if eq .S.Certs.Data.Total 0}}ok{{else}}bad{{end}}">{{.S.Certs.Data.Total}} expiring &lt; 30d</span>
    </div>
    {{if .S.Certs.Data.Expiring}}<ul class="badlist">{{range .S.Certs.Data.Expiring}}<li>{{.Namespace}}/{{.Name}} · {{.NotAfter}}</li>{{end}}</ul>{{end}}
  </section>

  <section class="tile" data-source="restarts">
    <h2>Pod restarts{{if .S.Restarts.LastError}} <span class="warn" title="{{.S.Restarts.LastError}}">⚠</span>{{end}}</h2>
    <div class="counts">
      <span class="{{if eq .S.Restarts.Data.Total 0}}ok{{else}}bad{{end}}">{{.S.Restarts.Data.Total}} pods restarted &gt; 5× / 24h</span>
    </div>
    {{if .S.Restarts.Data.Pods}}<ul class="badlist">{{range .S.Restarts.Data.Pods}}<li>{{.Namespace}}/{{.Pod}}/{{.Container}} · {{.Restarts}}</li>{{end}}</ul>{{end}}
  </section>
</main>
{{end}}

{{if gt .Stale 0}}<footer class="stale-banner">{{.Stale}} source(s) stale</footer>{{end}}

<script>
(function(){
  // theme persistence
  const links = document.querySelectorAll('.theme-link');
  const stored = localStorage.getItem('clustertv-theme');
  const params = new URLSearchParams(location.search);
  if (params.has('theme')) {
    localStorage.setItem('clustertv-theme', params.get('theme'));
  } else if (stored && stored !== document.documentElement.dataset.theme) {
    location.search = '?theme=' + encodeURIComponent(stored);
    return;
  }

  // 30-second poll loop. Replaces grid innerHTML on every tick so we don't
  // need a DOM-diffing library. The body gets the same theme on the way back.
  async function tick(){
    try {
      const r = await fetch('/api/state', {cache:'no-store'});
      if (!r.ok) return;
      // Re-fetch the rendered page (cheap, server-rendered) to pick up the
      // server's already-templated state. This keeps the JS path tiny.
      const html = await fetch(location.href, {cache:'no-store'}).then(x => x.text());
      const doc = new DOMParser().parseFromString(html, 'text/html');
      const newGrid = doc.getElementById('grid');
      const newBanner = doc.getElementById('ok-banner');
      const grid = document.getElementById('grid');
      const banner = document.getElementById('ok-banner');
      if (newBanner && !banner) location.reload();
      else if (banner && !newBanner) location.reload();
      else if (grid && newGrid) grid.innerHTML = newGrid.innerHTML;
    } catch (_) {}
  }
  setInterval(tick, 30000);
})();
</script>
</body>
</html>

{{define "crt-css"}}{{/* injected from crt.css at build time via embed */}}{{end}}
{{define "modern-css"}}{{/* injected from modern.css at build time via embed */}}{{end}}
```

The `{{template "crt-css"}}` placeholders look weird because we *don't* want both CSS bundles inline; we'll instead emit only the active theme's CSS. To keep this template stable, replace the `<style>` line in main.go (Task 12) by a function call that injects `crt.css` or `modern.css` directly. Update the template:

Replace the `<style>...</style>` line with:

```html
<style>{{.CSS}}</style>
```

And remove the trailing two `{{define}}` lines. Final intended file is:

```html
<!doctype html>
<html lang="en" data-theme="{{.Theme}}">
<head>
<meta charset="utf-8">
<title>cluster-tv</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="icon" href="data:,">
<style>{{.CSS}}</style>
</head>
<body>
<header>
  <h1>cluster-tv</h1>
  <nav>
    <a href="?theme=crt"  class="theme-link" data-theme-target="crt">CRT</a>
    <a href="?theme=modern" class="theme-link" data-theme-target="modern">MODERN</a>
  </nav>
</header>

{{if .AllGreen}}
<section id="ok-banner" class="banner ok">
  <h2>CLUSTER OK</h2>
  <p>All signals nominal · {{.Now.Format "15:04:05 MST"}}</p>
</section>
{{else}}
<main id="grid" class="grid">
  <section class="tile" data-source="argocd">
    <h2>ArgoCD{{if .S.ArgoCD.LastError}} <span class="warn" title="{{.S.ArgoCD.LastError}}">⚠</span>{{end}}</h2>
    <div class="counts">
      <span class="ok">{{.S.ArgoCD.Data.Healthy}} healthy</span>
      <span class="bad">{{.S.ArgoCD.Data.Degraded}} degraded</span>
      <span class="bad">{{.S.ArgoCD.Data.OutOfSync}} out-of-sync</span>
    </div>
    {{if .S.ArgoCD.Data.Bad}}<ul class="badlist">{{range .S.ArgoCD.Data.Bad}}<li>{{.Name}} ({{.Sync}} / {{.Health}})</li>{{end}}</ul>{{end}}
  </section>

  <section class="tile" data-source="longhorn">
    <h2>Longhorn{{if .S.Longhorn.LastError}} <span class="warn" title="{{.S.Longhorn.LastError}}">⚠</span>{{end}}</h2>
    <div class="counts">
      <span class="ok">{{.S.Longhorn.Data.Healthy}} healthy</span>
      <span class="bad">{{.S.Longhorn.Data.Degraded}} degraded</span>
      <span class="bad">{{.S.Longhorn.Data.Faulted}} faulted</span>
    </div>
  </section>

  <section class="tile" data-source="certs">
    <h2>Certs{{if .S.Certs.LastError}} <span class="warn" title="{{.S.Certs.LastError}}">⚠</span>{{end}}</h2>
    <div class="counts">
      <span class="{{if eq .S.Certs.Data.Total 0}}ok{{else}}bad{{end}}">{{.S.Certs.Data.Total}} expiring &lt; 30d</span>
    </div>
    {{if .S.Certs.Data.Expiring}}<ul class="badlist">{{range .S.Certs.Data.Expiring}}<li>{{.Namespace}}/{{.Name}} · {{.NotAfter}}</li>{{end}}</ul>{{end}}
  </section>

  <section class="tile" data-source="restarts">
    <h2>Pod restarts{{if .S.Restarts.LastError}} <span class="warn" title="{{.S.Restarts.LastError}}">⚠</span>{{end}}</h2>
    <div class="counts">
      <span class="{{if eq .S.Restarts.Data.Total 0}}ok{{else}}bad{{end}}">{{.S.Restarts.Data.Total}} pods restarted &gt; 5× / 24h</span>
    </div>
    {{if .S.Restarts.Data.Pods}}<ul class="badlist">{{range .S.Restarts.Data.Pods}}<li>{{.Namespace}}/{{.Pod}}/{{.Container}} · {{.Restarts}}</li>{{end}}</ul>{{end}}
  </section>
</main>
{{end}}

{{if gt .Stale 0}}<footer class="stale-banner">{{.Stale}} source(s) stale</footer>{{end}}

<script>
(function(){
  const params = new URLSearchParams(location.search);
  if (params.has('theme')) {
    localStorage.setItem('clustertv-theme', params.get('theme'));
  } else {
    const stored = localStorage.getItem('clustertv-theme');
    if (stored && stored !== document.documentElement.dataset.theme) {
      location.search = '?theme=' + encodeURIComponent(stored);
      return;
    }
  }
  async function tick(){
    try {
      const html = await fetch(location.href, {cache:'no-store'}).then(r => r.text());
      const doc = new DOMParser().parseFromString(html, 'text/html');
      const oldBody = document.body;
      const newBody = doc.body;
      // Same banner-vs-grid? swap inner; else hard reload to avoid drift.
      const wasOK = !!document.getElementById('ok-banner');
      const isOK = !!doc.getElementById('ok-banner');
      if (wasOK !== isOK) { location.reload(); return; }
      oldBody.innerHTML = newBody.innerHTML;
    } catch (_) {}
  }
  setInterval(tick, 30000);
})();
</script>
</body>
</html>
```

The point of using `.CSS` injection rather than two embedded CSS files is to keep the rendered page small (only one theme's CSS reaches the browser). main.go reads the right CSS file from the `embed.FS` and adds it to `indexData` before rendering.

The above template string is the **final** template content. Write that file (no need to keep the earlier draft).

- [ ] **Step 2: Update `indexData` in `handlers.go`**

Edit `cmd/cluster-tv/handlers.go` and add the `CSS` field:

```go
type indexData struct {
	Theme    string
	CSS      template.CSS
	S        Snapshot
	AllGreen bool
	Stale    int
	Now      time.Time
}
```

(The `template.CSS` type prevents `html/template` from escaping the inline CSS.)

Add a struct field on `Handlers` for the two CSS strings, plus a constructor parameter:

```go
type Handlers struct {
	state        *State
	tpl          *template.Template
	now          func() time.Time
	processStart time.Time
	cssCRT       template.CSS
	cssModern    template.CSS

	pollTotal       *prometheus.CounterVec
	lastSuccessSecs *prometheus.GaugeVec
	renderDuration  prometheus.Histogram
}

func NewHandlers(s *State, tpl *template.Template, now func() time.Time) *Handlers {
	return &Handlers{
		state:        s,
		tpl:          tpl,
		now:          now,
		processStart: now(),
		// CSS defaults are empty; main.go calls SetCSS to inject the embedded files.
		pollTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "cluster_tv_source_poll_total"},
			[]string{"source", "result"},
		),
		lastSuccessSecs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "cluster_tv_source_last_success_seconds"},
			[]string{"source"},
		),
		renderDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{Name: "cluster_tv_render_duration_seconds"},
		),
	}
}

func (h *Handlers) SetCSS(crt, modern string) {
	h.cssCRT = template.CSS(crt)
	h.cssModern = template.CSS(modern)
}
```

And update `HandleIndex` to pick the right CSS:

```go
func (h *Handlers) HandleIndex(w http.ResponseWriter, r *http.Request) {
	start := h.now()
	defer func() { h.renderDuration.Observe(time.Since(start).Seconds()) }()

	theme := r.URL.Query().Get("theme")
	if theme != "modern" {
		theme = "crt"
	}

	now := h.now()
	css := h.cssCRT
	if theme == "modern" {
		css = h.cssModern
	}
	data := indexData{
		Theme:    theme,
		CSS:      css,
		S:        h.state.Snapshot(),
		AllGreen: h.state.AllGreen(now),
		Stale:    h.state.StaleCount(now),
		Now:      now,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.tpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

The handler test in Task 10 already uses a stand-in template that does not reference `.CSS`, so it continues to pass. Verify with `go test ./cmd/cluster-tv/...`.

- [ ] **Step 3: Write `crt.css`**

Create `web/cluster-tv/crt.css`:

```css
:root {
  --bg: #001000;
  --fg: #2bff2b;
  --bg-glow: #003800;
  --bad: #ff6b3d;
  --warn: #ffe14a;
  --tile-bg: rgba(0, 32, 0, 0.6);
  font-family: "VT323", "Courier New", monospace;
}

html[data-theme="crt"] {
  background: var(--bg);
  color: var(--fg);
}

html[data-theme="crt"] body {
  margin: 0;
  padding: 1.5rem;
  min-height: 100vh;
  background:
    repeating-linear-gradient(
      to bottom,
      rgba(0,0,0,0) 0,
      rgba(0,0,0,0) 2px,
      rgba(0,0,0,0.25) 2px,
      rgba(0,0,0,0.25) 3px
    ),
    radial-gradient(ellipse at center, var(--bg-glow) 0%, var(--bg) 80%);
  text-shadow: 0 0 4px var(--fg), 0 0 12px rgba(43, 255, 43, 0.5);
  letter-spacing: 0.05em;
}

html[data-theme="crt"] header {
  display: flex; justify-content: space-between; align-items: baseline;
  border-bottom: 1px solid var(--fg);
  margin-bottom: 1.5rem; padding-bottom: 0.5rem;
}

html[data-theme="crt"] h1 { font-size: 2rem; margin: 0; letter-spacing: 0.2em; }
html[data-theme="crt"] nav a { color: var(--fg); text-decoration: none; margin-left: 1rem; opacity: 0.7; }
html[data-theme="crt"] nav a:hover { opacity: 1; }

html[data-theme="crt"] .grid {
  display: grid; gap: 1rem;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
}

html[data-theme="crt"] .tile {
  background: var(--tile-bg);
  border: 1px solid var(--fg);
  padding: 1rem;
}

html[data-theme="crt"] .tile h2 { margin: 0 0 0.5rem; font-size: 1.2rem; letter-spacing: 0.1em; }
html[data-theme="crt"] .counts span { display: inline-block; margin-right: 1rem; }
html[data-theme="crt"] .counts .bad { color: var(--bad); }
html[data-theme="crt"] .counts .ok { color: var(--fg); }
html[data-theme="crt"] .badlist { margin: 0.5rem 0 0; padding-left: 1rem; font-size: 0.9rem; }
html[data-theme="crt"] .warn { color: var(--warn); }

html[data-theme="crt"] .banner.ok {
  text-align: center; padding: 4rem 1rem;
  border: 2px solid var(--fg);
  background: var(--tile-bg);
}
html[data-theme="crt"] .banner.ok h2 { font-size: 6rem; margin: 0; letter-spacing: 0.3em; }
html[data-theme="crt"] .banner.ok p { opacity: 0.7; }

html[data-theme="crt"] .stale-banner {
  position: fixed; bottom: 0; left: 0; right: 0;
  background: rgba(255,225,74,0.15);
  color: var(--warn);
  text-align: center; padding: 0.5rem; border-top: 1px solid var(--warn);
}

@keyframes flicker { 0%,100%{opacity:1} 1%{opacity:0.94} 3%{opacity:0.97} }
html[data-theme="crt"] body { animation: flicker 1.5s infinite; }
```

- [ ] **Step 4: Write `modern.css`**

Create `web/cluster-tv/modern.css`:

```css
:root {
  --bg: #0f1115;
  --fg: #e6e8ee;
  --muted: #8a90a0;
  --ok: #4ade80;
  --bad: #f87171;
  --warn: #fbbf24;
  --tile-bg: #161a22;
  font-family: -apple-system, system-ui, "Segoe UI", sans-serif;
}

html[data-theme="modern"] {
  background: var(--bg);
  color: var(--fg);
}
html[data-theme="modern"] body { margin: 0; padding: 1.5rem; min-height: 100vh; }
html[data-theme="modern"] header {
  display: flex; justify-content: space-between; align-items: baseline;
  margin-bottom: 1.5rem;
}
html[data-theme="modern"] h1 { font-size: 1.5rem; margin: 0; font-weight: 600; }
html[data-theme="modern"] nav a {
  color: var(--muted); text-decoration: none; margin-left: 1rem;
  font-size: 0.85rem; text-transform: uppercase; letter-spacing: 0.1em;
}
html[data-theme="modern"] nav a:hover { color: var(--fg); }

html[data-theme="modern"] .grid {
  display: grid; gap: 0.75rem;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
}
html[data-theme="modern"] .tile {
  background: var(--tile-bg);
  border-radius: 8px; padding: 1rem;
  box-shadow: 0 1px 0 rgba(255,255,255,0.04) inset;
}
html[data-theme="modern"] .tile h2 { margin: 0 0 0.5rem; font-size: 1rem; font-weight: 600; color: var(--muted); }
html[data-theme="modern"] .counts span { display: inline-block; margin-right: 1rem; font-variant-numeric: tabular-nums; }
html[data-theme="modern"] .counts .ok { color: var(--ok); }
html[data-theme="modern"] .counts .bad { color: var(--bad); }
html[data-theme="modern"] .badlist { margin: 0.5rem 0 0; padding-left: 1rem; font-size: 0.85rem; color: var(--muted); }
html[data-theme="modern"] .warn { color: var(--warn); }

html[data-theme="modern"] .banner.ok {
  text-align: center; padding: 5rem 1rem;
  background: linear-gradient(180deg, rgba(74,222,128,0.08), transparent);
  border-radius: 8px;
}
html[data-theme="modern"] .banner.ok h2 { font-size: 4rem; margin: 0; color: var(--ok); font-weight: 700; }
html[data-theme="modern"] .banner.ok p { color: var(--muted); }

html[data-theme="modern"] .stale-banner {
  position: fixed; bottom: 0; left: 0; right: 0;
  background: var(--tile-bg);
  color: var(--warn); text-align: center;
  padding: 0.5rem; border-top: 1px solid rgba(251,191,36,0.3);
}
```

- [ ] **Step 5: Run handler tests, expect pass**

```bash
go test -race ./cmd/cluster-tv/...
```

Expected: pass — the handler tests use the stand-in template so the new `.CSS` field on `indexData` doesn't break them.

- [ ] **Step 6: Commit**

```bash
git add web/cluster-tv/ cmd/cluster-tv/handlers.go
git commit -m "feat(cluster-tv): HTML template + CRT/modern themes"
```

---

### Task 12: `main.go` — wire-up and signal handling

`main.go` reads env (`ARGOCD_URL`, `ARGOCD_TOKEN`, `PROMETHEUS_URL`, `PORT`), constructs everything, embeds the `web/cluster-tv/*` files, starts the four source goroutines, mounts the routes, and shuts down cleanly on SIGTERM.

**Files:**
- Create: `cmd/cluster-tv/main.go`

- [ ] **Step 1: Write `main.go`**

Create `cmd/cluster-tv/main.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/madic-creates/homelab-toys/internal/argocd"
	"github.com/madic-creates/homelab-toys/internal/certs"
	"github.com/madic-creates/homelab-toys/internal/kube"
	"github.com/madic-creates/homelab-toys/internal/prom"
	webclustertv "github.com/madic-creates/homelab-toys/web/cluster-tv"
	"github.com/prometheus/client_golang/prometheus"
)

const tickInterval = 20 * time.Second
const certWindow = 30 * 24 * time.Hour

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	if err := run(context.Background()); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(parent context.Context) error {
	port := envOrDefault("PORT", "8080")
	argoURL := mustEnv("ARGOCD_URL")
	argoToken := mustEnv("ARGOCD_TOKEN")
	promURL := mustEnv("PROMETHEUS_URL")

	cs, dyn, err := kube.NewInCluster()
	if err != nil {
		return fmt.Errorf("kube clients: %w", err)
	}
	_ = cs // typed clientset reserved for future use; cert-manager goes via dynamic

	state := NewState()
	httpClient := &http.Client{Timeout: 10 * time.Second}

	argoCli := argocd.NewClient(argoURL, argoToken, httpClient)
	promCli := prom.NewClient(promURL, httpClient)
	certLister := certs.NewLister(dyn)

	tpl, err := template.ParseFS(webclustertv.FS, "index.html.tmpl")
	if err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}
	crtCSS, err := webclustertv.FS.ReadFile("crt.css")
	if err != nil {
		return fmt.Errorf("read crt.css: %w", err)
	}
	modernCSS, err := webclustertv.FS.ReadFile("modern.css")
	if err != nil {
		return fmt.Errorf("read modern.css: %w", err)
	}

	now := time.Now
	handlers := NewHandlers(state, tpl, now)
	handlers.SetCSS(string(crtCSS), string(modernCSS))

	reg := prometheus.NewRegistry()
	handlers.Register(reg)

	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go runSource(ctx, "argocd",   MakeArgoCDPoll(argoCli, state, now), tickInterval)
	go runSource(ctx, "longhorn", MakeLonghornPoll(promCli, state, now), tickInterval)
	go runSource(ctx, "restarts", MakeRestartsPoll(promCli, state, now), tickInterval)
	go runSource(ctx, "certs",    MakeCertsPoll(certLister, certWindow, state, now), tickInterval)

	mux := http.NewServeMux()
	mux.HandleFunc("/", handlers.HandleIndex)
	mux.HandleFunc("/api/state", handlers.HandleAPIState)
	mux.HandleFunc("/healthz", handlers.HandleHealthz)
	mux.Handle("/metrics", handlers.MetricsHandler(reg))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer c2()
		return srv.Shutdown(shutdownCtx)
	}
}

func envOrDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		slog.Error("required env var missing", "key", k)
		os.Exit(2)
	}
	return v
}
```

- [ ] **Step 2: Verify build**

Run:

```bash
go build ./cmd/cluster-tv/
```

Expected: succeeds. The embed lives in the `web/cluster-tv` package (Task 11 Step 0), so this main.go uses a normal import — no `..` in any embed directive.

- [ ] **Step 3: Run all tests**

```bash
go test -race ./...
go vet ./...
```

Expected: all green.

- [ ] **Step 4: Smoke-run locally**

The binary expects in-cluster config, so a real run from the laptop will fail at `kube.NewInCluster`. This is fine — the goal here is to verify the binary at least *starts up*, parses templates, and serves the four routes if config is available. Skip running it locally; rely on the CI build + cluster verification (Phase 5) instead.

- [ ] **Step 5: Commit**

```bash
git add cmd/cluster-tv/main.go
git commit -m "feat(cluster-tv): main entrypoint with embedded templates"
```

- [ ] **Step 6: Push and verify CI**

```bash
git push origin main
```

Expected: GitHub Actions runs `test`, `lint`, then `build-cluster-tv`. The first push lands a green run that publishes `ghcr.io/madic-creates/cluster-tv:latest` and a SHA-tagged variant.

If `test` or `lint` fails: read the log, fix locally, commit, push again.

---

## Phase 4 — Kubernetes Manifests in `k3s-git-ops`

These tasks happen in the **other** repo: `/home/mne-adm/Git/personal/github.com/k3s-git-ops/`. Open a feature branch there.

### Task 13: `apps/cluster-tv/` — RBAC, Service, Deployment, Kustomization

**Files (in `k3s-git-ops`):**
- Create: `apps/cluster-tv/kustomization.yaml`
- Create: `apps/cluster-tv/k8s.rbac.yaml`
- Create: `apps/cluster-tv/k8s.service.yaml`
- Create: `apps/cluster-tv/k8s.deployment.yaml`

- [ ] **Step 1: Create branch and directory**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
git switch -c add-cluster-tv
mkdir -p apps/cluster-tv
```

- [ ] **Step 2: Write `kustomization.yaml`**

Create `apps/cluster-tv/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: monitoring
resources:
  - k8s.rbac.yaml
  - k8s.service.yaml
  - k8s.deployment.yaml
  - k8s.np.cluster-tv-default-deny.yaml
  - k8s.np.cluster-tv.yaml
  - cluster-tv.ingress.enc.yaml
generators:
  - kustomize-secret-generator.yaml
```

- [ ] **Step 3: Write `k8s.rbac.yaml`**

Create `apps/cluster-tv/k8s.rbac.yaml`:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: cluster-tv
  namespace: monitoring
  annotations:
    argocd.argoproj.io/sync-wave: "1"
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cluster-tv
  annotations:
    argocd.argoproj.io/sync-wave: "1"
rules:
  - apiGroups: ["cert-manager.io"]
    resources: ["certificates"]
    verbs: ["list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: cluster-tv
  annotations:
    argocd.argoproj.io/sync-wave: "1"
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-tv
subjects:
  - kind: ServiceAccount
    name: cluster-tv
    namespace: monitoring
```

- [ ] **Step 4: Write `k8s.service.yaml`**

Create `apps/cluster-tv/k8s.service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: cluster-tv
  namespace: monitoring
  labels:
    app.kubernetes.io/name: cluster-tv
  annotations:
    argocd.argoproj.io/sync-wave: "9"
spec:
  type: ClusterIP
  ports:
    - port: 8080
      targetPort: http
      name: http
  selector:
    app.kubernetes.io/name: cluster-tv
```

- [ ] **Step 5: Write `k8s.deployment.yaml`**

Pattern lifted from `apps/claude-alert-kubernetes-analyzer/k8s.deployment.yaml`. Note the digest: leave it as `latest` for the very first apply so Renovate can pin it on its next run.

Create `apps/cluster-tv/k8s.deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cluster-tv
  namespace: monitoring
  labels:
    app.kubernetes.io/name: cluster-tv
  annotations:
    argocd.argoproj.io/sync-wave: "10"
spec:
  replicas: 1
  revisionHistoryLimit: 3
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app.kubernetes.io/name: cluster-tv
  template:
    metadata:
      labels:
        app.kubernetes.io/name: cluster-tv
    spec:
      serviceAccountName: cluster-tv
      automountServiceAccountToken: true
      securityContext:
        runAsNonRoot: true
        runAsUser: 65534
        runAsGroup: 65534
        fsGroup: 65534
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: cluster-tv
          image: ghcr.io/madic-creates/cluster-tv:latest  # Renovate will pin to digest after first sync
          ports:
            - containerPort: 8080
              name: http
          env:
            - name: PORT
              value: "8080"
            - name: ARGOCD_URL
              value: "http://argo-cd-argocd-server.argocd.svc.cluster.local:80"
            - name: ARGOCD_TOKEN
              valueFrom:
                secretKeyRef:
                  name: cluster-tv-env
                  key: ARGOCD_TOKEN
            - name: PROMETHEUS_URL
              valueFrom:
                secretKeyRef:
                  name: cluster-tv-env
                  key: PROMETHEUS_URL
          resources:
            requests:
              cpu: 10m
              memory: 32Mi
            limits:
              cpu: 100m
              memory: 64Mi
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 30
            periodSeconds: 30
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
```

- [ ] **Step 6: Validate**

Run from `k3s-git-ops` root:

```bash
kustomize build apps/cluster-tv --enable-helm --enable-alpha-plugins --enable-exec 2>&1 | head -40
```

Expected: prints rendered manifests, no errors. The encrypted resources (`secrets.enc.yaml`, `cluster-tv.ingress.enc.yaml`) and ksops generator are referenced in `kustomization.yaml` but don't exist yet — kustomize will warn or error. Comment those four lines out for this step and re-add them in Tasks 14–16. Easier alternative: fully populate `kustomization.yaml` only after Tasks 14–16, leaving the resources list short for now:

Modify `kustomization.yaml` to start with only what exists today:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: monitoring
resources:
  - k8s.rbac.yaml
  - k8s.service.yaml
  - k8s.deployment.yaml
```

Re-run kustomize — expected: clean output.

- [ ] **Step 7: Commit (in k3s-git-ops)**

```bash
git add apps/cluster-tv/kustomization.yaml \
        apps/cluster-tv/k8s.rbac.yaml \
        apps/cluster-tv/k8s.service.yaml \
        apps/cluster-tv/k8s.deployment.yaml
git commit -m "feat(cluster-tv): add deployment, service, RBAC"
```

---

### Task 14: NetworkPolicies

**Files (in `k3s-git-ops`):**
- Create: `apps/cluster-tv/k8s.np.cluster-tv-default-deny.yaml`
- Create: `apps/cluster-tv/k8s.np.cluster-tv.yaml`

- [ ] **Step 1: Write the default-deny policy**

Create `apps/cluster-tv/k8s.np.cluster-tv-default-deny.yaml`:

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: cluster-tv-default-deny
  namespace: monitoring
  annotations:
    argocd.argoproj.io/sync-wave: "5"
spec:
  endpointSelector:
    matchLabels:
      app.kubernetes.io/name: cluster-tv
  ingress: []
  egress: []
```

- [ ] **Step 2: Write the allow-list policy**

Create `apps/cluster-tv/k8s.np.cluster-tv.yaml`:

```yaml
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: cluster-tv
  namespace: monitoring
  annotations:
    argocd.argoproj.io/sync-wave: "5"
spec:
  endpointSelector:
    matchLabels:
      app.kubernetes.io/name: cluster-tv
  ingress:
    # Traefik fronts the public Ingress.
    - fromEndpoints:
        - matchLabels:
            k8s:io.kubernetes.pod.namespace: traefik
      toPorts:
        - ports:
            - port: "8080"
              protocol: TCP
  egress:
    # DNS
    - toEndpoints:
        - matchLabels:
            k8s:io.cilium.k8s.namespace.labels.kubernetes.io/metadata.name: kube-system
            k8s:io.kubernetes.pod.namespace: kube-system
            k8s:k8s-app: kube-dns
      toPorts:
        - ports:
            - port: "53"
              protocol: UDP
            - port: "53"
              protocol: TCP
    # ArgoCD server
    - toEndpoints:
        - matchLabels:
            k8s:io.kubernetes.pod.namespace: argocd
            k8s:app.kubernetes.io/name: argocd-server
      toPorts:
        - ports:
            - port: "8080"
              protocol: TCP
            - port: "80"
              protocol: TCP
    # Prometheus
    - toEndpoints:
        - matchLabels:
            k8s:io.kubernetes.pod.namespace: monitoring
            k8s:app.kubernetes.io/name: prometheus
      toPorts:
        - ports:
            - port: "9090"
              protocol: TCP
    # Kubernetes API (for cert-manager Certificates)
    - toEntities:
        - kube-apiserver
```

- [ ] **Step 3: Re-enable references in `kustomization.yaml`**

Add the two NP files to `apps/cluster-tv/kustomization.yaml` resources list (after `k8s.deployment.yaml`):

```yaml
  - k8s.np.cluster-tv-default-deny.yaml
  - k8s.np.cluster-tv.yaml
```

Run:

```bash
kustomize build apps/cluster-tv --enable-helm --enable-alpha-plugins --enable-exec
```

Expected: still renders cleanly, now also emits both CiliumNetworkPolicy objects.

- [ ] **Step 4: Commit**

```bash
git add apps/cluster-tv/k8s.np.cluster-tv-default-deny.yaml \
        apps/cluster-tv/k8s.np.cluster-tv.yaml \
        apps/cluster-tv/kustomization.yaml
git commit -m "feat(cluster-tv): network policies"
```

---

### Task 15: SOPS-encrypted secret + ksops generator

**Files (in `k3s-git-ops`):**
- Create: `apps/cluster-tv/secrets.enc.yaml` (encrypted)
- Create: `apps/cluster-tv/kustomize-secret-generator.yaml`

- [ ] **Step 1: Generate the ArgoCD token**

The token is created in ArgoCD itself, against a local user with cluster-wide read RBAC. Steps:

1. Add a local Argo CD user, e.g. via `argocd-cm`:

   ```yaml
   data:
     accounts.cluster-tv: apiKey
   ```

2. Add a custom Argo CD role with cluster-wide app reads in `argocd-rbac-cm`:

   ```yaml
   data:
     policy.csv: |
       p, role:cluster-tv-read, applications, get, */*, allow
       p, role:cluster-tv-read, applications, list, */*, allow
       g, cluster-tv, role:cluster-tv-read
   ```

3. Generate a token via `argocd account generate-token --account cluster-tv`.

This is out-of-band cluster setup — record the resulting token; you'll paste it into the SOPS file in Step 2.

- [ ] **Step 2: Write and encrypt the secret**

Write the cleartext file first (do NOT commit cleartext):

```yaml
# /tmp/cluster-tv-secrets.yaml — DO NOT COMMIT
apiVersion: v1
kind: Secret
metadata:
  name: cluster-tv-env
  namespace: monitoring
type: Opaque
stringData:
  ARGOCD_TOKEN: "<paste token from Step 1>"
  PROMETHEUS_URL: "http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090"
```

Encrypt:

```bash
sops --encrypt /tmp/cluster-tv-secrets.yaml > apps/cluster-tv/secrets.enc.yaml
shred -u /tmp/cluster-tv-secrets.yaml
```

(Use the existing repo's `.sops.yaml` rule for the recipient list; no per-file recipients should be needed. Verify by opening `apps/cluster-tv/secrets.enc.yaml` and confirming the SOPS metadata block at the bottom matches the existing `apps/claude-alert-kubernetes-analyzer/secrets.enc.yaml`.)

- [ ] **Step 3: Write the ksops generator**

Create `apps/cluster-tv/kustomize-secret-generator.yaml`:

```yaml
apiVersion: viaduct.ai/v1
kind: ksops
metadata:
  name: secret-generator
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: ksops
files:
  - secrets.enc.yaml
```

- [ ] **Step 4: Re-enable generator in `kustomization.yaml`**

Add `generators: [- kustomize-secret-generator.yaml]` (already in the Task 13 draft — uncomment if removed).

Run:

```bash
kustomize build apps/cluster-tv --enable-helm --enable-alpha-plugins --enable-exec | grep -A1 'kind: Secret'
```

Expected: emits the decrypted `Secret` named `cluster-tv-env`.

- [ ] **Step 5: Commit**

```bash
git add apps/cluster-tv/secrets.enc.yaml \
        apps/cluster-tv/kustomize-secret-generator.yaml \
        apps/cluster-tv/kustomization.yaml
git commit -m "feat(cluster-tv): SOPS-encrypted env secret"
```

---

### Task 16: Authelia-protected Ingress

**Files (in `k3s-git-ops`):**
- Create: `apps/cluster-tv/cluster-tv.ingress.enc.yaml` (encrypted)

- [ ] **Step 1: Pick the hostname**

Use `cluster-tv.internal.neese-web.de`, matching the spec's pattern. Both `host:` and `tls.hosts:` get encrypted. The middleware annotation references the existing `traefik-redirect@kubernetescrd` and `authelia-forwardauth-authelia@kubernetescrd` middlewares.

- [ ] **Step 2: Write the cleartext Ingress**

Write `/tmp/cluster-tv-ingress.yaml`:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: cluster-tv
  namespace: monitoring
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: web239,websecure239
    traefik.ingress.kubernetes.io/router.tls: "true"
    traefik.ingress.kubernetes.io/router.middlewares: traefik-redirect@kubernetescrd, authelia-forwardauth-authelia@kubernetescrd
spec:
  ingressClassName: traefik
  tls:
    - hosts:
        - cluster-tv.internal.neese-web.de
      secretName: wildcard-cloudflare-production-02
  rules:
    - host: cluster-tv.internal.neese-web.de
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: cluster-tv
                port:
                  number: 8080
```

Cross-check against `apps/whoami/whoami.ingress.enc.yaml` for entrypoint/middleware names — they must match the actual Traefik config in the cluster.

- [ ] **Step 3: Encrypt**

```bash
sops --encrypt /tmp/cluster-tv-ingress.yaml > apps/cluster-tv/cluster-tv.ingress.enc.yaml
shred -u /tmp/cluster-tv-ingress.yaml
```

- [ ] **Step 4: Add to kustomization.yaml**

Append to `resources:` in `apps/cluster-tv/kustomization.yaml`:

```yaml
  - cluster-tv.ingress.enc.yaml
```

`scripts/kubeconform-validate.sh apps/cluster-tv` must pass.

- [ ] **Step 5: Commit**

```bash
git add apps/cluster-tv/cluster-tv.ingress.enc.yaml apps/cluster-tv/kustomization.yaml
git commit -m "feat(cluster-tv): Authelia-protected Ingress"
```

---

### Task 17: ArgoCD Application manifest

**Files (in `k3s-git-ops`):**
- Create: `apps/argo-cd-apps/90-cluster-tv.yaml`
- Modify: `apps/argo-cd-apps/kustomization.yaml`

- [ ] **Step 1: Find the gethomepage Application as a template**

```bash
ls apps/argo-cd-apps/ | grep -i homepage
cat apps/argo-cd-apps/$(ls apps/argo-cd-apps/ | grep -i homepage)
```

Expected: a YAML with `kind: Application`, sync-wave 90 or thereabouts. Use it as a structural template.

- [ ] **Step 2: Write `90-cluster-tv.yaml`**

Create `apps/argo-cd-apps/90-cluster-tv.yaml`. Copy the gethomepage pattern; replace name, path, and sync wave:

```yaml
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: 90-cluster-tv
  namespace: argocd
  finalizers:
    - resources-finalizer.argocd.argoproj.io
  annotations:
    argocd.argoproj.io/sync-wave: "90"
spec:
  project: default
  source:
    # Same repoURL the other apps use — copy verbatim from the gethomepage app.
    repoURL: <COPY FROM GETHOMEPAGE APP>
    path: apps/cluster-tv
    targetRevision: HEAD
  destination:
    server: "https://kubernetes.default.svc"
    namespace: monitoring
  syncPolicy:
    syncOptions:
      - ApplyOutOfSyncOnly=true
      - ServerSideApply=true
    automated:
      prune: true
      selfHeal: true
```

(Replace `<COPY FROM GETHOMEPAGE APP>` with the literal string from the gethomepage Application manifest before committing.)

- [ ] **Step 3: Add to `apps/argo-cd-apps/kustomization.yaml`**

Open the file, add `- 90-cluster-tv.yaml` to the resources list in alphabetical/numerical order.

- [ ] **Step 4: Validate**

```bash
kustomize build apps/argo-cd-apps --enable-helm --enable-alpha-plugins --enable-exec | grep -A1 cluster-tv
```

Expected: emits the Application YAML.

- [ ] **Step 5: Commit**

```bash
git add apps/argo-cd-apps/90-cluster-tv.yaml apps/argo-cd-apps/kustomization.yaml
git commit -m "feat(cluster-tv): register as ArgoCD Application"
```

---

## Phase 5 — Verification

### Task 18: Open PR, merge, verify on cluster

- [ ] **Step 1: Push the `k3s-git-ops` branch and open PR**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
git push -u origin add-cluster-tv
gh pr create --title "feat: cluster-tv" --body "Adds the cluster-tv app from homelab-toys."
```

Wait for CI checks (kubeconform, kustomize-validate). Fix anything that fails.

- [ ] **Step 2: Merge the PR**

After review/CI, merge to main. ArgoCD picks up the new Application in the `apps/argo-cd-apps` app-of-apps and syncs `cluster-tv`.

- [ ] **Step 3: Wait for sync**

```bash
argocd app wait 90-cluster-tv --health
kubectl -n monitoring get pods -l app.kubernetes.io/name=cluster-tv
kubectl -n monitoring logs deploy/cluster-tv | head -50
```

Expected: pod is `Running`, `Ready 1/1`. Logs show four "source poll failed" or "listening" entries — at minimum the `listening` line should appear.

- [ ] **Step 4: Verify endpoints from inside the cluster**

```bash
kubectl -n monitoring run -it --rm curl --image=curlimages/curl --restart=Never -- \
  sh -c 'curl -s http://cluster-tv:8080/healthz; echo; curl -s http://cluster-tv:8080/api/state | head -c 200'
```

Expected: `ok` for healthz and a JSON snippet from `/api/state`.

- [ ] **Step 5: Verify both themes via the public URL**

In a browser at `https://cluster-tv.internal.neese-web.de/?theme=crt` and `?theme=modern`. Confirm Authelia challenge appears once, then both themes render.

- [ ] **Step 6: Force a Degraded ArgoCD app to verify bad-news mode flips**

```bash
# pick a low-impact app, e.g. whoami; pause its source until you see the tile go red.
argocd app set whoami --source-path nonexistent-path
# Wait ~30s, refresh cluster-tv. The CLUSTER OK banner should be replaced
# by the grid with whoami listed under ArgoCD's "Bad" list.
argocd app set whoami --source-path apps/whoami  # restore
```

Expected: tile flips within one polling cycle (~30 s).

- [ ] **Step 7: Smoke `/metrics`**

```bash
kubectl -n monitoring port-forward svc/cluster-tv 8080:8080 &
curl -s http://localhost:8080/metrics | grep cluster_tv_
kill %1
```

Expected: at least the three `cluster_tv_source_poll_total{...}`, `cluster_tv_source_last_success_seconds{...}`, `cluster_tv_render_duration_seconds_*` series are present.

---

## Notes for the implementer

- **Repo conventions matter more than the spec verbatim.** Where the spec said "IngressRoute" but the repo uses plain `Ingress` with Traefik annotations, follow the repo. Where the spec said "image-pinned by Renovate", apply `latest` first and let Renovate's next run land the digest pin (matches every other app in `apps/`).
- **Don't worsen the dep graph.** The spec deliberately uses `dynamic.Interface` for cert-manager so the binary doesn't pull in `cert-manager-io` Go modules. Keep that.
- **Tamagotchi compatibility.** Do not put node-related code in `internal/kube/`. Pod-Tamagotchi will add `internal/kube/nodes.go` *additively*. Do not refactor `internal/kube/client.go` to assume a different shape.
- **Token rotation.** The ArgoCD token is bound to the `cluster-tv` Argo CD account. If you ever revoke it, regenerate, re-encrypt `secrets.enc.yaml`, and ArgoCD's auto-sync will roll the Pod.
- **Longhorn label values.** The Prometheus query in `aggregator.go` assumes `state` label values `healthy/degraded/faulted/unknown`. If the running Longhorn version differs, only the `longhornSamplesToData` switch statement needs to update.
