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

## Layout

- `cmd/<name>/` — one directory per binary
- `internal/` — shared packages: `kube`, `argocd`, `prom`, `certs`
- `web/<name>/` — server-rendered HTML templates and CSS for each tool

## Build / test

    go test -race ./...
    go vet ./...
    golangci-lint run

CI builds and pushes per-binary images on every push to `main`.
