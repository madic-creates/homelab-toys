# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

A Go monorepo for small, single-purpose tools that run inside `madic-creates`'s
home-lab Kubernetes cluster. Each tool is its own binary and its own container
image, but they share the cluster-integration plumbing under `internal/` so a
new tool boils down to "one `cmd/<name>/`, one `web/<name>/`, one
`Dockerfile.<name>`, one CI matrix entry".

The first (and currently only published) binary is **cluster-tv** — a wall
display that aggregates ArgoCD app health, Longhorn volume state,
cert-manager expiry, and pod restart counts. A second binary,
**tamagotchi**, is specced (`docs/superpowers/specs/`) but not yet
implemented.

`deploy/<tool>/` holds reference Kubernetes manifests for deploying a tool,
with `docs/<tool>-deployment.md` as the narrative companion.

## Commands

| Task | Command |
|---|---|
| All tests | `go test -race ./...` (the `-race` flag is mandatory; goroutine code in `cmd/<tool>/aggregator.go` is race-detector-tested) |
| Single package tests | `go test -race -v ./internal/argocd/...` |
| Single test function | `go test -race -run TestListApplications_Success ./internal/argocd/` |
| Vet | `go vet ./...` |
| Lint | `golangci-lint run` |
| Build all binaries | `go build ./...` |
| Build one binary | `go build ./cmd/cluster-tv/` |
| Pre-commit setup | `pre-commit install` (the hook chain runs golangci-lint + `go test`; the golangci-lint hook tries `$(command -v golangci-lint)` first, falls back to `/go/bin/golangci-lint`) |

CI on push to `main` runs one workflow per binary
(`.github/workflows/release-<binary>.yaml`), each with its own `go vet` +
`go test -race` + `golangci-lint` + build chain pushing
`ghcr.io/madic-creates/<binary>:{latest,<short-sha>}`. Each workflow is
path-filtered to its own `cmd/<binary>/`, `web/<binary>/`,
`Dockerfile.<binary>`, plus the shared `internal/`, `go.mod`, `go.sum` —
so a binary-only change rebuilds only that image, while an `internal/`
change triggers both pipelines.

## Architecture

### Layered, additive-only packages

```
cmd/<tool>/         — binary entry point, wire-up only
  main.go           — env reads, client construction, mux/server, signal handling
  state.go          — shared in-memory state (sync.RWMutex + Snapshot())
  aggregator.go     — per-source polling goroutines (panic-recover + backoff)
  handlers.go       — HTTP handlers + Prometheus collectors

internal/<area>/    — shared cluster-integration plumbing
  kube              — InClusterConfig + kubernetes.Interface + dynamic.Interface
  argocd            — HTTP client for /api/v1/applications
  prom              — Prometheus instant-query helper (no range queries)
  certs             — cert-manager Certificate lister via dynamic client

web/<tool>/         — server-rendered HTML + CSS, exposed as embed.FS
  embed.go          — package <tool>web; var FS embed.FS
  index.html.tmpl   — html/template, inline <style>{{.CSS}}</style>, vanilla-JS poll loop
  crt.css, modern.css
```

**Two layout rules matter for keeping new binaries additive:**

1. The `web/` embed lives in its **own Go package** (`web/<tool>/embed.go`) and is
   imported by `cmd/<tool>/main.go`. This is because `go:embed` patterns can't
   include `..`. Don't move templates next to `main.go`.

2. `internal/` packages are append-only across PRs. The pod-tamagotchi spec
   adds a new file `internal/kube/nodes.go` next to the existing `client.go`
   without modifying it. When extending, add a sibling file rather than editing
   an existing one.

### Aggregator → State → Handler dataflow

Per source (ArgoCD, Longhorn, certs, restart-pods):

```
goroutine: ticker(20s) → poll() → State.SetX(data, now) | State.SetXError(err, now)
                                       │
                                       ▼
HTTP handler: State.Snapshot() (deep-copy slices) → JSON or html/template render
```

- **Errors don't crash and don't restart the Pod.** Poll errors update
  `LastError` + `LastFailure` only; `LastSuccess` and `Loaded` are preserved.
  Panics are `recover()`-ed, logged ERROR, the goroutine restarts after a
  10-second backoff. `/healthz` is **liveness-only** (always 200 if the HTTP
  server can respond) — do not re-introduce data-freshness checks there.
- **`AllGreen` requires `freshCount > 0`.** A fresh `*State` (no source loaded
  yet) returns `false` so the page never flashes "CLUSTER OK" during init or
  total-cluster outage. Stale-but-loaded sources are excluded from the verdict
  but counted via `StaleCount` for the "N source(s) stale" footer.
- **Snapshots deep-copy.** `State.Snapshot()` clones the three slice fields
  (`ArgoCD.Bad`, `Certs.Expiring`, `Restarts.Pods`) so callers can mutate freely.
  Tests rely on this — see `TestState_SnapshotIsCopy`.

### HTTP client conventions

`internal/argocd/client.go` and `internal/prom/client.go` follow the same
defensive pattern; new clients should match it:

- stdlib `net/http` only (no upstream Go SDKs — keeps the dep graph small)
- `*http.Client` injected via constructor; defaults to `http.DefaultClient`
- `strings.TrimRight(baseURL, "/")` on construction
- All errors wrapped with `fmt.Errorf("...: %w", err)`
- Response body bounded by `io.LimitReader(resp.Body, 16<<20)` (16 MiB)
- Body decode happens before status check so non-2xx envelopes are
  surface-able (e.g. Prometheus `errorType`/`error` fields on 422)
- `defer func() { _ = resp.Body.Close() }()` (the inline discard is more
  reliable than errcheck's `(*net/http.Response).Body.Close` exclusion pattern)

### Lint quirks worth knowing

- `.golangci.yml` disables staticcheck **QF1011**. We use the
  `var _ Interface = value` idiom for compile-time interface assertions
  (`internal/kube/client_test.go`); QF1011 wants to strip the type and would
  break the assertion.
- Staticcheck **ST1021** wants exported-type doc comments to start with the
  bare type name. Generic types: write `// Slot holds...`, not `// Slot[T] holds...`.

## Where to look

| You want to understand | Read |
|---|---|
| Why a particular file exists / what it should do | `docs/superpowers/specs/2026-04-28-<tool>-design.md` |
| The TDD task breakdown that produced cluster-tv | `docs/superpowers/plans/2026-04-28-cluster-tv.md` |
| The release pipeline | `docs/renovate.md`, `docs/cleanup-ghcr.md`, `docs/pre-commit.md` |
| Reference patterns for new binaries | sibling repo `claude-alert-analyzer` (different problem domain, same shape: scratch Dockerfile, multi-binary CI matrix, pre-commit + renovate config) |
| How to deploy a tool | `docs/<tool>-deployment.md` + `deploy/<tool>/` |

## Conventional Commits + commit hygiene

Conventional Commits format is enforced by review (no automated check).
Subject scope is the area changed: `feat(cluster-tv):`, `fix(prom):`,
`ci:`, `docs:`, `chore:`, `refactor:`, `test:`. The body should explain
**why** when the change isn't self-evident.

Renovate auto-merges minor/patch through branch pushes (no PR review). Major
updates land as PRs and need a human. The `k8s.io/*` packages are grouped so
they always update together — mixing minors of api/apimachinery/client-go
breaks compilation.
