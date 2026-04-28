# Cluster-TV — Design Spec

**Date:** 2026-04-28
**Status:** Approved, ready for implementation plan
**Implementation order:** First — bootstraps the shared `homelab-toys` repo

## Motivation

The cluster already exposes its operational state through gethomepage (links), Grafana (deep metrics), ArgoCD (sync state), and Hubble UI (network). What is missing is a single low-density "wall display" view that aggregates the few signals that actually matter when a human glances at the cluster: are deployments healthy, are storage volumes safe, are certs alive, are pods stable? Cluster-TV is that glance view, with a deliberate retro-CRT aesthetic to differentiate it from the existing dashboards and to make a "bad-news-only" mode visually satisfying.

## Scope

### In v1

- ArgoCD application health (counts of Healthy / Degraded / OutOfSync, plus the list of non-green apps)
- Longhorn volume state (Healthy / Degraded / Faulted counts)
- cert-manager certificates expiring within 30 days
- Pods with > 5 container restarts in the last 24 hours
- Bad-news-only mode: when all signals are green, the screen shows a large "CLUSTER OK" instead of the kanban tiles
- Theme toggle CRT ↔ modern via `?theme=...` query param, persisted client-side in `localStorage`

### Out of v1 (explicit)

Emby "now playing", Pi-hole stats, historical graphs, alerts, persisted user preferences beyond theme, mobile-optimised layout, dark/light variants beyond the CRT/modern toggle.

## Repo Layout — `homelab-toys` (bootstrapped here)

This spec creates a new public GitHub repo `madic-creates/homelab-toys` with the following layout. The Pod-Tamagotchi spec depends on this repo existing and reuses the `internal/` packages.

```
homelab-toys/
├── cmd/
│   └── cluster-tv/main.go
├── internal/
│   ├── kube/         # rest.InClusterConfig + client-go shared client
│   ├── argocd/       # HTTP client for ArgoCD application API
│   ├── prom/         # Prometheus query helper
│   └── certs/        # cert-manager Certificate watcher
├── web/
│   └── cluster-tv/   # html/template files + handwritten CSS (CRT + modern)
├── Dockerfile.cluster-tv
├── .github/workflows/release.yaml
├── .golangci.yaml
├── go.mod
└── README.md
```

**Stack:** Go 1.23+, plain `net/http`, `html/template`, `log/slog`, no web framework, no JS bundler. Frontend is server-rendered HTML plus minimal vanilla JS for the 30-second polling loop. Handwritten CSS, ≤ 200 lines per app.

**Build / release:** GitHub Actions on push-to-`main` builds the image and pushes `ghcr.io/madic-creates/cluster-tv:latest` plus a SHA-tagged variant. The Dockerfile uses a multi-stage build (golang:1.23-alpine builder, distroless `static-debian12:nonroot` runtime). Renovate in the k3s-git-ops repo pins the digest, identical to `claude-alert-*`.

**Pod-Tamagotchi note:** The Tamagotchi spec adds `cmd/tamagotchi/`, `web/tamagotchi/`, `Dockerfile.tamagotchi`, `internal/health/`, and a parallel CI job. It also extends `internal/kube/` with one **new file** (`nodes.go`) — additive only, no modifications to any source file created by this spec. That separation keeps the Cluster-TV spec implementable without knowing what the Tamagotchi spec will need.

## Architecture

Single-binary `cluster-tv`, 1-replica Deployment in namespace `monitoring`, ServiceAccount pattern identical to `claude-alert-kubernetes-analyzer`. Stateless — no PVC, no DB.

```
                ┌─────────────────────────────────────────┐
                │         cluster-tv (Pod)                │
                │                                         │
   /api/state ──┤  HTTP handlers ◄── State (RWMutex) ◄──┐ │
   /                                                    │ │
   /healthz                                             │ │
   /metrics                                             │ │
                │                                       │ │
                │           Aggregator goroutine ───────┘ │
                │             ▲     ▲       ▲     ▲       │
                └─────────────┼─────┼───────┼─────┼───────┘
                              │     │       │     │
                          ArgoCD  Prom    Prom  Kube
                          (HTTP) (Vol)  (Pods) (Certs)
```

