# homelab-toys

Small, single-purpose tools for `madic-creates`'s homelab cluster.
Each binary lives in `cmd/<name>/` and reuses the shared packages in `internal/`.

## Tools

### cluster-tv
Single-page wall-display that aggregates ArgoCD application health, Longhorn
volume state, cert-manager expiry, and pod restart counts. CRT and modern
themes selectable via `?theme=...`. Image: `ghcr.io/madic-creates/cluster-tv`.
Deployment reference: [`docs/cluster-tv-deployment.md`](docs/cluster-tv-deployment.md).

### tamagotchi
Single-binary HTML page that reframes cluster-tv's signals as a 5-stage pixel-pet mood
(`ecstatic` / `happy` / `meh` / `sick` / `dying`) with hysteresis (immediate worsening,
5-minute window for improvement). Includes a compact `/widget` variant for embedding
into homepage dashboards. Image: `ghcr.io/madic-creates/tamagotchi`.
Deployment reference: [`docs/tamagotchi-deployment.md`](docs/tamagotchi-deployment.md).

`GET /api/state` returns the current mood as JSON for programmatic consumption (e.g.
gethomepage custom widgets, alerting):

```json
{
  "mood": "sick",
  "mood_level": 3,
  "age_days": 42,
  "born_at": "2026-01-01T00:00:00Z",
  "factors": [
    {"source": "argocd", "penalty": 1},
    {"source": "certs", "penalty": 1},
    {"source": "restarts", "penalty": 2}
  ],
  "stale_sources": [],
  "confused": false,
  "hello": false
}
```

`mood_level` is `0..4` (ecstatic→dying). `factors` lists each source whose penalty
is non-zero or whose last poll failed (`{source, penalty, error?}`); sources
contributing nothing are omitted, so an empty `factors` with a non-`ecstatic` mood
indicates the algorithm has stale data. `stale_sources` lists upstream sources that
have not polled successfully in the last 5 minutes; `confused` is `true` when ≥2
sources are stale; `hello` is `true` only during the init grace before the first
poll completes.

## Layout

- `cmd/<name>/` — one directory per binary
- `internal/` — shared packages: `kube`, `argocd`, `prom`, `certs`
- `web/<name>/` — server-rendered HTML templates and CSS for each tool

## Build / test

    go test -race ./...
    go vet ./...
    golangci-lint run

CI builds and pushes per-binary images on every push to `main`.
