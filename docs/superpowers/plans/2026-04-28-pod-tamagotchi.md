# Pod-Tamagotchi Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a second binary `tamagotchi` to the `homelab-toys` monorepo — a single-binary HTML page that reframes Cluster-TV's signals as a 5-stage pixel-pet mood (`ecstatic` / `happy` / `meh` / `sick` / `dying`) with hysteresis, a `/widget` variant for embedding, and reference Kubernetes manifests under `deploy/tamagotchi/`.

**Architecture:** Single binary `cmd/tamagotchi/` reusing the cluster-tv aggregator/state pattern. Five polling sources (ArgoCD, Longhorn-via-Prom, Prom restart query, cert-manager, Nodes-via-client-go) feed per-source penalty integers into a `State` struct guarded by `sync.RWMutex`. A pure mood calculator in `internal/health/` consumes a fresh snapshot, applies a hysteresis state machine (immediate worsening, 5-minute pending window for improvement, init grace for the first 30s, confused state when ≥2 sources are stale), and emits the displayed mood. HTTP handlers serve a fullscreen page (`/`), a compact widget (`/widget`), JSON state (`/api/state`), liveness (`/healthz`), and Prometheus metrics (`/metrics`). Pixel sprite is inline SVG generated from per-mood Go-side pixel matrices. Reference manifests in `deploy/tamagotchi/` mirror the conventions of `deploy/cluster-tv/`.

**Tech Stack:** Go 1.26 (existing toolchain), `net/http`, `html/template`, `log/slog`, `k8s.io/client-go` (existing `internal/kube/` extended with one additive file), `prometheus/client_golang` for `/metrics`. No new third-party dependencies. Zero JS bundler — vanilla `fetch` poll loop, ~80 lines per template.

**Repo conventions reused (from cluster-tv):**
- Aggregator pattern in `cmd/<tool>/aggregator.go` with `runSource(ctx, name, poll, interval, m)` panic-recover + 10s backoff
- State pattern with generic `Slot[T any]` holding `Data, LastSuccess, LastFailure, LastError, Loaded`
- HTTP client conventions: stdlib `net/http`, `io.LimitReader(resp.Body, 16<<20)`, `defer func() { _ = resp.Body.Close() }()`, error-wrapping with `fmt.Errorf("...: %w", err)`
- `web/<tool>/` as its own Go package with `var FS embed.FS`, imported by `cmd/<tool>/main.go`
- `Dockerfile.<tool>` multi-stage `golang:1.26-alpine` builder → `scratch` runtime, `USER 65534:65534`
- Reference manifests under `deploy/<tool>/`: `namespace.yaml`, `rbac.yaml`, `secret.yaml` (plain Secret with `REPLACE_WITH_*` placeholder), `deployment.yaml`, `service.yaml`, `networkpolicy.yaml`, `ingress.yaml`, `README.md`
- Per-binary deployment narrative under `docs/<tool>-deployment.md`
- `.golangci.yml` lint quirks: ST1021 wants doc comments to start with the bare type name (no type params)

**Out of scope for this plan:**
- Persistent state / PVC-backed birthday — pet is reborn on Pod restart (explicit v1 trade-off per spec)
- Sprite asset files (PNG/GIF) — sprite is pure inline SVG generated from Go-side matrices
- Multi-pet / feed interactions / sounds / achievements / mood history graph
- Mobile-optimised layout, theme toggle (single visual look)
- gethomepage iframe widget configuration — that's a deployment-side concern outside this repo

---

## File Structure (target end state)

New files (Go):

```
cmd/tamagotchi/
├── main.go                # wire-up, env reads, signal handling, HTTP server
├── state.go               # Snapshot + State + Slot[int] per-source penalties + Birthday + Mood
├── state_test.go
├── aggregator.go          # runSource + per-source poll factories + aggregator wiring
├── aggregator_test.go
├── handlers.go            # /, /widget, /api/state, /healthz, /metrics
├── handlers_test.go
├── sprite.go              # per-mood pixel matrix → inline SVG
└── sprite_test.go

internal/health/
├── mood.go                # Sources, History, Result, Compute() — pure function
└── mood_test.go

internal/kube/
├── nodes.go               # Nodes(ctx) helper — additive, no edits to client.go
├── nodes_test.go
├── selfpod.go             # SelfPodCreatedAt(ctx, ns, name) — additive
└── selfpod_test.go

web/tamagotchi/
├── embed.go               # package tamagotchiweb; var FS embed.FS
├── index.html.tmpl        # fullscreen page (sprite scale 8 + age + factors + Hallo bubble during init grace)
├── widget.html.tmpl       # compact widget (sprite scale 2 + mood text only)
└── style.css              # body.mood-<name> animations + layout
```

New files (build/CI/deploy):

```
Dockerfile.tamagotchi       # multi-stage builder → scratch
.github/workflows/release.yaml   # MODIFY: add build-tamagotchi job
README.md                   # MODIFY: add tamagotchi description + deploy link

deploy/tamagotchi/
├── namespace.yaml
├── rbac.yaml
├── secret.yaml             # plain Secret with REPLACE_WITH_ARGOCD_LOCAL_USER_TOKEN
├── deployment.yaml         # downward-API env (POD_NAME, POD_NAMESPACE)
├── service.yaml
├── networkpolicy.yaml
├── ingress.yaml
└── README.md

docs/tamagotchi-deployment.md   # narrative companion guide
```

**Layout rules being respected:**

1. `internal/kube/` is extended via two new files (`nodes.go`, `selfpod.go`) — `client.go` is not modified. This matches the project's "internal/ packages are append-only across PRs" rule.
2. `web/tamagotchi/` lives in its own Go package because `go:embed` cannot include `..`. `cmd/tamagotchi/main.go` imports `tamagotchiweb` for the FS.
3. `cmd/cluster-tv/` is not touched. Tamagotchi shapes its own per-source data; the only shared code lives in `internal/`.

---

## Task overview

| Phase | Tasks | What it produces |
|---|---|---|
| 1. Internal helpers (additive) | 1–2 | `Nodes()` and `SelfPodCreatedAt()` in `internal/kube/` |
| 2. Mood algorithm | 3–5 | `internal/health/mood.go` — penalty calc, hysteresis, init grace + confused |
| 3. Sprite generator | 6 | `cmd/tamagotchi/sprite.go` — pixel matrix → SVG |
| 4. State + aggregator | 7–9 | `cmd/tamagotchi/state.go`, `aggregator.go`, birthday lookup |
| 5. HTTP handlers | 10–13 | `/api/state`, `/`, `/widget`, `/healthz`, `/metrics` |
| 6. Web assets + main + Docker | 14–16 | Templates + CSS, `main.go`, `Dockerfile.tamagotchi` |
| 7. CI + repo docs | 17–18 | Release workflow matrix entry, README update |
| 8. Reference manifests + deploy doc | 19–22 | `deploy/tamagotchi/*` + `docs/tamagotchi-deployment.md` |

Total: 22 tasks. Each task is 5–9 steps following the TDD cycle (failing test → red → minimal impl → green → refactor → commit). Estimated execution time: 6–10 working hours, depending on familiarity with the cluster-tv codebase.

---

## Phase 1 — Internal helpers

### Task 1: Add `Nodes()` helper to `internal/kube/`

**Files:**
- Create: `internal/kube/nodes.go`
- Test: `internal/kube/nodes_test.go`

The spec (line 39) requires this be a sibling file to `internal/kube/client.go` with **no edits** to `client.go`. The helper returns just what tamagotchi needs — a list of `(name, ready)` pairs — so callers don't depend on the full `corev1.Node` shape and don't need to import `corev1` themselves.

- [ ] **Step 1: Write the failing test**

```go
// internal/kube/nodes_test.go
package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNodes_AllReady(t *testing.T) {
	cs := fake.NewClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			}},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			}},
		},
	)
	got, err := Nodes(context.Background(), cs)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, n := range got {
		if !n.Ready {
			t.Errorf("%s ready = false, want true", n.Name)
		}
	}
}

func TestNodes_OneNotReady(t *testing.T) {
	cs := fake.NewClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "ok"},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			}},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "bad"},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			}},
		},
	)
	got, err := Nodes(context.Background(), cs)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	want := map[string]bool{"ok": true, "bad": false}
	for _, n := range got {
		if w, ok := want[n.Name]; !ok || w != n.Ready {
			t.Errorf("%s ready = %v, want %v", n.Name, n.Ready, w)
		}
	}
}

func TestNodes_NoReadyConditionTreatedAsNotReady(t *testing.T) {
	cs := fake.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "weird"}},
	)
	got, err := Nodes(context.Background(), cs)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(got) != 1 || got[0].Ready {
		t.Fatalf("got %+v, want one not-ready node", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race -v -run TestNodes ./internal/kube/`
Expected: FAIL — `undefined: Nodes`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/kube/nodes.go
package kube

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// NodeStatus is the trimmed view of one Kubernetes Node that callers in
// this repo need: just the name and whether the Ready condition is True.
// Returning a plain struct keeps callers free of corev1 imports.
type NodeStatus struct {
	Name  string
	Ready bool
}

// Nodes lists all cluster Nodes via the given clientset and returns the
// trimmed NodeStatus view. A Node whose Ready condition is missing is
// reported as Ready=false — the conservative interpretation for a
// dashboard signal.
func Nodes(ctx context.Context, cs kubernetes.Interface) ([]NodeStatus, error) {
	list, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	out := make([]NodeStatus, 0, len(list.Items))
	for _, n := range list.Items {
		out = append(out, NodeStatus{Name: n.Name, Ready: nodeReady(n)})
	}
	return out, nil
}