### Endpoints

| Path | Function |
|---|---|
| `GET /` | Server-rendered HTML page with all tiles. Theme via `?theme=crt\|modern`, default `crt`, JS persists choice in `localStorage`. |
| `GET /api/state` | JSON snapshot of the current `State`. Always returns 200, even when sources have stale data. Polled every 30s by the page. |
| `GET /healthz` | 200 if every source goroutine has updated its heartbeat within the last 90 seconds; 503 otherwise. |
| `GET /metrics` | Prometheus metrics: `cluster_tv_source_poll_total{source,result}`, `cluster_tv_source_last_success_seconds{source}`, `cluster_tv_render_duration_seconds`. |

### Data flow

On startup, the binary launches one long-lived polling goroutine per source. Each goroutine runs an internal 20-second `time.Ticker` loop (deliberately faster than the 30-second browser poll, so `/api/state` always serves a fresh snapshot) and writes into its own slot of a shared `State` struct guarded by a `sync.RWMutex`. After each successful or failed poll attempt, the goroutine writes a heartbeat timestamp to its slot. `/api/state` takes a read-lock, marshals the snapshot, releases. `/healthz` returns 503 if any goroutine's heartbeat is older than 90 seconds. Browser JS replaces tile innerHTML on every poll — no DOM diffing library.

### Sources

| Source | Mechanism | Internal package |
|---|---|---|
| ArgoCD apps | `GET /api/v1/applications` with bearer token loaded from secret-mounted env `ARGOCD_TOKEN`. Token belongs to an Argo CD local user (`accounts.<name>: apiKey`) bound to a custom role granting cluster-wide `applications, get, */*, allow` and `applications, list, */*, allow`. A project-scoped role/token is **not** sufficient because Argo CD apps live across multiple projects. | `internal/argocd` |
| Longhorn volumes | Prometheus query against `longhorn_volume_robustness`. Longhorn exposes one time-series per `(volume, state)` combination with sample value `1` for the volume's current state. The query counts series by the `state` label: `count(longhorn_volume_robustness == 1) by (state)`. The spec assumes label values `healthy`, `degraded`, `faulted`, `unknown` — the implementer must verify exact label values against the Longhorn version in use before relying on them. | `internal/prom` |
| Cert-manager | `client-go` lists `cert-manager.io/v1 Certificates` cluster-wide, filters by `status.notAfter < now+30d`. Certificates without a populated `status.notAfter` (e.g. not yet issued or in failure state) are skipped silently — they show up in cert-manager's own metrics if needed. | `internal/certs` |
| Pod restarts | Prometheus query `increase(kube_pod_container_status_restarts_total[24h]) > 5` (kube-state-metrics is part of the existing kube-prometheus-stack). Returns a list of `(namespace, pod, container)` series where the 24-hour restart delta exceeded the threshold. The k8s API alone cannot answer "restarts in the last 24h" — `restartCount` is lifetime-total and `lastState.terminated.finishedAt` only describes the most recent termination, so Prometheus is the right tool for this signal. | `internal/prom` |
| Bad-news-mode | Pure aggregate: `state.AllGreen()` returns true iff every above source is green. | computed in handler |

### Secrets and RBAC

A SOPS-encrypted secret `cluster-tv-env` in the `monitoring` namespace carries `ARGOCD_TOKEN` and the Prometheus URL (the latter just because it keeps the Deployment manifest free of environment-specific values).

ServiceAccount `cluster-tv` needs cluster-wide `list` on:
- `cert-manager.io/v1 Certificates`

That is the entire RBAC surface — pod-restart and Longhorn data come from Prometheus, and ArgoCD data via the ArgoCD API. No write permissions anywhere.

## Error handling

The guiding rule: **a failing source must never break the UI.** Specifically:

