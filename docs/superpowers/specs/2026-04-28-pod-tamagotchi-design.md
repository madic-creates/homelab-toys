# Pod-Tamagotchi — Design Spec

**Date:** 2026-04-28
**Status:** Approved, ready for implementation plan
**Implementation order:** Second — depends on `homelab-toys` repo bootstrapped by Cluster-TV spec

## Motivation

Cluster-health dashboards (Grafana, gethomepage, Cluster-TV) all communicate through numbers and indicators. The same data can be reframed as a single emotional readout: a small pixel pet whose mood reflects whether the cluster is happy. The point is the glanceable feedback loop — when something starts to go wrong, the homepage widget changes mood before any alert fires, and fixing the issue brings the pet back to ecstatic. Pure spielerei, but with an honest signal underneath.

## Scope

### In v1

- 5-stage mood scale: `ecstatic`, `happy`, `meh`, `sick`, `dying`
- Mood derived from the same cluster signals as Cluster-TV (ArgoCD, Longhorn, cert-manager, pod restarts) plus node Ready-state
- Pet "age" displayed, derived from the Pod's own `creationTimestamp` read once at startup via the Kubernetes API (no persistent state)
- Standalone fullscreen page with large pixel sprite and idle animation
- Compact `/widget` HTML endpoint sized for embedding into gethomepage as a custom iframe widget
- Hysteresis: mood degrades immediately, improves only after the cluster has been good for 5 minutes (no flicker on transient blips)

### Out of v1 (explicit)

Persistent state or PVC-backed birthday, death-and-revive mechanic, sprite asset files (PNG/GIF), multiple pets, "feed" interactions, sounds, achievement system, mood history graph, mobile-optimised layout, CRT/modern theme toggle (the pixel sprite has only the one look).

## Dependency on Cluster-TV spec

This spec assumes the `madic-creates/homelab-toys` repo already exists with `go.mod`, `internal/kube/`, `internal/argocd/`, `internal/prom/`, `internal/certs/`, the CI release workflow, the `.golangci.yaml`, and the README. If Cluster-TV has not been merged yet, the bootstrap section of its spec must be executed first.

This spec adds:

- `cmd/tamagotchi/main.go`
- `internal/health/mood.go` and `internal/health/mood_test.go`
- `web/tamagotchi/` (HTML templates, sprite SVG templates, CSS)
- `Dockerfile.tamagotchi`
- A parallel job in `.github/workflows/release.yaml` that builds and pushes `ghcr.io/madic-creates/tamagotchi`
- One new file `internal/kube/nodes.go` providing the `Nodes()` helper

The change to `internal/kube/` is additive only — a new file alongside the files Cluster-TV created, with no edits to any pre-existing source file in that package or in `internal/argocd/`, `internal/prom/`, `internal/certs/`. The CI workflow adds a parallel job; it does not edit the Cluster-TV job.

## Architecture

Single-binary `tamagotchi`, 1-replica Deployment in namespace `monitoring`. RBAC adds `list` on Nodes and `get` on the self-Pod on top of Cluster-TV's certificate-only RBAC. Stateless.

```
                ┌──────────────────────────────────────────┐
                │         tamagotchi (Pod)                 │
                │                                          │
   /         ───┤  HTML templates ◄── State + Mood ◄────┐  │
   /widget   ───┤  (standalone +                        │  │
   /api/state   │   widget variants)                    │  │
   /healthz     │                                       │  │
   /metrics     │           Aggregator goroutine ───────┘  │
                │             ▲     ▲      ▲      ▲      ▲ │
                └─────────────┼─────┼──────┼──────┼──────┼─┘
                              │     │      │      │      │
                          ArgoCD  Prom    Prom   Kube   Kube
                          (HTTP) (Vol)  (Pods) (Certs) (Nodes)
```

### Endpoints