func nodeReady(n corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race -v -run TestNodes ./internal/kube/`
Expected: PASS — three subtests green.

- [ ] **Step 5: Run vet + lint to catch ST1021 / errcheck**

Run: `go vet ./internal/kube/ && golangci-lint run ./internal/kube/...`
Expected: no findings. (The doc comment starts with `NodeStatus` and `Nodes` — no generics involved here, so ST1021 is happy.)

- [ ] **Step 6: Commit**

```bash
git add internal/kube/nodes.go internal/kube/nodes_test.go
git commit -m "feat(kube): add Nodes() helper for Ready-state listing"
```

---

### Task 2: Add `SelfPodCreatedAt()` helper to `internal/kube/`

**Files:**
- Create: `internal/kube/selfpod.go`
- Test: `internal/kube/selfpod_test.go`

The spec (lines 106–108) wants a one-shot read of the pod's own `metadata.creationTimestamp` at process start, used to compute `age_days`. The downward API exposes `POD_NAME` and `POD_NAMESPACE` but not `creationTimestamp`, so an API GET on the pod itself is the cleanest path.

- [ ] **Step 1: Write the failing test**

```go
// internal/kube/selfpod_test.go
package kube

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestSelfPodCreatedAt_ReturnsCreationTimestamp(t *testing.T) {
	want := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	cs := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "tama-abc",
			Namespace:         "monitoring",
			CreationTimestamp: metav1.NewTime(want),
		},
	})
	got, err := SelfPodCreatedAt(context.Background(), cs, "monitoring", "tama-abc")
	if err != nil {
		t.Fatalf("SelfPodCreatedAt: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSelfPodCreatedAt_NotFoundReturnsError(t *testing.T) {
	cs := fake.NewClientset()
	_, err := SelfPodCreatedAt(context.Background(), cs, "monitoring", "tama-abc")
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -race -v -run TestSelfPodCreatedAt ./internal/kube/`
Expected: FAIL — `undefined: SelfPodCreatedAt`.

- [ ] **Step 3: Implement**

```go
// internal/kube/selfpod.go
package kube

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// SelfPodCreatedAt fetches the pod identified by namespace+name and returns
// its metadata.creationTimestamp. Used at process startup to derive the
// pet's age, which is then cached for the lifetime of the binary.
//
// The downward API exposes POD_NAME and POD_NAMESPACE via fieldRef, but
// not creationTimestamp, so this round-trip is the cleanest path.
func SelfPodCreatedAt(ctx context.Context, cs kubernetes.Interface, namespace, name string) (time.Time, error) {
	pod, err := cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return time.Time{}, fmt.Errorf("get self pod %s/%s: %w", namespace, name, err)
	}
	return pod.CreationTimestamp.Time, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -v -run TestSelfPodCreatedAt ./internal/kube/`
Expected: PASS.

- [ ] **Step 5: Lint**

Run: `golangci-lint run ./internal/kube/...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/kube/selfpod.go internal/kube/selfpod_test.go
git commit -m "feat(kube): add SelfPodCreatedAt() for startup birthday read"
```

---

## Phase 2 — Mood algorithm

The mood package is the algorithmic heart of tamagotchi. Tests are dense here because the hysteresis behaviour, init grace, and confused-state interactions are spelled out in the spec and must be locked in.

### Task 3: Mood types + pure penalty calculator

**Files:**
- Create: `internal/health/mood.go`
- Test: `internal/health/mood_test.go`

The penalty table from the spec:

| Source | Penalty contribution |
|---|---|
| ArgoCD ≥1 Degraded or OutOfSync | +1 |
| Longhorn ≥1 Degraded or Faulted | +1 |
| Cert with `notAfter` set and < 14d | +1 |
| Restart-storm pods | +1 per pod, capped at +2 |
| Node `Ready=False` | +3 |

Final clamped to [0, 4]: 0=ecstatic, 1=happy, 2=meh, 3=sick, 4=dying.

This task delivers the pure penalty sum + level mapping. Hysteresis, init grace and confused state come in tasks 4 and 5.

- [ ] **Step 1: Write the failing test for the level constants**

```go
// internal/health/mood_test.go
package health

import (
	"testing"
	"time"
)

func TestMoodLevels(t *testing.T) {
	tests := []struct {
		name  string
		level int
		want  string
	}{
		{"ecstatic", 0, "ecstatic"},
		{"happy", 1, "happy"},
		{"meh", 2, "meh"},
		{"sick", 3, "sick"},
		{"dying", 4, "dying"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Mood{Level: tt.level}
			if m.Name() != tt.want {
				t.Errorf("Name() = %q, want %q", m.Name(), tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -race -v ./internal/health/`
Expected: FAIL — package does not compile (`undefined: Mood`).

- [ ] **Step 3: Implement Mood type + Name()**

```go
// internal/health/mood.go
package health

import "time"

// Mood is the discrete pet state shown to the user.
type Mood struct {
	Level int // 0..4
}

// Name returns the string label for the mood level. Out-of-range levels
// return "happy" as a defensive fallback — the algorithm clamps before
// constructing Mood, so this branch is unreachable in production.
func (m Mood) Name() string {
	switch m.Level {
	case 0:
		return "ecstatic"
	case 1:
		return "happy"
	case 2:
		return "meh"
	case 3:
		return "sick"
	case 4:
		return "dying"
	default:
		return "happy"
	}
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -race -v ./internal/health/`
Expected: PASS.

- [ ] **Step 5: Add penalty-sum table tests**

Append to `mood_test.go`:

```go
func TestSumPenalty_Cases(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	fresh := func(p int) Source {
		return Source{Loaded: true, LastSuccess: now, Penalty: p}
	}
	tests := []struct {
		name string
		s    Sources
		want int // expected level after clamp
	}{
		{
			name: "all green",
			s:    Sources{ArgoCD: fresh(0), Longhorn: fresh(0), Certs: fresh(0), Restarts: fresh(0), Nodes: fresh(0)},
			want: 0,
		},
		{
			name: "argocd alone",
			s:    Sources{ArgoCD: fresh(1), Longhorn: fresh(0), Certs: fresh(0), Restarts: fresh(0), Nodes: fresh(0)},
			want: 1,
		},
		{
			name: "longhorn alone",
			s:    Sources{ArgoCD: fresh(0), Longhorn: fresh(1), Certs: fresh(0), Restarts: fresh(0), Nodes: fresh(0)},
			want: 1,
		},
		{
			name: "argocd + longhorn",
			s:    Sources{ArgoCD: fresh(1), Longhorn: fresh(1), Certs: fresh(0), Restarts: fresh(0), Nodes: fresh(0)},
			want: 2,
		},
		{
			name: "restart storm capped at 2",
			s:    Sources{ArgoCD: fresh(0), Longhorn: fresh(0), Certs: fresh(0), Restarts: fresh(2), Nodes: fresh(0)},
			want: 2,
		},
		{
			// Node penalty is +3 per the spec table — alone that lands on
			// level 3 (sick), not 4 (dying). The spec's parenthetical
			// "(immediately dying)" is semantic colour: node-down is the
			// single worst-weighted signal, but it still combines with
			// others to reach dying. See Task 4's hysteresis test for the
			// matching expectation.
			name: "node down reaches sick",
			s:    Sources{ArgoCD: fresh(0), Longhorn: fresh(0), Certs: fresh(0), Restarts: fresh(0), Nodes: fresh(3)},
			want: 3,
		},
		{
			name: "all bad clamps at 4",
			s:    Sources{ArgoCD: fresh(1), Longhorn: fresh(1), Certs: fresh(1), Restarts: fresh(2), Nodes: fresh(3)},
			want: 4,
		},
		{
			name: "expiring cert + degraded argocd",
			s:    Sources{ArgoCD: fresh(1), Longhorn: fresh(0), Certs: fresh(1), Restarts: fresh(0), Nodes: fresh(0)},
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SumPenalty(tt.s, now)
			if got != tt.want {
				t.Errorf("SumPenalty = %d, want %d", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 6: Run to verify failure**

Run: `go test -race -v ./internal/health/`
Expected: FAIL — `undefined: Source, Sources, SumPenalty`.

- [ ] **Step 7: Implement Source, Sources, SumPenalty**

Append to `mood.go`:

```go
// Source is one upstream signal as the aggregator hands it to the mood
// calculator. Penalty is computed by the aggregator from the raw source
// data (e.g. "≥1 degraded ArgoCD app" → 1) so the calculator stays free
// of source-specific shapes.
type Source struct {
	Loaded      bool
	LastSuccess time.Time
	Penalty     int
}

// Sources bundles all five inputs the mood calculator considers.
type Sources struct {
	ArgoCD   Source
	Longhorn Source
	Certs    Source
	Restarts Source
	Nodes    Source
}

// stalenessWindow is how old LastSuccess can be before a source is
// excluded from the penalty sum. Matches the cluster-tv staleness rule.
const stalenessWindow = 5 * time.Minute

// SumPenalty applies the spec's penalty table to fresh, loaded sources
// and clamps the total to [0, 4]. Stale or unloaded sources are skipped;
// see Compute() for staleness reporting.
func SumPenalty(s Sources, now time.Time) int {
	total := 0
	for _, src := range []Source{s.ArgoCD, s.Longhorn, s.Certs, s.Restarts, s.Nodes} {
		if !isFresh(src, now) {
			continue
		}
		total += src.Penalty
	}
	if total < 0 {
		total = 0
	}
	if total > 4 {
		total = 4
	}
	return total
}

func isFresh(s Source, now time.Time) bool {
	if !s.Loaded {
		return false
	}
	return now.Sub(s.LastSuccess) <= stalenessWindow
}
```

- [ ] **Step 8: Verify pass**

Run: `go test -race -v ./internal/health/`
Expected: PASS — eight subtests green plus the level-name test.

- [ ] **Step 9: Lint**

Run: `golangci-lint run ./internal/health/...`
Expected: clean.

- [ ] **Step 10: Commit**

```bash
git add internal/health/
git commit -m "feat(health): add Mood + Sources + SumPenalty"
```

---

### Task 4: Add hysteresis state machine to `Compute()`

**Files:**
- Modify: `internal/health/mood.go`
- Test: `internal/health/mood_test.go`

The spec (lines 89–100) is precise about hysteresis. Encoded:

1. **Worsening** (`next > current`): `current = next` immediately. Any pending improvement discarded.
2. **Improvement candidate** (`next < current`): set `pending = next`, `pendingSince = now`. After 5 minutes at the same target, `current = pending` (single-step).
3. **Stable** (`next == current`): clear pending.
4. **Pending regression** (`next != pending` but still `< current`): reset the window — replace pending and reset `pendingSince`.

Worsening is single-step: from `ecstatic` (0) to a computed `dying` (4) jumps directly to `dying`, not 0→1→…→4. The spec calls this out by saying "regardless of how many levels are crossed".

- [ ] **Step 1: Write hysteresis tests**

Append to `mood_test.go`:

```go
func TestCompute_Hysteresis_ImmediateWorsening(t *testing.T) {
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	good := allFresh(t0, 0, 0, 0, 0, 0)
	bad := allFresh(t0, 0, 0, 0, 0, 3) // node down → +3 → level 3
	// Start at happy.
	h := History{Current: Mood{Level: 1}, FirstSuccess: &t0}
	r := Compute(bad, h, t0.Add(10*time.Second))
	if r.Current.Level != 3 {
		t.Fatalf("immediate worsening: current = %d, want 3", r.Current.Level)
	}
	if r.History.Pending != nil {
		t.Errorf("pending must be nil after worsening, got %+v", r.History.Pending)
	}
	_ = good
}

func TestCompute_Hysteresis_ImprovementHeldFor5Minutes(t *testing.T) {
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	good := allFresh(t0, 0, 0, 0, 0, 0)
	h := History{Current: Mood{Level: 3}, FirstSuccess: &t0}
	// Tick at t+0: pending starts, current still 3.
	r := Compute(good, h, t0)
	if r.Current.Level != 3 {
		t.Fatalf("t+0 current = %d, want still 3", r.Current.Level)
	}
	if r.History.Pending == nil || r.History.Pending.Target.Level != 0 {
		t.Fatalf("t+0 pending = %+v, want target=0", r.History.Pending)
	}
	// Tick at t+4m: still pending.
	r = Compute(good, r.History, t0.Add(4*time.Minute))
	if r.Current.Level != 3 {
		t.Fatalf("t+4m current = %d, want still 3", r.Current.Level)
	}
	// Tick at t+5m: improvement applied (single-step jump to target).
	r = Compute(good, r.History, t0.Add(5*time.Minute))
	if r.Current.Level != 0 {
		t.Fatalf("t+5m current = %d, want 0", r.Current.Level)
	}
	if r.History.Pending != nil {
		t.Errorf("pending must clear after applying, got %+v", r.History.Pending)
	}
}

func TestCompute_Hysteresis_PendingResetsOnNewCandidate(t *testing.T) {
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	mid := allFresh(t0, 1, 0, 0, 0, 0) // level 1
	good := allFresh(t0, 0, 0, 0, 0, 0) // level 0
	h := History{Current: Mood{Level: 3}, FirstSuccess: &t0}
	// t+0: pending target = 1.
	r := Compute(mid, h, t0)
	if r.History.Pending == nil || r.History.Pending.Target.Level != 1 {
		t.Fatalf("t+0 pending = %+v, want target=1", r.History.Pending)
	}
	// t+3m: target shifts to 0, window resets.
	r = Compute(good, r.History, t0.Add(3*time.Minute))
	if r.History.Pending == nil || r.History.Pending.Target.Level != 0 {
		t.Fatalf("t+3m pending = %+v, want target=0 reset", r.History.Pending)
	}
	if !r.History.Pending.Since.Equal(t0.Add(3 * time.Minute)) {
		t.Errorf("Since not reset to t+3m, got %v", r.History.Pending.Since)
	}
	// t+7m (4m after reset): still pending.
	r = Compute(good, r.History, t0.Add(7*time.Minute))
	if r.Current.Level != 3 {
		t.Fatalf("t+7m current = %d, want 3 (only 4m since reset)", r.Current.Level)
	}
	// t+8m (5m after reset): applied.
	r = Compute(good, r.History, t0.Add(8*time.Minute))
	if r.Current.Level != 0 {
		t.Fatalf("t+8m current = %d, want 0", r.Current.Level)
	}
}

func TestCompute_Hysteresis_StableClearsPending(t *testing.T) {
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	good := allFresh(t0, 0, 0, 0, 0, 0)
	h := History{
		Current:      Mood{Level: 0},
		FirstSuccess: &t0,
		Pending:      &Pending{Target: Mood{Level: 0}, Since: t0.Add(-3 * time.Minute)},
	}
	r := Compute(good, h, t0)
	if r.History.Pending != nil {
		t.Errorf("pending should clear on stable, got %+v", r.History.Pending)
	}
}

// allFresh constructs a Sources where every source is loaded, fresh at
// the given timestamp, and carries the supplied penalty (in declaration
// order: argocd, longhorn, certs, restarts, nodes).
func allFresh(now time.Time, a, l, c, r, n int) Sources {
	mk := func(p int) Source { return Source{Loaded: true, LastSuccess: now, Penalty: p} }
	return Sources{ArgoCD: mk(a), Longhorn: mk(l), Certs: mk(c), Restarts: mk(r), Nodes: mk(n)}
}
```

(Delete the bogus `nodesBad` and `TestCompute_Hysteresis_ImmediateWorsening` from the first draft — keep only `_Real` and the rest.)

- [ ] **Step 2: Run to verify failure**

Run: `go test -race -v -run TestCompute_Hysteresis ./internal/health/`
Expected: FAIL — `undefined: History, Pending, Result, Compute`.

- [ ] **Step 3: Implement History, Pending, Result, Compute**

Append to `mood.go`:

```go
// Pending is an in-flight improvement candidate that the algorithm holds
// before applying. Target is the better mood the calculator wants to
// move to; Since is when that target first appeared. After
// improvementHold time has elapsed at the same Target, the algorithm
// promotes Pending into Current and clears Pending.
type Pending struct {
	Target Mood
	Since  time.Time
}

// History is the persisted state the mood algorithm threads through ticks.
// It lives in cmd/tamagotchi/state.go and is updated atomically per tick.
type History struct {
	Current      Mood
	Pending      *Pending
	FirstSuccess *time.Time // nil until the first tick where any source is Loaded
}

// Result is what Compute returns: the updated history plus presentation
// data for the handlers.
type Result struct {
	History      History
	Current      Mood
	StaleSources []string // names of sources that have data but are older than stalenessWindow
	Confused     bool     // ≥2 stale sources (per spec line 104)
}

const improvementHold = 5 * time.Minute

// Compute applies the spec's mood algorithm to the given Sources, threading
// History across ticks. The function is pure — all time inputs come via
// `now` — so tests can drive it deterministically without a Clock interface.
func Compute(s Sources, h History, now time.Time) Result {
	// Init grace + staleness reporting are added in Task 5; this task
	// implements the hysteresis state machine over fresh, loaded sources.

	level := SumPenalty(s, now)
	next := Mood{Level: level}

	switch {
	case next.Level > h.Current.Level:
		// Worsening — single-step jump, discard any pending improvement.
		h.Current = next
		h.Pending = nil
	case next.Level < h.Current.Level:
		// Improvement candidate.
		switch {
		case h.Pending == nil || h.Pending.Target.Level != next.Level:
			// New candidate or target shift — (re)start the window.
			h.Pending = &Pending{Target: next, Since: now}
		case now.Sub(h.Pending.Since) >= improvementHold:
			// Window has elapsed at the same target — apply.
			h.Current = h.Pending.Target
			h.Pending = nil
		}
	default:
		// Stable — discard any pending improvement (it's no longer needed).
		h.Pending = nil
	}

	return Result{History: h, Current: h.Current}
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -race -v -run TestCompute_Hysteresis ./internal/health/`
Expected: PASS — four hysteresis subtests green.

- [ ] **Step 5: Lint**

Run: `golangci-lint run ./internal/health/...`
Expected: clean. ST1021 reminder: doc comments above generic types must start with the bare type name; nothing here is generic so we're fine. The doc comment for `History` references a future file — that's a known forward reference and acceptable.

- [ ] **Step 6: Commit**

```bash
git add internal/health/
git commit -m "feat(health): add Compute() with hysteresis state machine"
```

---

### Task 5: Init grace + confused state + stale-sources reporting

**Files:**
- Modify: `internal/health/mood.go`
- Test: `internal/health/mood_test.go`

The remaining spec semantics:

- **Init grace:** `History.FirstSuccess == nil`, no source Loaded yet → `Current = happy` (level 1), no penalty applied. As soon as any source is Loaded (FirstSuccess assigned), set `Current = computed level` immediately and skip the hysteresis improvement window for that one tick.
- **Stale-source skipping:** sources older than `stalenessWindow` are excluded from `SumPenalty` (already in Task 3) but their names are reported in `Result.StaleSources`.
- **Confused:** `len(StaleSources) >= 2` → `Result.Confused = true`. The handler renders the confused sprite variant; `Current` is still set per the algorithm, but the algorithm explicitly skips stale sources so a half-cluster outage doesn't drive the pet to dying.

- [ ] **Step 1: Write tests**

Append to `mood_test.go`:

```go
func TestCompute_InitGrace_NoSourcesYet(t *testing.T) {
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	s := Sources{} // nothing Loaded
	r := Compute(s, History{}, t0)
	if r.Current.Level != 1 {
		t.Fatalf("init current = %d, want 1 (happy)", r.Current.Level)
	}
	if r.History.FirstSuccess != nil {
		t.Errorf("FirstSuccess should still be nil, got %v", *r.History.FirstSuccess)
	}
}

func TestCompute_InitGrace_FirstSuccessAdoptsComputedLevel(t *testing.T) {
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	// Bad data on first poll (level 3) — adopt immediately, no 5-min wait.
	bad := allFresh(t0, 0, 0, 0, 0, 3)
	r := Compute(bad, History{}, t0)
	if r.Current.Level != 3 {
		t.Fatalf("first success current = %d, want 3 (immediate adopt)", r.Current.Level)
	}
	if r.History.FirstSuccess == nil || !r.History.FirstSuccess.Equal(t0) {
		t.Fatalf("FirstSuccess = %v, want set to %v", r.History.FirstSuccess, t0)
	}
}

func TestCompute_StaleSourcesReported(t *testing.T) {
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	old := t0.Add(-10 * time.Minute) // > stalenessWindow
	s := Sources{
		ArgoCD:   Source{Loaded: true, LastSuccess: old, Penalty: 1},
		Longhorn: Source{Loaded: true, LastSuccess: t0, Penalty: 0},
		Certs:    Source{Loaded: true, LastSuccess: t0, Penalty: 0},
		Restarts: Source{Loaded: true, LastSuccess: t0, Penalty: 0},
		Nodes:    Source{Loaded: true, LastSuccess: t0, Penalty: 0},
	}
	r := Compute(s, History{Current: Mood{Level: 1}, FirstSuccess: &t0}, t0)
	if got := r.StaleSources; len(got) != 1 || got[0] != "argocd" {
		t.Errorf("StaleSources = %v, want [argocd]", got)
	}
	if r.Confused {
		t.Errorf("Confused = true, want false (only 1 stale)")
	}
	// Stale source's penalty must not contribute.
	if r.Current.Level != 1 {
		// Hysteresis: from happy with all-zero fresh signals → improvement window starts, current stays at 1.
		// This is the right behaviour; stale ArgoCD's penalty=1 is excluded.
		t.Errorf("Current.Level = %d, want still 1 (stale penalty excluded)", r.Current.Level)
	}
}

func TestCompute_TwoStaleSourcesConfused(t *testing.T) {
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	old := t0.Add(-10 * time.Minute)
	s := Sources{
		ArgoCD:   Source{Loaded: true, LastSuccess: old},
		Longhorn: Source{Loaded: true, LastSuccess: old},
		Certs:    Source{Loaded: true, LastSuccess: t0},
		Restarts: Source{Loaded: true, LastSuccess: t0},
		Nodes:    Source{Loaded: true, LastSuccess: t0},
	}
	r := Compute(s, History{Current: Mood{Level: 0}, FirstSuccess: &t0}, t0)
	if !r.Confused {
		t.Fatalf("Confused = false, want true (2 stale)")
	}
	if want := []string{"argocd", "longhorn"}; !equalStrings(r.StaleSources, want) {
		t.Errorf("StaleSources = %v, want %v", r.StaleSources, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run to see failures**

Run: `go test -race -v ./internal/health/`
Expected: FAIL on the four new tests. (Existing tests still pass.)

- [ ] **Step 3: Implement init grace + stale reporting + confused**

Replace the `Compute` function body in `mood.go` with the following (and keep all the types from Task 4 unchanged):

```go
// Compute applies the spec's mood algorithm to the given Sources, threading
// History across ticks. Pure function — all time inputs come via `now`.
func Compute(s Sources, h History, now time.Time) Result {
	stale, anyLoaded := classifySources(s, now)

	// Init grace: no source has ever been Loaded yet — show happy and
	// skip the algorithm entirely. The handler displays "Hallo!" while
	// FirstSuccess remains nil.
	if !anyLoaded {
		h.Current = Mood{Level: 1}
		return Result{History: h, Current: h.Current, StaleSources: stale, Confused: len(stale) >= 2}
	}

	level := SumPenalty(s, now)
	next := Mood{Level: level}

	// First successful tick — adopt the computed level immediately,
	// bypassing the hysteresis improvement window.
	if h.FirstSuccess == nil {
		now := now // pin
		h.FirstSuccess = &now
		h.Current = next
		h.Pending = nil
		return Result{History: h, Current: h.Current, StaleSources: stale, Confused: len(stale) >= 2}
	}

	// Standard hysteresis state machine.
	switch {
	case next.Level > h.Current.Level:
		h.Current = next
		h.Pending = nil
	case next.Level < h.Current.Level:
		switch {
		case h.Pending == nil || h.Pending.Target.Level != next.Level:
			h.Pending = &Pending{Target: next, Since: now}
		case now.Sub(h.Pending.Since) >= improvementHold:
			h.Current = h.Pending.Target
			h.Pending = nil
		}
	default:
		h.Pending = nil
	}

	return Result{History: h, Current: h.Current, StaleSources: stale, Confused: len(stale) >= 2}
}

// classifySources returns the names of stale sources (Loaded but older
// than stalenessWindow) plus a flag set to true if any source is Loaded
// at all (used to detect the init-grace boundary).
//
// Order is fixed (argocd, longhorn, certs, restarts, nodes) so the slice
// is stable across calls — the JSON handler and tests both rely on this.
func classifySources(s Sources, now time.Time) ([]string, bool) {
	var stale []string
	anyLoaded := false
	type ent struct {
		name string
		src  Source
	}
	for _, e := range []ent{
		{"argocd", s.ArgoCD},
		{"longhorn", s.Longhorn},
		{"certs", s.Certs},
		{"restarts", s.Restarts},
		{"nodes", s.Nodes},
	} {
		if e.src.Loaded {
			anyLoaded = true
			if now.Sub(e.src.LastSuccess) > stalenessWindow {
				stale = append(stale, e.name)
			}
		}
	}
	return stale, anyLoaded
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -race -v ./internal/health/`
Expected: PASS — all tests, including the four hysteresis cases from Task 4.

- [ ] **Step 5: Lint**

Run: `golangci-lint run ./internal/health/...`
Expected: clean. (Note: the inner `now := now` shadow rebinds the parameter to a local so we can take its address; this pattern is idiomatic and not flagged.)

- [ ] **Step 6: Commit**

```bash
git add internal/health/
git commit -m "feat(health): add init grace, stale-source reporting, confused flag"
```

---

## Phase 3 — Sprite generator

### Task 6: Pixel-matrix → SVG sprite

**Files:**
- Create: `cmd/tamagotchi/sprite.go`
- Test: `cmd/tamagotchi/sprite_test.go`

The spec (lines 110–112) wants per-mood 64×64 pixel matrices, palette per mood, one `<rect width="1" height="1">` per non-background pixel. The body class `mood-<name>` selects the per-mood CSS animation (handled in templates, not here).

For this task we keep the matrices small and deliberately blocky — a v1 pet shape — so byte counts stay under the 16 KiB practical SVG-inline ceiling. Each mood's matrix is a `[]string` of length 64, 64 chars wide, where space=transparent and any other char is a palette index.

The sprite generator is also where `confused` modifier is rendered (a `?` floating above the head) — this overlays the base sprite when the mood algorithm reports `Confused = true`.

- [ ] **Step 1: Write tests for the SVG emitter**

```go
// cmd/tamagotchi/sprite_test.go
package main

import (
	"strings"
	"testing"
)

func TestRenderSprite_ContainsMoodClass(t *testing.T) {
	tests := []string{"ecstatic", "happy", "meh", "sick", "dying"}
	for _, mood := range tests {
		t.Run(mood, func(t *testing.T) {
			svg := RenderSprite(mood, false)
			if !strings.Contains(svg, `class="sprite mood-`+mood+`"`) {
				t.Errorf("mood %q: missing class, got: %s", mood, svg[:min(200, len(svg))])
			}
			if !strings.Contains(svg, `viewBox="0 0 64 64"`) {
				t.Errorf("mood %q: missing viewBox", mood)
			}
		})
	}
}

func TestRenderSprite_Confused_AppendsQuestionMark(t *testing.T) {
	plain := RenderSprite("happy", false)
	confused := RenderSprite("happy", true)
	if confused == plain {
		t.Fatal("confused output identical to plain")
	}
	// The "?" overlay is rendered as a <text> element near top-right.
	if !strings.Contains(confused, "<text") || !strings.Contains(confused, "?") {
		t.Error("confused variant missing <text>?</text> overlay")
	}
}

func TestRenderSprite_DyingHasNoBounceAnimationClass(t *testing.T) {
	svg := RenderSprite("dying", false)
	// dying mood is "lying flat, no movement" per spec — but the CSS
	// animation is keyed off the mood-<name> body class, not the SVG. So
	// this test just sanity-checks dying still renders as a valid sprite
	// with the right class.
	if !strings.Contains(svg, "mood-dying") {
		t.Error("dying sprite missing mood-dying class")
	}
}

func TestRenderSprite_RectCountReasonable(t *testing.T) {
	// Each non-space cell becomes one <rect>. Sanity-check that a 64×64
	// matrix doesn't accidentally emit > 1500 rects (would mean the
	// matrix is mostly filled, which is wrong for a pet sprite).
	svg := RenderSprite("happy", false)
	rectCount := strings.Count(svg, "<rect")
	if rectCount < 30 || rectCount > 1500 {
		t.Errorf("rect count = %d, expected 30..1500", rectCount)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -race -v -run TestRenderSprite ./cmd/tamagotchi/`
Expected: FAIL — package doesn't compile (`undefined: RenderSprite`).

- [ ] **Step 3: Implement sprite matrices + RenderSprite**

```go
// cmd/tamagotchi/sprite.go
package main

import (
	"fmt"
	"strings"
)

// spriteSize is the matrix dimension. Standalone view scales by 8 in CSS,
// widget by 2; image-rendering: pixelated keeps each cell crisp.
const spriteSize = 64

// moodMatrix is one mood's 64×64 grid. Space = transparent; any other
// rune is a palette index (single character, 0..9 or a..z).
type moodMatrix [spriteSize]string

// moodPalette maps single-character cell IDs to hex colour strings.
type moodPalette map[byte]string

// matrices and palettes are seeded with deliberately simple shapes so the
// total inline-SVG payload stays small. A real artist can refine these
// later — the structure is the contract.
//
// Layout convention (rough):
//   rows  0–6: empty (room for the confused "?" overlay)
//   rows  7–14: ear/antenna line
//   rows 15–46: head (round-ish 32×32 in the centre)
//   rows 47–58: body
//   rows 59–63: feet
//
// Per-mood differences:
//   ecstatic: wide smile, sparkles
//   happy:    standard smile
//   meh:      flat mouth
//   sick:     droopy eyes, slight green tint
//   dying:    lying flat — body shape compressed to bottom rows
var matrices = map[string]moodMatrix{
	"ecstatic": ecstaticMatrix(),
	"happy":    happyMatrix(),
	"meh":      mehMatrix(),
	"sick":     sickMatrix(),
	"dying":    dyingMatrix(),
}

var palettes = map[string]moodPalette{
	"ecstatic": {'1': "#ffd84a", '2': "#1d1d1d", '3': "#ff6f9c"},
	"happy":    {'1': "#ffd84a", '2': "#1d1d1d"},
	"meh":      {'1': "#cdc78a", '2': "#1d1d1d"},
	"sick":     {'1': "#9bc18a", '2': "#1d1d1d", '3': "#5a7a4f"},
	"dying":    {'1': "#7a7a7a", '2': "#1d1d1d"},
}

// RenderSprite emits one sprite as inline SVG. The wrapping <svg>
// element carries class="sprite mood-<name>" so per-mood CSS animations
// can target it. Confused=true overlays a "?" near the top-right.
func RenderSprite(mood string, confused bool) string {
	m, ok := matrices[mood]
	if !ok {
		m = matrices["happy"]
		mood = "happy"
	}
	palette := palettes[mood]

	var b strings.Builder
	b.Grow(8 * 1024)
	fmt.Fprintf(&b, `<svg class="sprite mood-%s" viewBox="0 0 64 64" xmlns="http://www.w3.org/2000/svg" shape-rendering="crispEdges">`, mood)

	for y, row := range m {
		for x := 0; x < len(row) && x < spriteSize; x++ {
			c := row[x]
			if c == ' ' {
				continue
			}
			colour, ok := palette[c]
			if !ok {
				continue // unknown palette index → skip
			}
			fmt.Fprintf(&b, `<rect x="%d" y="%d" width="1" height="1" fill="%s"/>`, x, y, colour)
		}
	}

	if confused {
		// "?" floats above the head. Big enough to read at scale=2 (widget).
		b.WriteString(`<text x="48" y="10" font-family="monospace" font-size="10" font-weight="bold" fill="#fff" stroke="#000" stroke-width="0.4">?</text>`)
	}

	b.WriteString(`</svg>`)
	return b.String()
}

// happyMatrix returns the v1 happy-pet matrix. The shape is a round head
// with two dot-eyes and an upward-curving mouth, body below with two
// stub legs. Easy to tweak: each row is a 64-char string.
func happyMatrix() moodMatrix {
	return baseRoundShape("smile")
}

func ecstaticMatrix() moodMatrix {
	m := baseRoundShape("bigsmile")
	// Sparkles around the head (rows 12–18, cols 8 and 56).
	for _, p := range [][2]int{{12, 8}, {14, 56}, {16, 9}, {18, 55}} {
		row := []byte(m[p[0]])
		row[p[1]] = '3'
		m[p[0]] = string(row)
	}
	return m
}

func mehMatrix() moodMatrix {
	return baseRoundShape("flat")
}

func sickMatrix() moodMatrix {
	m := baseRoundShape("frown")
	// Add a downward droop to eye rows (rows 24–26, cols 22 and 42 use palette index 3).
	for _, y := range []int{24, 25, 26} {
		row := []byte(m[y])
		if row[22] == '2' {
			row[22] = '3'
		}
		if row[42] == '2' {
			row[42] = '3'
		}
		m[y] = string(row)
	}
	return m
}

func dyingMatrix() moodMatrix {
	// Lying flat — fill only rows 50–60, body sideways. Head on the
	// right, feet sticking up at the left. No animation needed; the CSS
	// .mood-dying body class skips the bounce.
	var m moodMatrix
	for y := 50; y <= 60; y++ {
		row := make([]byte, spriteSize)
		for x := 0; x < spriteSize; x++ {
			row[x] = ' '
		}
		// Head circle on the right
		if y >= 51 && y <= 58 {
			for x := 36; x <= 56; x++ {
				row[x] = '1'
			}
		}
		// Body extending left
		if y >= 53 && y <= 56 {
			for x := 14; x < 36; x++ {
				row[x] = '1'
			}
		}
		// X-eyes on the head
		if y == 54 {
			row[44] = '2'
			row[48] = '2'
		}
		if y == 55 {
			row[45] = '2'
			row[47] = '2'
		}
		m[y] = string(row)
	}
	// Empty top rows
	for y := 0; y < 50; y++ {
		m[y] = strings.Repeat(" ", spriteSize)
	}
	for y := 61; y < 64; y++ {
		m[y] = strings.Repeat(" ", spriteSize)
	}
	return m
}

// baseRoundShape returns a head/body/legs sprite with the given mouth
// variant ("smile", "bigsmile", "flat", "frown"). Centralising this
// removes ~150 lines of literal-row duplication across moods.
func baseRoundShape(mouth string) moodMatrix {
	var m moodMatrix
	for y := 0; y < spriteSize; y++ {
		row := make([]byte, spriteSize)
		for x := 0; x < spriteSize; x++ {
			row[x] = ' '
		}
		// Head: rows 12..40, roughly circular around (32, 26) radius 14.
		if y >= 12 && y <= 40 {
			cy := 26
			r2 := 14 * 14
			for x := 0; x < spriteSize; x++ {
				dx := x - 32
				dy := y - cy
				if dx*dx+dy*dy <= r2 {
					row[x] = '1'
				}
			}
		}
		// Body: rows 41..56, narrower oval.
		if y >= 41 && y <= 56 {
			cy := 48
			r2 := 12 * 12
			for x := 0; x < spriteSize; x++ {
				dx := x - 32
				dy := y - cy
				if dx*dx+dy*dy <= r2 {
					row[x] = '1'
				}
			}
		}
		// Legs: rows 57..62, two stubs.
		if y >= 57 && y <= 62 {
			for x := 24; x <= 28; x++ {
				row[x] = '1'
			}
			for x := 36; x <= 40; x++ {
				row[x] = '1'
			}
		}
		m[y] = string(row)
	}
	// Eyes: dots at (22, 24) and (42, 24).
	for y := 23; y <= 25; y++ {
		row := []byte(m[y])
		row[22] = '2'
		row[42] = '2'
		m[y] = string(row)
	}
	// Mouth: rows 32–34 depending on variant.
	switch mouth {
	case "bigsmile":
		// Wide upturn: row 31 ends, row 32 middle, row 33 dips.
		drawMouth(&m, []struct{ y, lx, rx int }{{31, 24, 40}, {32, 25, 39}, {33, 28, 36}})
	case "smile":
		drawMouth(&m, []struct{ y, lx, rx int }{{32, 26, 38}, {33, 28, 36}})
	case "flat":
		drawMouth(&m, []struct{ y, lx, rx int }{{33, 27, 37}})
	case "frown":
		// Inverted: row 33 ends, row 32 middle.
		drawMouth(&m, []struct{ y, lx, rx int }{{33, 24, 40}, {32, 28, 36}})
	}
	return m
}

func drawMouth(m *moodMatrix, segs []struct{ y, lx, rx int }) {
	for _, seg := range segs {
		row := []byte(m[seg.y])
		for x := seg.lx; x <= seg.rx; x++ {
			row[x] = '2'
		}
		m[seg.y] = string(row)
	}
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -race -v -run TestRenderSprite ./cmd/tamagotchi/`
Expected: PASS — four subtests green.

- [ ] **Step 5: Lint**

Run: `golangci-lint run ./cmd/tamagotchi/...`
Expected: clean. The `min()` helper exists in stdlib since Go 1.21 — but our test redefines it. If golangci-lint flags the redeclaration, rename the local helper to `imin` or drop it (Go 1.26 has `min` as a builtin). On Go 1.26, drop the local `min` and use the builtin.

- [ ] **Step 6: Commit**

```bash
git add cmd/tamagotchi/sprite.go cmd/tamagotchi/sprite_test.go
git commit -m "feat(tamagotchi): add per-mood pixel sprite generator"
```

---

## Phase 4 — State + aggregator

### Task 7: `State` struct mirroring cluster-tv with mood/birthday extensions

**Files:**
- Create: `cmd/tamagotchi/state.go`
- Test: `cmd/tamagotchi/state_test.go`

The cluster-tv State pattern uses `Slot[T any]` to wrap data + heartbeat fields. Tamagotchi's per-source data is just an `int` penalty (the aggregator computes the penalty from raw source data before writing to State), so we use `Slot[int]` for all five sources. The State additionally carries `Birthday` (set once at startup) and the mood `History` for hysteresis.

The State exposes:
- `Set<Source>(penalty int, now time.Time)` — success path
- `Set<Source>Error(err error, now time.Time)` — failure path (preserves previous Penalty)
- `SetBirthday(birthday time.Time)` — called once at startup
- `Snapshot()` — returns a deep-copied Snapshot
- `UpdateMood(now time.Time)` — runs `health.Compute()`, updates `History` and `Mood`/`Confused`/`StaleSources` under the write lock; called by the aggregator at end of every tick

- [ ] **Step 1: Test scaffolding**

```go
// cmd/tamagotchi/state_test.go
package main

import (
	"errors"
	"testing"
	"time"
)

func TestState_SetAndSnapshot(t *testing.T) {
	s := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	s.SetArgoCD(1, now)
	s.SetLonghorn(0, now)
	s.SetCerts(1, now)
	s.SetRestarts(2, now)
	s.SetNodes(0, now)
	s.SetBirthday(now.Add(-3 * 24 * time.Hour))

	snap := s.Snapshot()
	if !snap.ArgoCD.Loaded || snap.ArgoCD.Data != 1 {
		t.Errorf("argocd: %+v", snap.ArgoCD)
	}
	if !snap.Restarts.Loaded || snap.Restarts.Data != 2 {
		t.Errorf("restarts: %+v", snap.Restarts)
	}
	if snap.Birthday.IsZero() {
		t.Error("birthday not set")
	}
}

func TestState_ErrorPreservesPreviousPenalty(t *testing.T) {
	s := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	s.SetArgoCD(1, now)
	s.SetArgoCDError(errors.New("argocd unreachable"), now.Add(time.Minute))

	snap := s.Snapshot()
	if snap.ArgoCD.Data != 1 {
		t.Errorf("data lost on error: got %d, want 1", snap.ArgoCD.Data)
	}
	if !snap.ArgoCD.Loaded {
		t.Error("Loaded flipped on error — should preserve last-known good")
	}
	if snap.ArgoCD.LastError == "" {
		t.Error("LastError not recorded")
	}
	if !snap.ArgoCD.LastFailure.Equal(now.Add(time.Minute)) {
		t.Errorf("LastFailure = %v, want %v", snap.ArgoCD.LastFailure, now.Add(time.Minute))
	}
}

func TestState_UpdateMood_RunsCompute(t *testing.T) {
	s := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	s.SetArgoCD(0, now)
	s.SetLonghorn(0, now)
	s.SetCerts(0, now)
	s.SetRestarts(0, now)
	s.SetNodes(3, now) // node-down
	s.UpdateMood(now)

	snap := s.Snapshot()
	if snap.Mood.Level != 4 {
		t.Errorf("Mood.Level = %d, want 4 (dying)", snap.Mood.Level)
	}
}

func TestState_UpdateMood_BeforeAnySourceLoadedHappy(t *testing.T) {
	s := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	s.UpdateMood(now)
	snap := s.Snapshot()
	if snap.Mood.Level != 1 {
		t.Errorf("init mood = %d, want 1 (happy)", snap.Mood.Level)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test -race -v -run TestState ./cmd/tamagotchi/`
Expected: FAIL — package doesn't compile.

- [ ] **Step 3: Implement state.go**

```go
// cmd/tamagotchi/state.go
package main

import (
	"sync"
	"time"

	"github.com/madic-creates/homelab-toys/internal/health"
)

// stalenessWindow mirrors cluster-tv's threshold. Stale sources are
// excluded from the mood penalty sum and reported via StaleSources.
const stalenessWindow = 5 * time.Minute

// Slot holds the most recent successful payload plus heartbeat metadata
// for one source. Layout matches cluster-tv's Slot[T] — duplicated rather
// than shared because both binaries are small enough that a 30-line
// generic type doesn't justify a new internal/ package.
type Slot[T any] struct {
	Data        T         `json:"data"`
	LastSuccess time.Time `json:"last_success"`
	LastFailure time.Time `json:"last_failure,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	Loaded      bool      `json:"loaded"`
}

// IsStale reports whether the source has not had a successful poll within
// the staleness window. Unloaded sources are always stale.
func (s Slot[T]) IsStale(now time.Time) bool {
	if !s.Loaded {
		return true
	}
	return now.Sub(s.LastSuccess) > stalenessWindow
}

// Snapshot is the public, deep-copied view of State at one moment.
type Snapshot struct {
	ArgoCD       Slot[int] `json:"argocd"`
	Longhorn     Slot[int] `json:"longhorn"`
	Certs        Slot[int] `json:"certs"`
	Restarts     Slot[int] `json:"restarts"`
	Nodes        Slot[int] `json:"nodes"`
	Birthday     time.Time `json:"birthday"`
	Mood         health.Mood
	Confused     bool
	StaleSources []string
	HasFirstTick bool // mirrors history.FirstSuccess != nil — used by handlers for the Hallo bubble
}

// State is the aggregator's shared store. Per-source fields hold an int
// penalty (computed by the pollFunc) plus heartbeat metadata. Birthday
// and history.History are tamagotchi-specific.
type State struct {
	mu       sync.RWMutex
	snap     Snapshot
	history  health.History
	birthday time.Time
}

func NewState() *State {
	return &State{}
}

func (s *State) SetArgoCD(p int, now time.Time)   { s.setSource(&s.snap.ArgoCD, p, now) }
func (s *State) SetLonghorn(p int, now time.Time) { s.setSource(&s.snap.Longhorn, p, now) }
func (s *State) SetCerts(p int, now time.Time)    { s.setSource(&s.snap.Certs, p, now) }
func (s *State) SetRestarts(p int, now time.Time) { s.setSource(&s.snap.Restarts, p, now) }
func (s *State) SetNodes(p int, now time.Time)    { s.setSource(&s.snap.Nodes, p, now) }

func (s *State) SetArgoCDError(err error, now time.Time)   { s.setSourceError(&s.snap.ArgoCD, err, now) }
func (s *State) SetLonghornError(err error, now time.Time) { s.setSourceError(&s.snap.Longhorn, err, now) }
func (s *State) SetCertsError(err error, now time.Time)    { s.setSourceError(&s.snap.Certs, err, now) }
func (s *State) SetRestartsError(err error, now time.Time) { s.setSourceError(&s.snap.Restarts, err, now) }
func (s *State) SetNodesError(err error, now time.Time)    { s.setSourceError(&s.snap.Nodes, err, now) }

func (s *State) setSource(slot *Slot[int], p int, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot.Data = p
	slot.LastSuccess = now
	slot.LastError = ""
	slot.Loaded = true
}

func (s *State) setSourceError(slot *Slot[int], err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot.LastFailure = now
	slot.LastError = err.Error()
}

// SetBirthday is called once at process startup with the pod's
// creationTimestamp. Subsequent calls overwrite (cheap; would only happen
// in test code).
func (s *State) SetBirthday(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.birthday = t
	s.snap.Birthday = t
}

// UpdateMood runs the mood algorithm against the current source state and
// stores the result on the snapshot. The aggregator calls this at the end
// of every tick (after writing per-source updates), so the read side
// always observes a coherent (sources, mood) pair under one snapshot.
func (s *State) UpdateMood(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := health.Sources{
		ArgoCD:   slotToSource(s.snap.ArgoCD),
		Longhorn: slotToSource(s.snap.Longhorn),
		Certs:    slotToSource(s.snap.Certs),
		Restarts: slotToSource(s.snap.Restarts),
		Nodes:    slotToSource(s.snap.Nodes),
	}
	res := health.Compute(src, s.history, now)
	s.history = res.History
	s.snap.Mood = res.Current
	s.snap.Confused = res.Confused
	s.snap.StaleSources = append(s.snap.StaleSources[:0], res.StaleSources...)
	s.snap.HasFirstTick = s.history.FirstSuccess != nil
}

func slotToSource(sl Slot[int]) health.Source {
	return health.Source{
		Loaded:      sl.Loaded,
		LastSuccess: sl.LastSuccess,
		Penalty:     sl.Data,
	}
}

// Snapshot returns a deep-copied view. The StaleSources slice is cloned
// so callers can mutate freely.
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.snap
	if len(s.snap.StaleSources) > 0 {
		out.StaleSources = append([]string(nil), s.snap.StaleSources...)
	} else {
		out.StaleSources = nil
	}
	return out
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -race -v -run TestState ./cmd/tamagotchi/`
Expected: PASS — four subtests green.

- [ ] **Step 5: Snapshot deep-copy test**

Append to `state_test.go`:

```go
func TestState_SnapshotIsCopy(t *testing.T) {
	s := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	s.SetArgoCD(0, now)
	s.SetLonghorn(0, now)
	s.SetCerts(0, now.Add(-10*time.Minute)) // stale → reported in StaleSources
	s.SetRestarts(0, now.Add(-10*time.Minute))
	s.SetNodes(0, now)
	s.UpdateMood(now)

	snap := s.Snapshot()
	if len(snap.StaleSources) != 2 {
		t.Fatalf("StaleSources = %v", snap.StaleSources)
	}
	// Mutate the copy.
	snap.StaleSources[0] = "MUTATED"
	// Re-snapshot and assert the original is intact.
	again := s.Snapshot()
	if again.StaleSources[0] == "MUTATED" {
		t.Error("Snapshot did not deep-copy StaleSources")
	}
}
```

Run: `go test -race -v -run TestState_SnapshotIsCopy ./cmd/tamagotchi/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/tamagotchi/state.go cmd/tamagotchi/state_test.go
git commit -m "feat(tamagotchi): add State with per-source penalties + mood update"
```

---

### Task 8: Aggregator — runSource scaffolding (port from cluster-tv) + nodes poll

**Files:**
- Create: `cmd/tamagotchi/aggregator.go`
- Test: `cmd/tamagotchi/aggregator_test.go`

The `runSource(ctx, name, poll, interval, m)` function is structurally identical to `cmd/cluster-tv/aggregator.go`. We duplicate it here because it uses the `pollFunc` and `metricsRecorder` types local to the binary; YAGNI says don't extract to `internal/` until a third binary exists.

This task ports the runner + adds the `MakeNodesPoll` factory. ArgoCD/Longhorn/certs/restarts polls land in Task 9 (they're shaped almost identically to cluster-tv, so they go together).

- [ ] **Step 1: Test the runner with a fake poll function**

```go
// cmd/tamagotchi/aggregator_test.go
package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeMetrics struct {
	calls atomic.Int64
}

func (f *fakeMetrics) PollTotal(_, _ string)                  { f.calls.Add(1) }
func (f *fakeMetrics) LastSuccessSeconds(_ string, _ float64) {}

func TestRunSource_TickAndCancel(t *testing.T) {
	m := &fakeMetrics{}
	var pollCount atomic.Int64
	poll := func(_ context.Context) error {
		pollCount.Add(1)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runSourceWithBackoff(ctx, "test", poll, 20*time.Millisecond, time.Millisecond, m)
		close(done)
	}()
	time.Sleep(70 * time.Millisecond)
	cancel()
	<-done
	if pollCount.Load() < 2 {
		t.Errorf("expected ≥2 polls, got %d", pollCount.Load())
	}
	if m.calls.Load() < 2 {
		t.Errorf("expected ≥2 metric records, got %d", m.calls.Load())
	}
}

func TestRunSource_ErrorIsLoggedNotPropagated(t *testing.T) {
	m := &fakeMetrics{}
	poll := func(_ context.Context) error { return errors.New("boom") }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runSourceWithBackoff(ctx, "test", poll, 10*time.Millisecond, time.Millisecond, m)
		close(done)
	}()
	time.Sleep(40 * time.Millisecond)
	cancel()
	<-done
	if m.calls.Load() < 2 {
		t.Errorf("error path should still record metrics; got %d", m.calls.Load())
	}
}

func TestRunSource_PanicRecoveredAndRestarts(t *testing.T) {
	m := &fakeMetrics{}
	var pollCount atomic.Int64
	poll := func(_ context.Context) error {
		pollCount.Add(1)
		if pollCount.Load() == 1 {
			panic("first call panics")
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runSourceWithBackoff(ctx, "test", poll, 10*time.Millisecond, time.Millisecond, m)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if pollCount.Load() < 2 {
		t.Errorf("expected restart after panic, got %d polls", pollCount.Load())
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test -race -v -run TestRunSource ./cmd/tamagotchi/`
Expected: FAIL — `undefined: runSourceWithBackoff`.

- [ ] **Step 3: Port runSource from cluster-tv**

```go
// cmd/tamagotchi/aggregator.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/madic-creates/homelab-toys/internal/kube"
)

// pollFunc is what each source goroutine actually runs.
type pollFunc func(ctx context.Context) error

// metricsRecorder is what the aggregator needs from the metrics layer.
// *Handlers in handlers.go satisfies this interface.
type metricsRecorder interface {
	PollTotal(source, result string)
	LastSuccessSeconds(source string, seconds float64)
}

const defaultBackoff = 10 * time.Second

// runSource is the production wrapper around runSourceWithBackoff, using
// the spec's 10-second post-panic backoff.
func runSource(ctx context.Context, name string, poll pollFunc, interval time.Duration, m metricsRecorder) {
	runSourceWithBackoff(ctx, name, poll, interval, defaultBackoff, m)
}

// runSourceWithBackoff is split out so tests can inject a short backoff.
func runSourceWithBackoff(ctx context.Context, name string, poll pollFunc, interval, backoff time.Duration, m metricsRecorder) {
	for ctx.Err() == nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("source panic", "source", name, "panic", fmt.Sprint(r))
					if m != nil {
						m.PollTotal(name, "panic")
					}
				}
			}()
			tickOnce(ctx, name, poll, m)
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					tickOnce(ctx, name, poll, m)
				}
			}
		}()
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func tickOnce(ctx context.Context, name string, poll pollFunc, m metricsRecorder) {
	if err := poll(ctx); err != nil {
		slog.Warn("source poll failed", "source", name, "error", err)
		if m != nil {
			m.PollTotal(name, "error")
		}
		return
	}
	if m != nil {
		m.PollTotal(name, "success")
		m.LastSuccessSeconds(name, float64(time.Now().Unix()))
	}
}

// MakeNodesPoll returns a pollFunc that lists nodes via the supplied
// clientset and writes the resulting penalty (3 if any not-ready, else 0)
// into the State.
func MakeNodesPoll(lister NodesLister, st *State, now func() time.Time) pollFunc {
	return func(ctx context.Context) error {
		nodes, err := lister.Nodes(ctx)
		if err != nil {
			st.SetNodesError(err, now())
			return err
		}
		penalty := 0
		for _, n := range nodes {
			if !n.Ready {
				penalty = 3
				break
			}
		}
		st.SetNodes(penalty, now())
		return nil
	}
}

// NodesLister is the indirection that lets tests stub out kube access.
// In production, *kubeNodesLister wraps internal/kube.Nodes().
type NodesLister interface {
	Nodes(ctx context.Context) ([]kube.NodeStatus, error)
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -race -v -run TestRunSource ./cmd/tamagotchi/`
Expected: PASS — three subtests green.

- [ ] **Step 5: Test MakeNodesPoll**

Append to `aggregator_test.go`:

```go
import (
	// (add to existing imports if not already there)
	"github.com/madic-creates/homelab-toys/internal/kube"
)

type stubNodesLister struct {
	nodes []kube.NodeStatus
	err   error
}

func (s *stubNodesLister) Nodes(_ context.Context) ([]kube.NodeStatus, error) {
	return s.nodes, s.err
}

func TestMakeNodesPoll_AllReadyZero(t *testing.T) {
	st := NewState()
	now := time.Now()
	poll := MakeNodesPoll(&stubNodesLister{
		nodes: []kube.NodeStatus{{Name: "a", Ready: true}, {Name: "b", Ready: true}},
	}, st, func() time.Time { return now })
	if err := poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	snap := st.Snapshot()
	if snap.Nodes.Data != 0 || !snap.Nodes.Loaded {
		t.Errorf("nodes slot = %+v, want penalty=0 loaded=true", snap.Nodes)
	}
}

func TestMakeNodesPoll_OneNotReadyPenalty3(t *testing.T) {
	st := NewState()
	now := time.Now()
	poll := MakeNodesPoll(&stubNodesLister{
		nodes: []kube.NodeStatus{{Name: "a", Ready: true}, {Name: "b", Ready: false}},
	}, st, func() time.Time { return now })
	if err := poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if got := st.Snapshot().Nodes.Data; got != 3 {
		t.Errorf("nodes penalty = %d, want 3", got)
	}
}

func TestMakeNodesPoll_ErrorRecorded(t *testing.T) {
	st := NewState()
	now := time.Now()
	poll := MakeNodesPoll(&stubNodesLister{err: errors.New("kube down")}, st, func() time.Time { return now })
	if err := poll(context.Background()); err == nil {
		t.Fatal("want error from poll")
	}
	snap := st.Snapshot()
	if snap.Nodes.LastError == "" {
		t.Error("LastError not recorded")
	}
}
```

Run: `go test -race -v -run TestMakeNodesPoll ./cmd/tamagotchi/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/tamagotchi/aggregator.go cmd/tamagotchi/aggregator_test.go
git commit -m "feat(tamagotchi): add aggregator scaffold + nodes poll"
```

---

### Task 9: ArgoCD / Longhorn / certs / restarts polls

**Files:**
- Modify: `cmd/tamagotchi/aggregator.go`
- Modify: `cmd/tamagotchi/aggregator_test.go`

These polls reuse the `internal/argocd`, `internal/prom`, `internal/certs` clients that cluster-tv already uses. Each poll converts the upstream's raw data into a single integer penalty per the spec table, then writes it to State.

The Longhorn signal — "≥1 Degraded or Faulted volume" — is sourced from a Prometheus query the same way cluster-tv does it. The exact PromQL `count(longhorn_volume_robustness == 1) + count(longhorn_volume_robustness == 2)` (degraded + faulted) returns one scalar; if `> 0`, penalty = 1.

The restart-storm signal reuses cluster-tv's PromQL: `count(increase(kube_pod_container_status_restarts_total[24h]) > 5)` returns the count of pods restarting > 5 times in the last 24h. Penalty = `min(2, count)`.

The certs signal walks the cert-manager.io/v1 list and counts entries whose `status.notAfter` is set and within 14 days. Penalty = 1 if count > 0.

- [ ] **Step 1: Tests for each poll factory**

Append to `aggregator_test.go`:

```go
import (
	// add:
	"net/http"
	"net/http/httptest"

	"github.com/madic-creates/homelab-toys/internal/argocd"
	"github.com/madic-creates/homelab-toys/internal/certs"
	"github.com/madic-creates/homelab-toys/internal/prom"
)

func TestMakeArgoCDPoll_DegradedAppPenalty1(t *testing.T) {
	// Internal/argocd flattens metadata.name + status.{sync,health}.status
	// into the Application{Name, Sync, Health} struct. The HTTP fixture
	// must therefore use the upstream nested shape.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":[
			{"metadata":{"name":"ok"},"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}},
			{"metadata":{"name":"bad"},"status":{"sync":{"status":"Synced"},"health":{"status":"Degraded"}}}
		]}`))
	}))
	defer srv.Close()
	c := argocd.NewClient(srv.URL, "tok", srv.Client())

	st := NewState()
	now := time.Now()
	poll := MakeArgoCDPoll(c, st, func() time.Time { return now })
	if err := poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if got := st.Snapshot().ArgoCD.Data; got != 1 {
		t.Errorf("argocd penalty = %d, want 1", got)
	}
}

func TestMakeArgoCDPoll_AllSyncedPenalty0(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":[{"metadata":{"name":"ok"},"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}]}`))
	}))
	defer srv.Close()
	c := argocd.NewClient(srv.URL, "tok", srv.Client())
	st := NewState()
	poll := MakeArgoCDPoll(c, st, time.Now)
	if err := poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := st.Snapshot().ArgoCD.Data; got != 0 {
		t.Errorf("argocd penalty = %d, want 0", got)
	}
}

func TestMakeLonghornPoll_DegradedVolumePenalty1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[123,"2"]}]}}`))
	}))
	defer srv.Close()
	c := prom.NewClient(srv.URL, srv.Client())
	st := NewState()
	poll := MakeLonghornPoll(c, st, time.Now)
	if err := poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := st.Snapshot().Longhorn.Data; got != 1 {
		t.Errorf("longhorn penalty = %d, want 1", got)
	}
}

func TestMakeRestartsPoll_CapAt2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[123,"7"]}]}}`))
	}))
	defer srv.Close()
	c := prom.NewClient(srv.URL, srv.Client())
	st := NewState()
	poll := MakeRestartsPoll(c, st, time.Now)
	if err := poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := st.Snapshot().Restarts.Data; got != 2 {
		t.Errorf("restarts penalty = %d, want 2 (capped)", got)
	}
}

func TestMakeCertsPoll_ExpiringPenalty1(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	lister := &stubCertsLister{
		expiring: []certs.Cert{
			{Namespace: "ns", Name: "soon", NotAfter: now.Add(7 * 24 * time.Hour)},
		},
	}
	st := NewState()
	poll := MakeCertsPoll(lister, st, func() time.Time { return now })
	if err := poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := st.Snapshot().Certs.Data; got != 1 {
		t.Errorf("certs penalty = %d, want 1", got)
	}
}

func TestMakeCertsPoll_NothingExpiringPenalty0(t *testing.T) {
	st := NewState()
	poll := MakeCertsPoll(&stubCertsLister{}, st, time.Now)
	if err := poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := st.Snapshot().Certs.Data; got != 0 {
		t.Errorf("certs penalty = %d, want 0", got)
	}
}

// stubCertsLister returns the configured `expiring` slice without any
// filtering — this stub stands in for the already-filtering ExpiringSoon
// implementation. To test "filtering happens before the poll sees the
// list", that's a concern of internal/certs/lister_test.go, not here.
type stubCertsLister struct {
	expiring []certs.Cert
	err      error
}

func (s *stubCertsLister) ExpiringSoon(_ context.Context, _ time.Time, _ time.Duration) ([]certs.Cert, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.expiring, nil
}
```

- [ ] **Step 2: Add the poll factories**

Append to `aggregator.go`:

```go
import (
	// add:
	"strconv"

	"github.com/madic-creates/homelab-toys/internal/argocd"
	"github.com/madic-creates/homelab-toys/internal/prom"
)

// argoCDLister is the surface MakeArgoCDPoll needs. *argocd.Client
// satisfies it.
type argoCDLister interface {
	ListApplications(ctx context.Context) ([]argocd.Application, error)
}

// MakeArgoCDPoll lists ArgoCD applications and writes penalty=1 if any
// app has Health=Degraded or Sync=OutOfSync, else penalty=0.
//
// argocd.Application is a flat struct (Name, Sync, Health) — the
// internal/argocd package already trims the nested upstream shape, so
// callers don't traverse status.{sync,health}.status themselves.
func MakeArgoCDPoll(c argoCDLister, st *State, now func() time.Time) pollFunc {
	return func(ctx context.Context) error {
		apps, err := c.ListApplications(ctx)
		if err != nil {
			st.SetArgoCDError(err, now())
			return err
		}
		penalty := 0
		for _, a := range apps {
			if a.Health == "Degraded" || a.Sync == "OutOfSync" {
				penalty = 1
				break
			}
		}
		st.SetArgoCD(penalty, now())
		return nil
	}
}

// promQuerier is the surface the prom-backed polls need. *prom.Client
// satisfies it. Returns the raw []Sample so callers parse Value (a
// string per the Prometheus envelope) themselves.
type promQuerier interface {
	Query(ctx context.Context, q string) ([]prom.Sample, error)
}

// longhornQuery selects volumes that are not Healthy. The exporter's
// `longhorn_volume_robustness` enum is 0=Healthy, 1=Degraded, 2=Faulted,
// 3=Unknown — `> 0` excludes Healthy.
const longhornQuery = `count(longhorn_volume_robustness > 0)`

// MakeLonghornPoll runs the Longhorn PromQL and writes penalty=1 if any
// non-Healthy volume exists, else penalty=0. An empty result vector
// means zero matching volumes (Prometheus omits the row when the count
// would be 0), so penalty stays 0 in that case.
func MakeLonghornPoll(c promQuerier, st *State, now func() time.Time) pollFunc {
	return func(ctx context.Context) error {
		samples, err := c.Query(ctx, longhornQuery)
		if err != nil {
			st.SetLonghornError(err, now())
			return err
		}
		penalty := 0
		if v, ok := firstScalar(samples); ok && v > 0 {
			penalty = 1
		}
		st.SetLonghorn(penalty, now())
		return nil
	}
}

// restartsQuery matches cluster-tv's exact query: pods with > 5
// container restarts in the last 24h.
const restartsQuery = `count(increase(kube_pod_container_status_restarts_total[24h]) > 5)`

// MakeRestartsPoll runs the restart-storm PromQL and writes penalty
// equal to min(2, count). The cap is the spec's "+1 per pod, capped at +2".
func MakeRestartsPoll(c promQuerier, st *State, now func() time.Time) pollFunc {
	return func(ctx context.Context) error {
		samples, err := c.Query(ctx, restartsQuery)
		if err != nil {
			st.SetRestartsError(err, now())
			return err
		}
		penalty := 0
		if v, ok := firstScalar(samples); ok {
			penalty = int(v)
		}
		if penalty < 0 {
			penalty = 0
		}
		if penalty > 2 {
			penalty = 2
		}
		st.SetRestarts(penalty, now())
		return nil
	}
}

// firstScalar parses the first Sample's Value as a float64. Returns
// (0,false) for an empty vector — the caller decides what that means.
func firstScalar(samples []prom.Sample) (float64, bool) {
	if len(samples) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(samples[0].Value, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// certsLister is the surface MakeCertsPoll needs. *certs.Lister
// satisfies it via the existing ExpiringSoon(ctx, now, window) method,
// so no adapter is required in production.
type certsLister interface {
	ExpiringSoon(ctx context.Context, now time.Time, window time.Duration) ([]certs.Cert, error)
}

// certsExpiryWindow is the spec's 14-day threshold.
const certsExpiryWindow = 14 * 24 * time.Hour

// MakeCertsPoll lists certs expiring within certsExpiryWindow and writes
// penalty=1 if any are found, else penalty=0.
func MakeCertsPoll(l certsLister, st *State, now func() time.Time) pollFunc {
	return func(ctx context.Context) error {
		expiring, err := l.ExpiringSoon(ctx, now(), certsExpiryWindow)
		if err != nil {
			st.SetCertsError(err, now())
			return err
		}
		penalty := 0
		if len(expiring) > 0 {
			penalty = 1
		}
		st.SetCerts(penalty, now())
		return nil
	}
}
```

(Add the imports `"strconv"`, `"github.com/madic-creates/homelab-toys/internal/certs"` and `"github.com/madic-creates/homelab-toys/internal/prom"` to `aggregator.go` if not already present.)

- [ ] **Step 3: Verify production lister integration**

The `internal/certs` package's lister returns its own type. The tamagotchi `certsEntry` redeclaration is fine — it's a local view type. We need a small adapter in `main.go` (Task 14) that wraps `*certs.Lister` into the `certsLister` interface. The same applies to a wrapper around `*kube.Nodes` for `NodesLister`.

Run: `go test -race -v -run "TestMakeArgoCDPoll|TestMakeLonghornPoll|TestMakeRestartsPoll|TestMakeCertsPoll" ./cmd/tamagotchi/`
Expected: PASS — five subtests green.

- [ ] **Step 4: Lint**

Run: `golangci-lint run ./cmd/tamagotchi/...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/tamagotchi/aggregator.go cmd/tamagotchi/aggregator_test.go
git commit -m "feat(tamagotchi): add ArgoCD/Longhorn/certs/restarts polls"
```

---

## Phase 5 — HTTP handlers

The handlers expose five endpoints. The shape mirrors cluster-tv: a `Handlers` struct holds the State, templates, metrics, and a `now func() time.Time`. Tests use a stand-in template that doesn't need the real CSS.

### Task 10: `/api/state` JSON

**Files:**
- Create: `cmd/tamagotchi/handlers.go`
- Test: `cmd/tamagotchi/handlers_test.go`

The JSON shape from the spec (line 67):

```json
{
  "mood": "happy",
  "mood_level": 1,
  "age_days": 42,
  "born_at": "2026-01-01T00:00:00Z",
  "factors": [{"source": "argocd", "severity": "...", "reason": "..."}],
  "stale_sources": [],
  "confused": false
}
```

We'll add `"hello": true` while `HasFirstTick == false` so the page can show the "Hallo!" bubble — this keeps the bubble decision in the algorithm rather than scattered across handlers/templates.

- [ ] **Step 1: Test for `/api/state`**

```go
// cmd/tamagotchi/handlers_test.go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type apiStateResponse struct {
	Mood         string   `json:"mood"`
	MoodLevel    int      `json:"mood_level"`
	AgeDays      int      `json:"age_days"`
	BornAt       string   `json:"born_at"`
	StaleSources []string `json:"stale_sources"`
	Confused     bool     `json:"confused"`
	Hello        bool     `json:"hello"`
}

func TestAPIState_HappyPath(t *testing.T) {
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	st.SetBirthday(now.Add(-3 * 24 * time.Hour))
	st.SetArgoCD(0, now)
	st.SetLonghorn(0, now)
	st.SetCerts(0, now)
	st.SetRestarts(0, now)
	st.SetNodes(0, now)
	st.UpdateMood(now)

	h := NewHandlers(st, nil, func() time.Time { return now })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	h.APIState(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got apiStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Mood != "ecstatic" || got.MoodLevel != 0 {
		t.Errorf("mood = %q level=%d, want ecstatic 0", got.Mood, got.MoodLevel)
	}
	if got.AgeDays != 3 {
		t.Errorf("age = %d, want 3", got.AgeDays)
	}
	if got.Hello {
		t.Errorf("Hello should be false after first tick")
	}
}

func TestAPIState_HelloWhileNotYetTicked(t *testing.T) {
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	st.UpdateMood(now) // no sources Loaded → init grace
	h := NewHandlers(st, nil, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.APIState(rec, httptest.NewRequest(http.MethodGet, "/api/state", nil))

	var got apiStateResponse
	json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.Hello {
		t.Errorf("Hello = false, want true during init grace")
	}
	if got.Mood != "happy" {
		t.Errorf("init mood = %q, want happy", got.Mood)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test -race -v -run TestAPIState ./cmd/tamagotchi/`
Expected: FAIL — `undefined: NewHandlers, *Handlers.APIState`.

- [ ] **Step 3: Implement Handlers + APIState**

```go
// cmd/tamagotchi/handlers.go
package main

import (
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handlers wires HTTP endpoints to the shared State + templates.
type Handlers struct {
	state        *State
	tpl          *template.Template
	now          func() time.Time
	processStart time.Time

	cssPayload template.CSS

	pollTotal       *prometheus.CounterVec
	lastSuccessSecs *prometheus.GaugeVec
	moodLevel       prometheus.Gauge
	renderDuration  prometheus.Histogram
}

// NewHandlers constructs the handlers. tpl is allowed to be nil so unit
// tests can target APIState/healthz without parsing the real templates.
func NewHandlers(s *State, tpl *template.Template, now func() time.Time) *Handlers {
	return &Handlers{
		state:        s,
		tpl:          tpl,
		now:          now,
		processStart: now(),
		pollTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "tamagotchi_source_poll_total"},
			[]string{"source", "result"},
		),
		lastSuccessSecs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "tamagotchi_source_last_success_seconds"},
			[]string{"source"},
		),
		moodLevel: prometheus.NewGauge(
			prometheus.GaugeOpts{Name: "tamagotchi_mood_level"},
		),
		renderDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{Name: "tamagotchi_render_duration_seconds"},
		),
	}
}

// SetCSS installs the per-page CSS string. Wired up from main.go.
func (h *Handlers) SetCSS(css string) { h.cssPayload = template.CSS(css) }

// PollTotal increments per-source poll counters. Implements metricsRecorder.
func (h *Handlers) PollTotal(source, result string) {
	h.pollTotal.WithLabelValues(source, result).Inc()
}

// LastSuccessSeconds records the unix-second timestamp of the last
// successful poll. Implements metricsRecorder.
func (h *Handlers) LastSuccessSeconds(source string, seconds float64) {
	h.lastSuccessSecs.WithLabelValues(source).Set(seconds)
}

// APIState serves a JSON snapshot — see the spec's response shape.
func (h *Handlers) APIState(w http.ResponseWriter, _ *http.Request) {
	snap := h.state.Snapshot()
	resp := struct {
		Mood         string   `json:"mood"`
		MoodLevel    int      `json:"mood_level"`
		AgeDays      int      `json:"age_days"`
		BornAt       string   `json:"born_at"`
		StaleSources []string `json:"stale_sources"`
		Confused     bool     `json:"confused"`
		Hello        bool     `json:"hello"`
	}{
		Mood:         snap.Mood.Name(),
		MoodLevel:    snap.Mood.Level,
		AgeDays:      ageInDays(snap.Birthday, h.now()),
		BornAt:       formatBirthday(snap.Birthday),
		StaleSources: snap.StaleSources,
		Confused:     snap.Confused,
		Hello:        !snap.HasFirstTick,
	}
	if resp.StaleSources == nil {
		resp.StaleSources = []string{} // ensure JSON `[]`, not `null`
	}

	// Update the mood-level gauge while we're holding a fresh snapshot.
	h.moodLevel.Set(float64(snap.Mood.Level))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Warn("encode /api/state", "error", err)
	}
}

func ageInDays(birthday, now time.Time) int {
	if birthday.IsZero() {
		return 0
	}
	return int(now.Sub(birthday) / (24 * time.Hour))
}

func formatBirthday(birthday time.Time) string {
	if birthday.IsZero() {
		return ""
	}
	return birthday.UTC().Format(time.RFC3339)
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -race -v -run TestAPIState ./cmd/tamagotchi/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tamagotchi/handlers.go cmd/tamagotchi/handlers_test.go
git commit -m "feat(tamagotchi): add /api/state JSON handler"
```

---

### Task 11: `/` and `/widget` HTML handlers + sprite injection

**Files:**
- Modify: `cmd/tamagotchi/handlers.go`
- Modify: `cmd/tamagotchi/handlers_test.go`

Both pages render via `html/template` and include the inline SVG from `RenderSprite`. The data passed to the template:

```go
type pageData struct {
    Mood     string
    Level    int
    AgeDays  int
    Sprite   template.HTML // from RenderSprite — already safe HTML
    Hello    bool
    Confused bool
    Stale    []string
    CSS      template.CSS
}
```

Standalone (`/`) shows mood + age + a small "factors" line + the Hallo bubble during init grace.
Widget (`/widget`) shows just the sprite + mood text.

For tests we use a stand-in template inside the test file rather than parsing the real templates — the goal is to verify the handler wires `RenderSprite` and the snapshot correctly.

- [ ] **Step 1: Tests**

Append to `handlers_test.go`:

```go
func TestIndex_RendersSpriteAndMoodClass(t *testing.T) {
	tpl := template.Must(template.New("index").Parse(`<body class="mood-{{.Mood}}">{{.Sprite}}</body>`))
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	st.SetArgoCD(0, now)
	st.SetLonghorn(0, now)
	st.SetCerts(0, now)
	st.SetRestarts(0, now)
	st.SetNodes(0, now)
	st.UpdateMood(now)

	h := NewHandlers(st, tpl, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.Index(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `class="mood-ecstatic"`) {
		t.Errorf("body missing mood class: %s", body)
	}
	if !strings.Contains(body, `<svg class="sprite mood-ecstatic"`) {
		t.Errorf("body missing sprite: %s", body)
	}
}

func TestWidget_RendersSpriteAndMoodText(t *testing.T) {
	tpl := template.Must(template.New("widget").Parse(`<body>{{.Sprite}}<span>{{.Mood}}</span></body>`))
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	st.SetArgoCD(0, now)
	st.SetLonghorn(0, now)
	st.SetCerts(0, now)
	st.SetRestarts(0, now)
	st.SetNodes(3, now) // node down → dying
	st.UpdateMood(now)

	h := NewHandlers(st, tpl, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.Widget(rec, httptest.NewRequest(http.MethodGet, "/widget", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `<svg class="sprite mood-dying"`) {
		t.Errorf("widget missing sprite: %s", body)
	}
	if !strings.Contains(body, ">dying</span>") {
		t.Errorf("widget missing mood text: %s", body)
	}
}

// remember to add `import "strings"` and `"html/template"` at the top of handlers_test.go
```

- [ ] **Step 2: Implement Index + Widget**

Append to `handlers.go`:

```go
type pageData struct {
	Mood     string
	Level    int
	AgeDays  int
	Sprite   template.HTML
	Hello    bool
	Confused bool
	Stale    []string
	CSS      template.CSS
}

// Index serves the fullscreen page. The template name is "index".
func (h *Handlers) Index(w http.ResponseWriter, _ *http.Request) {
	h.renderPage(w, "index")
}

// Widget serves the compact widget page. The template name is "widget".
func (h *Handlers) Widget(w http.ResponseWriter, _ *http.Request) {
	h.renderPage(w, "widget")
}

func (h *Handlers) renderPage(w http.ResponseWriter, name string) {
	start := h.now()
	defer func() {
		h.renderDuration.Observe(time.Since(start).Seconds())
	}()
	snap := h.state.Snapshot()
	data := pageData{
		Mood:     snap.Mood.Name(),
		Level:    snap.Mood.Level,
		AgeDays:  ageInDays(snap.Birthday, h.now()),
		Sprite:   template.HTML(RenderSprite(snap.Mood.Name(), snap.Confused)),
		Hello:    !snap.HasFirstTick,
		Confused: snap.Confused,
		Stale:    snap.StaleSources,
		CSS:      h.cssPayload,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Warn("render template", "name", name, "error", err)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test -race -v -run "TestIndex|TestWidget" ./cmd/tamagotchi/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/tamagotchi/handlers.go cmd/tamagotchi/handlers_test.go
git commit -m "feat(tamagotchi): add Index + Widget handlers with sprite injection"
```

---

### Task 12: `/healthz` — heartbeat-based liveness

**Files:**
- Modify: `cmd/tamagotchi/handlers.go`
- Modify: `cmd/tamagotchi/handlers_test.go`

Per the spec (line 68), `/healthz` returns 200 if every source goroutine has updated its heartbeat within the last 90 seconds, else 503. **Note:** this differs from cluster-tv, where `/healthz` is liveness-only (always 200 if the HTTP server can respond). Tamagotchi's spec explicitly wants the freshness check — keep it as the spec states.

The check uses `LastSuccess` per source. During init grace (no source loaded yet), `/healthz` should return 200 — otherwise the readiness probe would fail forever before the first poll.

- [ ] **Step 1: Tests**

Append to `handlers_test.go`:

```go
func TestHealthz_AllFreshOK(t *testing.T) {
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	for _, fn := range []func(int, time.Time){
		st.SetArgoCD, st.SetLonghorn, st.SetCerts, st.SetRestarts, st.SetNodes,
	} {
		fn(0, now)
	}
	h := NewHandlers(st, nil, func() time.Time { return now.Add(30 * time.Second) })
	rec := httptest.NewRecorder()
	h.Healthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHealthz_StaleSourceFails(t *testing.T) {
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	st.SetArgoCD(0, now)
	st.SetLonghorn(0, now)
	st.SetCerts(0, now)
	st.SetRestarts(0, now)
	st.SetNodes(0, now.Add(-2*time.Minute)) // > 90s old
	h := NewHandlers(st, nil, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.Healthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestHealthz_InitGraceOK(t *testing.T) {
	// No source loaded yet — /healthz should still be 200 so readiness
	// doesn't fail before the first poll arrives.
	st := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	h := NewHandlers(st, nil, func() time.Time { return now })
	rec := httptest.NewRecorder()
	h.Healthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("init grace status = %d, want 200", rec.Code)
	}
}
```

- [ ] **Step 2: Implement**

Append to `handlers.go`:

```go
const healthzWindow = 90 * time.Second

// Healthz returns 200 if every source's last successful poll is within
// healthzWindow, else 503. During init grace (no source loaded yet)
// returns 200 to avoid blocking readiness before the first poll.
func (h *Handlers) Healthz(w http.ResponseWriter, _ *http.Request) {
	snap := h.state.Snapshot()
	now := h.now()

	slots := []Slot[int]{snap.ArgoCD, snap.Longhorn, snap.Certs, snap.Restarts, snap.Nodes}
	anyLoaded := false
	for _, s := range slots {
		if s.Loaded {
			anyLoaded = true
			break
		}
	}
	if !anyLoaded {
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, s := range slots {
		if !s.Loaded || now.Sub(s.LastSuccess) > healthzWindow {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 3: Run tests**

Run: `go test -race -v -run TestHealthz ./cmd/tamagotchi/`
Expected: PASS — three subtests green.

- [ ] **Step 4: Commit**

```bash
git add cmd/tamagotchi/handlers.go cmd/tamagotchi/handlers_test.go
git commit -m "feat(tamagotchi): add /healthz with 90s heartbeat window"
```

---

### Task 13: `/metrics` Prometheus endpoint

**Files:**
- Modify: `cmd/tamagotchi/handlers.go`

`/metrics` is just a wired-up `promhttp.HandlerFor(...)` over a custom `prometheus.Registry` that includes the four collectors built in `NewHandlers`. Using a custom registry rather than `prometheus.DefaultRegisterer` keeps tests independent (no global state).

- [ ] **Step 1: Implement**

Append to `handlers.go`:

```go
// Metrics returns a Prometheus-handler that serves the four tamagotchi
// collectors. Caller must invoke this once and mount the returned
// http.Handler on /metrics.
func (h *Handlers) Metrics() http.Handler {
	r := prometheus.NewRegistry()
	r.MustRegister(h.pollTotal, h.lastSuccessSecs, h.moodLevel, h.renderDuration)
	return promhttp.HandlerFor(r, promhttp.HandlerOpts{})
}
```

- [ ] **Step 2: Smoke test**

Append to `handlers_test.go`:

```go
func TestMetrics_ServesPrometheusFormat(t *testing.T) {
	st := NewState()
	h := NewHandlers(st, nil, time.Now)
	h.PollTotal("argocd", "success") // produce one sample
	rec := httptest.NewRecorder()
	h.Metrics().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `tamagotchi_source_poll_total{result="success",source="argocd"} 1`) {
		t.Errorf("metric body missing counter line: %s", body)
	}
}
```

- [ ] **Step 3: Run**

Run: `go test -race -v -run TestMetrics ./cmd/tamagotchi/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/tamagotchi/handlers.go cmd/tamagotchi/handlers_test.go
git commit -m "feat(tamagotchi): add /metrics handler"
```

---

## Phase 6 — Web assets, main, Docker

### Task 14: Web templates + CSS + embed package

**Files:**
- Create: `web/tamagotchi/embed.go`
- Create: `web/tamagotchi/index.html.tmpl`
- Create: `web/tamagotchi/widget.html.tmpl`
- Create: `web/tamagotchi/style.css`

Per the layout rule, `web/tamagotchi/` is its own Go package because `go:embed` patterns can't include `..`. The embed file is tiny.

- [ ] **Step 1: embed.go**

```go
// web/tamagotchi/embed.go
package tamagotchiweb

import "embed"

//go:embed index.html.tmpl widget.html.tmpl style.css
var FS embed.FS
```

- [ ] **Step 2: index.html.tmpl**

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Pod-Tamagotchi</title>
<style>{{.CSS}}</style>
</head>
<body class="mood-{{.Mood}}{{if .Confused}} confused{{end}}">
<main class="stage">
  <div class="sprite-wrap">{{.Sprite}}</div>
  <p class="mood-text">{{.Mood}}</p>
  {{if .Hello}}<div class="hallo">Hallo!</div>{{end}}
  <p class="age">Age: {{.AgeDays}} day(s)</p>
  {{if .Stale}}<p class="stale">Stale: {{range $i, $s := .Stale}}{{if $i}}, {{end}}{{$s}}{{end}}</p>{{end}}
</main>
<script>
  async function tick() {
    try {
      const r = await fetch('/api/state', {cache: 'no-store'});
      if (!r.ok) return;
      const j = await r.json();
      const cls = 'mood-' + j.mood + (j.confused ? ' confused' : '');
      if (document.body.className !== cls) document.body.className = cls;
      const text = document.querySelector('.mood-text');
      if (text) text.textContent = j.mood;
      const age = document.querySelector('.age');
      if (age) age.textContent = 'Age: ' + j.age_days + ' day(s)';
    } catch (e) { /* swallow — next tick */ }
  }
  setInterval(tick, 30000);
</script>
</body>
</html>
```

- [ ] **Step 3: widget.html.tmpl**

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Tamagotchi widget</title>
<style>{{.CSS}}</style>
</head>
<body class="widget mood-{{.Mood}}{{if .Confused}} confused{{end}}">
<div class="sprite-wrap">{{.Sprite}}</div>
<span class="mood-text">{{.Mood}}</span>
<script>
  async function tick() {
    try {
      const r = await fetch('/api/state', {cache: 'no-store'});
      if (!r.ok) return;
      const j = await r.json();
      const cls = 'widget mood-' + j.mood + (j.confused ? ' confused' : '');
      if (document.body.className !== cls) document.body.className = cls;
      const text = document.querySelector('.mood-text');
      if (text) text.textContent = j.mood;
    } catch (e) {}
  }
  setInterval(tick, 30000);
</script>
</body>
</html>
```

- [ ] **Step 4: style.css**

```css
:root {
  --bg: #1a1a1a;
  --fg: #f5f5f5;
  --accent: #ffd84a;
}
* { box-sizing: border-box; margin: 0; padding: 0; }
html, body { height: 100%; background: var(--bg); color: var(--fg); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

body { display: flex; align-items: center; justify-content: center; }
.stage { text-align: center; }

.sprite-wrap {
  display: inline-block;
  image-rendering: pixelated;
  image-rendering: crisp-edges;
}
.sprite-wrap svg { display: block; transform: scale(8); transform-origin: top left; }
body.widget .sprite-wrap svg { transform: scale(2); }

.mood-text { font-size: 1.5rem; margin-top: 1rem; text-transform: uppercase; letter-spacing: 0.2em; }
body.widget .mood-text { font-size: 0.9rem; margin: 0.2rem; }

.age { margin-top: 0.5rem; opacity: 0.7; }
.stale { margin-top: 0.4rem; color: #c79f3c; font-size: 0.85rem; }

.hallo {
  position: relative; display: inline-block; margin-bottom: 1rem;
  background: #fff; color: #1a1a1a; padding: 0.4rem 0.8rem; border-radius: 1rem;
  font-weight: bold;
}
.hallo::after {
  content: ''; position: absolute; left: 50%; bottom: -0.5rem; transform: translateX(-50%);
  border: 0.5rem solid transparent; border-top-color: #fff;
}

/* Per-mood idle animations — applied to the sprite-wrap so the
   <svg> element keeps its scale transform separate from animation. */
@keyframes bounce-big   { 0%,100%{transform:translateY(0)} 50%{transform:translateY(-6px)} }
@keyframes bounce-small { 0%,100%{transform:translateY(0)} 50%{transform:translateY(-3px)} }
@keyframes wobble       { 0%,100%{transform:translateX(0)} 50%{transform:translateX(2px)} }
@keyframes flicker      { 0%,49%{opacity:1} 50%,52%{opacity:0.6} 53%,100%{opacity:1} }

body.mood-ecstatic .sprite-wrap { animation: bounce-big   1s ease-in-out infinite; }
body.mood-happy    .sprite-wrap { animation: bounce-small 1.5s ease-in-out infinite; }
body.mood-meh      .sprite-wrap { animation: none; }
body.mood-sick     .sprite-wrap { animation: wobble 2s ease-in-out infinite; }
body.mood-dying    .sprite-wrap { animation: flicker 4s ease-in-out infinite; }
```

- [ ] **Step 5: Compile-time check**

Run: `go build ./web/tamagotchi/...`
Expected: success.

- [ ] **Step 6: Commit**

```bash
git add web/tamagotchi/
git commit -m "feat(tamagotchi): add HTML templates + CSS + embed FS"
```

---

### Task 15: `cmd/tamagotchi/main.go` wire-up

**Files:**
- Create: `cmd/tamagotchi/main.go`

The shape mirrors `cmd/cluster-tv/main.go`: env reads, in-cluster client config, lister adapters, handler mux, signal handling.

Required env vars:
- `ARGOCD_URL` (e.g. `http://argocd-server.argocd.svc.cluster.local`)
- `ARGOCD_TOKEN` (mounted from secret)
- `PROMETHEUS_URL` (e.g. `http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090`)
- `POD_NAME` and `POD_NAMESPACE` (downward API; for the self-pod birthday read)
- `PORT` (default `8080`)

- [ ] **Step 1: Implement**

```go
// cmd/tamagotchi/main.go
package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
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
	tamagotchiweb "github.com/madic-creates/homelab-toys/web/tamagotchi"
)

const (
	pollInterval        = 20 * time.Second
	httpReadTimeout     = 10 * time.Second
	httpWriteTimeout    = 30 * time.Second
	httpIdleTimeout     = 90 * time.Second
	shutdownGracePeriod = 10 * time.Second
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	argocdURL := mustEnv("ARGOCD_URL")
	argocdToken := mustEnv("ARGOCD_TOKEN")
	promURL := mustEnv("PROMETHEUS_URL")
	podName := os.Getenv("POD_NAME")
	podNS := os.Getenv("POD_NAMESPACE")
	port := envOr("PORT", "8080")

	cs, dyn, err := kube.NewInCluster()
	if err != nil {
		return fmt.Errorf("kube clients: %w", err)
	}

	// Birthday: best-effort. If POD_NAME/POD_NAMESPACE are unset (e.g.
	// running locally with kubectl proxy) or the API call fails, log and
	// proceed with a zero birthday — the page will show age 0.
	st := NewState()
	if podName != "" && podNS != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		bday, err := kube.SelfPodCreatedAt(ctx, cs, podNS, podName)
		cancel()
		if err != nil {
			slog.Warn("self-pod birthday read", "error", err)
		} else {
			st.SetBirthday(bday)
		}
	}

	// Build clients. *certs.Lister already satisfies the certsLister
	// interface (ExpiringSoon method). *kubeNodesLister wraps cs to
	// satisfy NodesLister.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	argoC := argocd.NewClient(argocdURL, argocdToken, httpClient)
	promC := prom.NewClient(promURL, httpClient)
	certsL := certs.NewLister(dyn)
	nodesL := &kubeNodesLister{cs: cs}

	// Templates.
	tpl, err := template.New("").ParseFS(tamagotchiweb.FS, "*.html.tmpl")
	if err != nil {
		return fmt.Errorf("parse templates: %w", err)
	}
	cssBytes, err := io.ReadAll(must(tamagotchiweb.FS.Open("style.css")))
	if err != nil {
		return fmt.Errorf("read css: %w", err)
	}

	now := time.Now
	h := NewHandlers(st, tpl, now)
	h.SetCSS(string(cssBytes))

	// Aggregator.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go runSource(ctx, "argocd", MakeArgoCDPoll(argoC, st, now), pollInterval, h)
	go runSource(ctx, "longhorn", MakeLonghornPoll(promC, st, now), pollInterval, h)
	go runSource(ctx, "certs", MakeCertsPoll(certsL, st, now), pollInterval, h)
	go runSource(ctx, "restarts", MakeRestartsPoll(promC, st, now), pollInterval, h)
	go runSource(ctx, "nodes", MakeNodesPoll(nodesL, st, now), pollInterval, h)

	// Mood ticker — independent of source goroutines, runs Compute every
	// 5s so the page reflects state changes promptly.
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				st.UpdateMood(now())
			}
		}
	}()

	// HTTP server.
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.Index)
	mux.HandleFunc("/widget", h.Widget)
	mux.HandleFunc("/api/state", h.APIState)
	mux.HandleFunc("/healthz", h.Healthz)
	mux.Handle("/metrics", h.Metrics())

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  httpReadTimeout,
		WriteTimeout: httpWriteTimeout,
		IdleTimeout:  httpIdleTimeout,
	}

	listenErr := make(chan error, 1)
	go func() {
		slog.Info("listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-listenErr:
		slog.Error("listener", "error", err)
	}

	slog.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownGracePeriod)
	defer shutdownCancel()
	return srv.Shutdown(shutdownCtx)
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		panic("missing env: " + k)
	}
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// kubeNodesLister adapts a kubernetes.Interface clientset into the
// NodesLister surface that MakeNodesPoll expects. The internal/kube
// package's Nodes(ctx, cs) function does the actual list + Ready
// extraction; this wrapper just pins the clientset.
type kubeNodesLister struct{ cs kubernetes.Interface }

func (n *kubeNodesLister) Nodes(ctx context.Context) ([]kube.NodeStatus, error) {
	return kube.Nodes(ctx, n.cs)
}
```

(Add `"k8s.io/client-go/kubernetes"` to the import block. *certs.Lister* needs no adapter — its ExpiringSoon signature already matches the certsLister interface defined in aggregator.go.)

- [ ] **Step 2: Compile**

Run: `go build ./cmd/tamagotchi/`
Expected: success.

- [ ] **Step 3: Vet + lint**

Run: `go vet ./... && golangci-lint run`
Expected: clean across the whole module.

- [ ] **Step 4: Run all tests**

Run: `go test -race ./...`
Expected: all green — cluster-tv tests untouched, tamagotchi tests pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/tamagotchi/main.go
git commit -m "feat(tamagotchi): wire main.go (clients, aggregator, server)"
```

---

### Task 16: `Dockerfile.tamagotchi`

**Files:**
- Create: `Dockerfile.tamagotchi`

Identical to `Dockerfile.cluster-tv` with binary name swapped.

- [ ] **Step 1: Write**

```dockerfile
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache ca-certificates
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/ web/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /tamagotchi ./cmd/tamagotchi/

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /tamagotchi /tamagotchi
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/tamagotchi"]
```

- [ ] **Step 2: Build locally**

Run: `docker build -f Dockerfile.tamagotchi -t tamagotchi:dev .`
Expected: image built successfully. Verify size with `docker images tamagotchi:dev` — should be ≤ 30 MiB.

- [ ] **Step 3: Smoke-run**

```bash
docker run --rm \
  -e ARGOCD_URL=http://localhost \
  -e ARGOCD_TOKEN=fake \
  -e PROMETHEUS_URL=http://localhost \
  -p 8080:8080 \
  tamagotchi:dev &
sleep 2
curl -s localhost:8080/healthz   # → 200 (init grace)
curl -s localhost:8080/api/state # → JSON with hello:true
kill %1
```

(Polls will fail because the URLs are bogus — that's fine; init-grace `/healthz` returns 200 anyway.)

- [ ] **Step 4: Commit**

```bash
git add Dockerfile.tamagotchi
git commit -m "feat(tamagotchi): add Dockerfile (multi-stage scratch)"
```

---

## Phase 7 — CI + repo docs

### Task 17: CI matrix entry for tamagotchi build

**Files:**
- Modify: `.github/workflows/release.yaml`

Append a parallel `build-tamagotchi` job mirroring `build-cluster-tv`.

- [ ] **Step 1: Inspect existing job**

Run: `grep -A 60 "build-cluster-tv:" .github/workflows/release.yaml`

Take note of the structure: `needs: [test, lint]`, login to GHCR, `docker/build-push-action` with the right Dockerfile path and image tags.

- [ ] **Step 2: Append the parallel job**

After the `build-cluster-tv` job in `.github/workflows/release.yaml`, add a `build-tamagotchi` job that:
- has the same `needs: [test, lint]` gate
- uses `Dockerfile.tamagotchi`
- pushes to `ghcr.io/${{ github.repository_owner }}/tamagotchi:{latest,<short-sha>}`

Concretely (paste after the existing `build-cluster-tv` block, indentation flush with `build-cluster-tv`):

```yaml
  build-tamagotchi:
    needs: [test, lint]
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v6
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/setup-buildx-action@v3
      - id: short-sha
        run: echo "sha=$(echo ${{ github.sha }} | cut -c1-7)" >> "$GITHUB_OUTPUT"
      - uses: docker/build-push-action@v6
        with:
          context: .
          file: Dockerfile.tamagotchi
          push: true
          tags: |
            ${{ env.REGISTRY }}/tamagotchi:latest
            ${{ env.REGISTRY }}/tamagotchi:${{ steps.short-sha.outputs.sha }}
```

(If the cluster-tv job uses different tag formatting, copy that exact format and just swap `cluster-tv` → `tamagotchi`.)

- [ ] **Step 3: Validate workflow YAML**

Run: `yq eval '.jobs | keys' .github/workflows/release.yaml`
Expected output includes `build-tamagotchi`.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/release.yaml
git commit -m "ci: add parallel build-tamagotchi matrix job"
```

(Don't push yet — manifests + README + deployment doc still pending. Push at the end.)

---

### Task 18: README.md update

**Files:**
- Modify: `README.md`

Add a tamagotchi section mirroring the cluster-tv section structure. Include the deploy reference link.

- [ ] **Step 1: Inspect cluster-tv section**

Run: `grep -B1 -A6 "## cluster-tv" README.md` (or whichever heading style is in use).

- [ ] **Step 2: Add tamagotchi section**

In `README.md`, below the cluster-tv binary description, add (matching the same heading depth and style as cluster-tv's section):

```markdown
### tamagotchi

Pixel-pet wall display that reframes the same cluster signals as cluster-tv
into a 5-stage mood (`ecstatic` / `happy` / `meh` / `sick` / `dying`) with
hysteresis (immediate worsening, 5-minute window for improvement). Includes
a compact `/widget` variant for embedding into homepage dashboards. Image:
`ghcr.io/madic-creates/tamagotchi`.
Deployment reference: [`docs/tamagotchi-deployment.md`](docs/tamagotchi-deployment.md).
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: add tamagotchi section to README"
```

---

## Phase 8 — Reference manifests + deployment doc

### Task 19: `deploy/tamagotchi/{namespace,rbac,secret,deployment,service}.yaml`

**Files:**
- Create: `deploy/tamagotchi/namespace.yaml`
- Create: `deploy/tamagotchi/rbac.yaml`
- Create: `deploy/tamagotchi/secret.yaml`
- Create: `deploy/tamagotchi/deployment.yaml`
- Create: `deploy/tamagotchi/service.yaml`

These mirror `deploy/cluster-tv/` exactly with two delta points: the RBAC adds `list` on `nodes` plus a namespaced Role for `get` on the self-pod; the Deployment adds the `POD_NAME` / `POD_NAMESPACE` downward-API env vars.

- [ ] **Step 1: namespace.yaml**

```yaml
# deploy/tamagotchi/namespace.yaml
# Optional — drop this file if you're reusing an existing namespace and
# update the metadata.namespace fields in the other manifests accordingly.
apiVersion: v1
kind: Namespace
metadata:
  name: tamagotchi
  labels:
    app.kubernetes.io/name: tamagotchi
```

- [ ] **Step 2: rbac.yaml**

```yaml
# deploy/tamagotchi/rbac.yaml
# ServiceAccount + cluster-wide list on Nodes and cert-manager Certificates,
# plus a namespaced get on Pods (for the self-pod birthday read at startup).
apiVersion: v1
kind: ServiceAccount
metadata:
  name: tamagotchi
  namespace: tamagotchi
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: tamagotchi-reader
rules:
  - apiGroups: [""]
    resources: ["nodes"]
    verbs: ["list"]
  - apiGroups: ["cert-manager.io"]
    resources: ["certificates"]
    verbs: ["list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: tamagotchi-reader
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: tamagotchi-reader
subjects:
  - kind: ServiceAccount
    name: tamagotchi
    namespace: tamagotchi
---
# Namespace-scoped: get on the binary's own pod. resourceNames is impractical
# (pod name is dynamic), so we scope to the namespace instead.
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: tamagotchi-self-pod
  namespace: tamagotchi
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: tamagotchi-self-pod
  namespace: tamagotchi
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: tamagotchi-self-pod
subjects:
  - kind: ServiceAccount
    name: tamagotchi
    namespace: tamagotchi
```

- [ ] **Step 3: secret.yaml**

```yaml
# deploy/tamagotchi/secret.yaml
# REPLACE_WITH_ARGOCD_LOCAL_USER_TOKEN before applying. Use a real
# argocd account token (see docs/tamagotchi-deployment.md).
apiVersion: v1
kind: Secret
metadata:
  name: tamagotchi-env
  namespace: tamagotchi
type: Opaque
stringData:
  ARGOCD_TOKEN: REPLACE_WITH_ARGOCD_LOCAL_USER_TOKEN
```

- [ ] **Step 4: deployment.yaml**

Copy `deploy/cluster-tv/deployment.yaml` and:
- swap `cluster-tv` → `tamagotchi` everywhere (name, labels, image, secret name)
- swap `runAsUser/runAsGroup: 65532` → `65534` to match the Dockerfile's `USER 65534:65534` (cluster-tv uses 65532; verify that line in cluster-tv before copying — if cluster-tv's Dockerfile uses 65532, we should align Dockerfile.tamagotchi to 65532 too. **Pick one and align**: prefer keeping `nonroot:nonroot` (65532) for K8s convention and update Task 16's Dockerfile to `USER 65532:65532`.)
- add downward-API env vars:

```yaml
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: POD_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
```

Full file:

```yaml
# deploy/tamagotchi/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tamagotchi
  namespace: tamagotchi
  labels:
    app.kubernetes.io/name: tamagotchi
spec:
  replicas: 1
  revisionHistoryLimit: 3
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app.kubernetes.io/name: tamagotchi
  template:
    metadata:
      labels:
        app.kubernetes.io/name: tamagotchi
    spec:
      serviceAccountName: tamagotchi
      automountServiceAccountToken: true
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: tamagotchi
          # Pin to a specific digest in production; `latest` shown for clarity.
          image: ghcr.io/madic-creates/tamagotchi:latest
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8080
              name: http
          env:
            - name: PORT
              value: "8080"
            - name: PROMETHEUS_URL
              value: "http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090"
            - name: ARGOCD_URL
              value: "http://argocd-server.argocd.svc.cluster.local"
            - name: ARGOCD_TOKEN
              valueFrom:
                secretKeyRef:
                  name: tamagotchi-env
                  key: ARGOCD_TOKEN
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
            - name: POD_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
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
            initialDelaySeconds: 5
            periodSeconds: 30
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 2
            periodSeconds: 10
```

**Plan-correctness note:** the spec's resource requests (line 173) say "requests/limits ~50m / 64Mi". Cluster-tv uses 10m / 32Mi (request) and 100m / 64Mi (limit). The plan keeps cluster-tv's numbers because they have proven sufficient on the same workload shape; tamagotchi's CPU profile is essentially identical (5 polls × 20s + html/template renders).

- [ ] **Step 5: service.yaml**

```yaml
# deploy/tamagotchi/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: tamagotchi
  namespace: tamagotchi
  labels:
    app.kubernetes.io/name: tamagotchi
spec:
  type: ClusterIP
  selector:
    app.kubernetes.io/name: tamagotchi
  ports:
    - name: http
      port: 8080
      targetPort: http
```

- [ ] **Step 6: Validate with kubeconform**

Run: `kubeconform -strict -summary deploy/tamagotchi/*.yaml`
Expected: 0 errors. (If the project doesn't have kubeconform, skip — `kubectl apply --dry-run=client -f deploy/tamagotchi/` is an alternative.)

- [ ] **Step 7: Commit**

```bash
git add deploy/tamagotchi/{namespace,rbac,secret,deployment,service}.yaml
git commit -m "feat(tamagotchi): add reference k8s manifests (namespace, rbac, secret, deployment, service)"
```

---

### Task 20: `deploy/tamagotchi/{networkpolicy,ingress}.yaml`

**Files:**
- Create: `deploy/tamagotchi/networkpolicy.yaml`
- Create: `deploy/tamagotchi/ingress.yaml`

Identical egress shape to cluster-tv (kube-API, DNS, Prometheus, ArgoCD); ingress allows the cluster's ingress controller.

- [ ] **Step 1: networkpolicy.yaml**

```yaml
# deploy/tamagotchi/networkpolicy.yaml
# Standard Kubernetes NetworkPolicy for tamagotchi.
# Adjust the namespace selectors and labels to match your cluster.
#
# This policy only governs traffic FROM and TO tamagotchi pods. The
# destination apps (argocd-server, prometheus, kube-apiserver) may have
# their own ingress policies that need separate allow rules — see
# docs/tamagotchi-deployment.md.
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: tamagotchi
  namespace: tamagotchi
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: tamagotchi
  policyTypes:
    - Ingress
    - Egress
  ingress:
    # Allow your ingress controller to reach the pod. Adjust the namespace
    # label and pod label to match your setup (e.g. ingress-nginx, traefik).
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
      ports:
        - port: 8080
          protocol: TCP
  egress:
    # DNS
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: kube-system
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - port: 53
          protocol: UDP
        - port: 53
          protocol: TCP
    # Prometheus (Longhorn volume state + pod-restart deltas)
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: monitoring
          podSelector:
            matchLabels:
              app.kubernetes.io/name: prometheus
      ports:
        - port: 9090
          protocol: TCP
    # ArgoCD API server
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: argocd
          podSelector:
            matchLabels:
              app.kubernetes.io/name: argocd-server
      ports:
        - port: 8080
          protocol: TCP
    # Kubernetes API server (cert-manager Certificate listing + node listing
    # + the one-shot self-pod read at startup). Standard NetworkPolicy
    # cannot select the apiserver by entity, so we allow egress to the
    # default namespace pods; tighten this for stricter setups.
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: default
      ports:
        - port: 443
          protocol: TCP
        - port: 6443
          protocol: TCP
```

- [ ] **Step 2: ingress.yaml**

```yaml
# deploy/tamagotchi/ingress.yaml
# Optional: browser-facing Ingress. The example below assumes Traefik with
# an Authelia forward-auth middleware. Adjust ingressClassName, annotations,
# and host to match your setup, or replace this entirely with the Ingress
# flavour you use (ingress-nginx, HAProxy, IngressRoute, HTTPRoute, etc.).
#
# Tamagotchi does not authenticate clients itself. Putting it behind a
# forward-auth proxy is the recommended pattern.
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: tamagotchi
  namespace: tamagotchi
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
    traefik.ingress.kubernetes.io/router.middlewares: traefik-redirect@kubernetescrd, authelia-forwardauth-authelia@kubernetescrd
spec:
  ingressClassName: traefik
  tls:
    - hosts:
        - tamagotchi.example.com
      secretName: tamagotchi-tls
  rules:
    - host: tamagotchi.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: tamagotchi
                port:
                  number: 8080
```

- [ ] **Step 3: Validate**

Run: `kubeconform -strict deploy/tamagotchi/networkpolicy.yaml deploy/tamagotchi/ingress.yaml`
Expected: 0 errors (ingress.networking.k8s.io/v1 and networking.k8s.io/v1 are in kubeconform's stock schema set).

- [ ] **Step 4: Commit**

```bash
git add deploy/tamagotchi/networkpolicy.yaml deploy/tamagotchi/ingress.yaml
git commit -m "feat(tamagotchi): add NetworkPolicy + Ingress reference manifests"
```

---

### Task 21: `deploy/tamagotchi/README.md`

**Files:**
- Create: `deploy/tamagotchi/README.md`

Mirror `deploy/cluster-tv/README.md` with the file table, quick-start sequence, and "what to adjust" notes. Add a callout for the downward-API env vars (POD_NAME / POD_NAMESPACE) and the self-pod RBAC role.

- [ ] **Step 1: Write the file**

(Use `cat deploy/cluster-tv/README.md` as the template; swap `cluster-tv` → `tamagotchi`, add the rbac.yaml line about the namespaced Role for self-pod read, mention the `POD_NAME` / `POD_NAMESPACE` downward-API env. Keep the structure identical so users moving between the two binaries find the same shape.)

- [ ] **Step 2: Commit**

```bash
git add deploy/tamagotchi/README.md
git commit -m "docs(tamagotchi): add deploy/ README with quick-start sequence"
```

---

### Task 22: `docs/tamagotchi-deployment.md`

**Files:**
- Create: `docs/tamagotchi-deployment.md`

Narrative companion. Mirror `docs/cluster-tv-deployment.md` structurally:
- "What you need" table (resource → why)
- "Step by step" walk-through (namespace → secret → manifests → ingress)
- "Two trip-wires people most often hit" — for tamagotchi:
  1. The `POD_NAME` / `POD_NAMESPACE` downward-API env vars are required for the birthday read; without them, the pet is permanently age-0 and a warning logs at startup.
  2. The `tamagotchi-self-pod` Role is namespaced; if you change the namespace in `namespace.yaml` you must update the Role's namespace too.

- [ ] **Step 1: Write the file**

Use `docs/cluster-tv-deployment.md` as the template (`cat` it for shape), swap names, add the two trip-wires above, mention the `/widget` URL for embedding into homepage dashboards.

- [ ] **Step 2: Commit + push**

```bash
git add docs/tamagotchi-deployment.md
git commit -m "docs(tamagotchi): add deployment narrative guide"
git push origin main
```

This is the final commit of the plan. After push, CI runs the `build-tamagotchi` job and produces `ghcr.io/madic-creates/tamagotchi:latest` plus a SHA-tagged variant. Renovate will pick up the new image when downstream consumers (or the user's home cluster) pin a digest.

---

## Verification checklist (run after Task 22)

- [ ] `go test -race ./...` — all green (cluster-tv tests untouched, tamagotchi tests pass)
- [ ] `go vet ./...` — clean
- [ ] `golangci-lint run` — clean, no ST1021 / errcheck regressions
- [ ] `docker build -f Dockerfile.tamagotchi -t tamagotchi:dev .` — builds, image ≤ 30 MiB
- [ ] `kubeconform -strict deploy/tamagotchi/*.yaml` — 0 errors (or `kubectl apply --dry-run=client -f deploy/tamagotchi/`)
- [ ] CI shows both `build-cluster-tv` and `build-tamagotchi` green on first push
- [ ] `ghcr.io/madic-creates/tamagotchi:latest` is published and pullable
- [ ] Smoke test on a real cluster: deploy via `deploy/tamagotchi/`, hit `/`, force one ArgoCD app to `Degraded`, verify mood drops to `meh` within ~30s, restore the app, verify mood climbs back to `ecstatic` after ~5 minutes (the hysteresis window)

---

## Notes for the executing agent

- **Do not re-architect.** The cluster-tv aggregator/state pattern is duplicated here intentionally. Don't extract `runSource`, `Slot[T]`, or pollFunc to a shared `internal/` package as part of this plan — that's a separate refactor that should land after a third binary exists.
- **Keep `internal/` packages additive.** Add files (`nodes.go`, `selfpod.go`) — never edit `internal/kube/client.go`. The project's CLAUDE.md is explicit on this.
- **Watch the ST1021 lint.** Any new doc comment above a generic type (e.g. on `Slot[T]`) must start with the bare name (`// Slot holds...`), not `// Slot[T] holds...`. This caught cluster-tv mid-implementation.
- **Don't introduce new third-party deps.** The spec doesn't require any. Stay on stdlib + the existing `client-go` / `prometheus/client_golang`.
- **Frequent commits — one per task.** The `git commit` at the end of each task is non-negotiable; reviewing 22 tasks as 22 commits is much easier than as one mega-commit.