- **Source failures retain prior data.** Each source slot in `State` carries `Data`, `LastSuccess time.Time`, `LastError string`. On poll failure, only `LastError` and a `LastFailure` timestamp update; `Data` stays. The UI shows a small ⚠️ on that tile with a tooltip "last update X minutes ago".
- **Stale threshold = 5 minutes.** If `LastSuccess` is older than 5 minutes, the tile renders greyed. Bad-news-mode skips stale sources when computing AllGreen but renders a banner "N source(s) stale".
- **Init phase = first 30s.** All sources start in a "loading" state, tiles show skeleton boxes, bad-news-mode is suppressed.
- **`/api/state` always returns 200** with the current state — broken sources surface inside the JSON, never as HTTP errors. This keeps the polling JS trivial.
- **Logging** via `log/slog` in JSON mode. Source errors are `WARN`. Aggregator panics are recovered and logged at `ERROR`; the affected source goroutine restarts after a 10-second backoff.

## Testing

- **Unit tests per source package.** `httptest.NewServer` for ArgoCD and Prometheus, `k8s.io/client-go/kubernetes/fake` and `cert-manager/.../fake` for the listing sources. Table-driven cases include empty lists, API down, expired-token responses, malformed payloads.
- **Aggregator integration test.** All sources mocked, asserts `State` is consistent under concurrent polls. Run with `-race`.
- **HTTP handler tests.** `httptest.NewRecorder` for `/api/state`; asserts the JSON schema is stable across releases (a golden-file diff check).
- **HTML render smoke tests.** One golden-file per theme, byte-equality on rendered output for a fixed `State` fixture. Catches accidental template breakage, not pixel correctness.
- **No browser E2E.** The CSS theme toggle and the polling loop are simple enough that a unit test on `/api/state` plus a manual Firefox check before merge is sufficient.

**Pre-merge gate:** `go test -race ./...`, `go vet`, `golangci-lint run`, plus `kustomize build apps/cluster-tv --enable-helm --enable-alpha-plugins --enable-exec` and `scripts/kubeconform-validate.sh apps/cluster-tv` from the k3s-git-ops repo side.

## Kubernetes manifests (in k3s-git-ops repo)

Path: `apps/cluster-tv/`. Files:

- `kustomization.yaml` — namespace `monitoring`, sync wave `90` (alongside other UI tools), references resources below plus `kustomize-secret-generator.yaml`.
- `k8s.deployment.yaml` — 1 replica, image `ghcr.io/madic-creates/cluster-tv:latest@sha256:...`, distroless runtime, securityContext `runAsNonRoot: true`, requests/limits in the 50m / 64Mi range.
- `k8s.service.yaml` — ClusterIP, port 8080.
- `k8s.rbac.yaml` — ServiceAccount, ClusterRole (`list` on `cert-manager.io/v1 Certificates` only), ClusterRoleBinding.
- `k8s.np.cluster-tv-default-deny.yaml` + `k8s.np.cluster-tv.yaml` — default-deny plus allow ingress from Traefik and egress to ArgoCD, Prometheus, kube-apiserver.
- `secrets.enc.yaml` — SOPS-encrypted, contains `ARGOCD_TOKEN` and `PROMETHEUS_URL`.
- `kustomize-secret-generator.yaml` — ksops generator referencing the encrypted file.
- `cluster-tv.ingress.enc.yaml` — encrypted IngressRoute on `internal.neese-web.de` entrypoints (Authelia forward-auth applied via the existing middleware), referencing `wildcard-cloudflare-production-02`.

Plus `apps/argo-cd-apps/90-cluster-tv.yaml` (ArgoCD Application manifest, copied from the gethomepage one) and a new entry in `apps/argo-cd-apps/kustomization.yaml`.

## Implementation order

1. Create the `madic-creates/homelab-toys` GitHub repo with `go mod init`, `.golangci.yaml`, `README.md`, `Dockerfile.cluster-tv`, `.github/workflows/release.yaml`.
2. Implement the four `internal/` packages with their tests.
3. Implement `cmd/cluster-tv/main.go`: aggregator goroutine, HTTP handlers, HTML templates, CSS for both themes.
4. First green CI run produces `ghcr.io/madic-creates/cluster-tv:latest`.
5. In k3s-git-ops, add `apps/cluster-tv/` plus the ArgoCD Application manifest. Encrypt the ArgoCD token, configure IngressRoute, apply network policies.
6. Verify on cluster: `/`, `/api/state`, `/metrics`, both themes, bad-news-mode (pause one ArgoCD app to force a Degraded state).