| Path | Function |
|---|---|
| `GET /` | Standalone fullscreen page. Pixel sprite at `transform: scale(8)`, mood text, age in days, list of active mood-reducer factors. |
| `GET /widget` | Compact HTML (~200×120px), sprite at `transform: scale(2)`, mood text only. Designed for an iframe widget in gethomepage. |
| `GET /api/state` | JSON: `{ mood: "happy", mood_level: 1, age_days: 42, born_at: "2026-...", factors: [{source, severity, reason}], stale_sources: [], confused: false }`. |
| `GET /healthz` | 200 if every source goroutine has updated its heartbeat within the last 90 seconds; 503 otherwise. |
| `GET /metrics` | Prometheus metrics: `tamagotchi_mood_level` (gauge 0..4), `tamagotchi_source_poll_total{source,result}`, `tamagotchi_source_last_success_seconds{source}`. |

### Mood algorithm

Implemented in `internal/health/mood.go`. Pure function `Compute(sources SourceStates, history MoodHistory, now time.Time) Mood`.

Starting from level 0 (`ecstatic`), each signal contributes a penalty:

| Signal | Penalty |
|---|---|
| ArgoCD: ≥1 Degraded or OutOfSync app | +1 |
| Longhorn: ≥1 Degraded or Faulted volume | +1 |
| cert-manager: cert with `status.notAfter` set and < 14 days from now | +1 |
| Pods returned by Cluster-TV's restart Prometheus query (`increase(kube_pod_container_status_restarts_total[24h]) > 5`) | +1 per pod, capped at +2 |
| Node `Ready=False` | +3 (immediately `dying`) |

Certificates without a `status.notAfter` (not yet issued, in failure state) are skipped and do not contribute a penalty.

Final level clamped to `[0, 4]`. Mapping: 0=ecstatic, 1=happy, 2=meh, 3=sick, 4=dying.

**Hysteresis — exact semantics:**

The algorithm holds three values in memory: `current_mood` (what is reported to clients), `pending_target` (the most recent computed level that is *better* than `current_mood`, or `nil` if none is pending), and `pending_since` (the timestamp `pending_target` first appeared).

On every aggregator tick the algorithm computes `next_level` from the source states, then:

1. **Worsening** — if `next_level > current_mood`: `current_mood` is set to `next_level` immediately (a single-step jump, regardless of how many levels are crossed). Any pending improvement is discarded (`pending_target = nil`).
2. **Improvement candidate** — if `next_level < current_mood`:
   - If `pending_target == nil` or `next_level != pending_target`, set `pending_target = next_level` and `pending_since = now`.
   - If `now - pending_since >= 5 minutes`, set `current_mood = pending_target` (a single-step jump to the candidate) and clear `pending_target`.
3. **Stable** — if `next_level == current_mood`, clear any pending improvement.
4. **Regression of pending candidate** — if at any point the computed level is *worse* than `pending_target` (but still ≤ `current_mood`), the pending window is reset (`pending_target` updated to the new value, `pending_since = now`). This prevents flickering candidates from accumulating stable time.

**Init grace (first 30s after process start):** the entire algorithm is bypassed. `current_mood = happy` (level 1) and the page shows a "Hallo!" speech bubble. After the first successful aggregator cycle, `current_mood` is set to the computed level *immediately* (no 5-minute wait), then the standard hysteresis takes over from there.

**Confused state:** if ≥ 2 sources are stale (no successful poll in > 5 minutes), the sprite renders the `confused` variant (a `?` floating above the head) and `current_mood` is reported as the last known good value, never made worse by stale data. This avoids the failure mode "ArgoCD-server is being upgraded → pet appears to be dying". The hysteresis state machine continues running underneath but only over the still-fresh sources.

### Pet birthday

At process startup, the binary makes a single `GET /api/v1/namespaces/$POD_NAMESPACE/pods/$POD_NAME` to read its own Pod's `creationTimestamp`, then caches the value for the lifetime of the process to compute `age_days`. The downward API exposes `POD_NAME` and `POD_NAMESPACE` via `fieldRef`, but `metadata.creationTimestamp` is not exposed, so the API round-trip is the cleanest available path. The pod-restart signal is sourced from Prometheus (inherited from Cluster-TV), so no cluster-wide pod list is otherwise maintained. On Pod restart the pet is reborn — this is the explicit non-persistence trade-off accepted in v1.

### Pixel sprite

Inline SVG, generated from a Go-side per-mood pixel matrix. Each mood has a 64×64 cell matrix; a colour palette per mood maps cell IDs to hex colours. The SVG has one `<rect width="1" height="1">` per non-background pixel. Body class `mood-<name>` selects the per-mood CSS animation.

Idle animations (CSS `@keyframes`):
- `ecstatic` — vertical bounce, ~6px peak, 1s cycle
- `happy` — vertical bounce, ~3px peak, 1.5s cycle
- `meh` — none
- `sick` — slow horizontal wobble, ~2px, 2s cycle
- `dying` — lying flat, no movement, occasional 1-frame flicker

Standalone scaling: container has `transform: scale(8)` plus `image-rendering: pixelated` so each SVG-pixel becomes 8 screen pixels. Widget scales to 2.

### Secrets and RBAC

Reuses the ArgoCD-token pattern from Cluster-TV: SOPS-encrypted `tamagotchi-env` secret in `monitoring` with `ARGOCD_TOKEN` and `PROMETHEUS_URL`.

ServiceAccount `tamagotchi` permissions:

- ClusterRole, cluster-wide `list` on `nodes` (for the Ready-state signal).
- ClusterRole, cluster-wide `list` on `cert-manager.io/v1 Certificates` (for the cert-expiry signal).
- Role scoped to the `monitoring` namespace, `get` on `pods` (for the one-off self-pod read at startup; the pod's name is dynamic so `resourceNames` is impractical, namespace scoping is the next-tightest bound).

The pod-restart signal is queried via Prometheus and needs no Kubernetes RBAC. No write permissions anywhere.

## Error handling

Inherits the "stale data is preserved, never break the UI" rule from Cluster-TV. Tamagotchi-specific behaviours:

- **Stale sources do not worsen mood.** The mood algorithm explicitly skips a source whose `LastSuccess` is older than 5 minutes. Without this rule, a temporary ArgoCD outage would falsely drive the pet to `sick`.
- **Init handling** is described in the Mood-algorithm section above: a 30-second grace forces `current_mood = happy`, then the first successful aggregator cycle adopts the computed level immediately, after which standard hysteresis applies. There is no separate 5-minute hysteresis bypass — the spec previously implied one and the discrepancy has been removed.
- **`/api/state` always returns 200.** Failures are reported inside the JSON via `factors[].source == "<name>"` carrying a non-empty `error` field, plus the top-level `stale_sources` array.
- **Panic recovery.** Aggregator goroutines have `recover()`; on panic, log at `ERROR` and restart the source goroutine after a 10-second backoff.
- **Log format.** `log/slog` JSON, source errors at `WARN`, aggregator panics at `ERROR`.

## Testing

The mood calculator is the unit under heaviest test coverage.

- **Mood-calculator table tests** (`internal/health/mood_test.go`):
  - All sources green → `ecstatic`
  - One source degraded (each, separately) → `happy`
  - Two sources degraded → `meh`
  - One pod restart-storm > cap → still bounded at `meh`
  - Node NotReady alone → `dying`
  - Cert expiring < 14d combined with degraded ArgoCD → `meh`
  - All five sources bad → `dying`
- **Hysteresis tests** with an injected `Clock` interface:
  - Bad → good for 4 minutes → mood stays bad
  - Bad → good for 6 minutes → mood improves
  - Good → bad → immediate worsening, no delay
  - Bad → good → bad within window → window resets
- **Source tests reuse Cluster-TV fixtures.** `internal/argocd`, `internal/prom`, `internal/certs` are not re-tested here. The new `Nodes()` helper in `internal/kube/nodes.go` gets a fake-clientset table test, and the self-pod-read helper used at startup gets a small fake-clientset test asserting it returns the cached `creationTimestamp` for `POD_NAME` / `POD_NAMESPACE`.
- **HTTP handler tests.** `/api/state` JSON schema golden-file. `/widget` smoke render asserts the HTML body contains `<svg>` and the expected `class="mood-<name>"`. `/` smoke render the same.
- **Sprite snapshot tests.** One golden file per mood pinning the SVG byte output for a fixed palette. Prevents unintended pixel changes during refactors.

**Pre-merge gate:** `go test -race ./...`, `go vet`, `golangci-lint run`, plus `kustomize build apps/tamagotchi --enable-helm --enable-alpha-plugins --enable-exec` and `scripts/kubeconform-validate.sh apps/tamagotchi` from the k3s-git-ops repo side.

## Kubernetes manifests (in k3s-git-ops repo)

Path: `apps/tamagotchi/`. Files:

- `kustomization.yaml` — namespace `monitoring`, sync wave `90`, resources below plus `kustomize-secret-generator.yaml`.
- `k8s.deployment.yaml` — 1 replica, image `ghcr.io/madic-creates/tamagotchi:latest@sha256:...`, distroless runtime, `runAsNonRoot: true`, requests/limits ~50m / 64Mi. Downward-API env vars `POD_NAME` (`fieldRef: metadata.name`) and `POD_NAMESPACE` (`fieldRef: metadata.namespace`) are set so the binary can resolve its own Pod via the API at startup to read `creationTimestamp`.
- `k8s.service.yaml` — ClusterIP, port 8080.
- `k8s.rbac.yaml` — ServiceAccount, ClusterRole (`list` on `nodes` + `list` on `cert-manager.io/v1 Certificates`), ClusterRoleBinding, plus a namespace-scoped Role (`get` on `pods` in the `monitoring` namespace) and RoleBinding for the self-pod read at startup.
- `k8s.np.tamagotchi-default-deny.yaml` + `k8s.np.tamagotchi.yaml` — default-deny plus allow ingress from Traefik (the widget iframe loads via the public Authelia-protected URL, not from inside the cluster), egress to ArgoCD, Prometheus, kube-apiserver.
- `secrets.enc.yaml` — SOPS-encrypted, `ARGOCD_TOKEN` and `PROMETHEUS_URL`.
- `kustomize-secret-generator.yaml` — ksops generator.
- `tamagotchi.ingress.enc.yaml` — encrypted IngressRoute on `internal.neese-web.de` entrypoints (Authelia forward-auth via existing middleware), `wildcard-cloudflare-production-02`.

Plus `apps/argo-cd-apps/91-tamagotchi.yaml` (ArgoCD Application) and a new entry in `apps/argo-cd-apps/kustomization.yaml`.

The gethomepage configuration adds a custom iframe widget pointing at the public URL `https://tamagotchi.internal.neese-web.de/widget`. The user's existing Authelia session for the homepage covers the iframe load — no extra auth round-trip in the embedded view.

## Implementation order

1. Verify the `homelab-toys` repo exists with shared `internal/` packages from the Cluster-TV spec. If not, run that spec's bootstrap first.
2. Add `internal/health/mood.go` plus tests; add `Nodes()` helper to `internal/kube/`.
3. Implement `cmd/tamagotchi/main.go`: aggregator, HTTP handlers, HTML templates, sprite SVG generator, idle-animation CSS.
4. Add `Dockerfile.tamagotchi` and the parallel CI job in `.github/workflows/release.yaml`.
5. First green CI produces `ghcr.io/madic-creates/tamagotchi:latest`.
6. In k3s-git-ops, add `apps/tamagotchi/` plus the ArgoCD Application manifest. Encrypt secrets, configure IngressRoute, apply network policies.
7. Add the gethomepage iframe widget pointing at the in-cluster `/widget` endpoint.
8. Verify on cluster: `/` standalone, `/widget` rendered inside gethomepage, mood transitions when forcing one ArgoCD app into Degraded state, hysteresis behaviour after restoring it.
