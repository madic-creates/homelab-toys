package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/madic-creates/homelab-toys/internal/argocd"
	"github.com/madic-creates/homelab-toys/internal/certs"
	"github.com/madic-creates/homelab-toys/internal/kube"
	"github.com/madic-creates/homelab-toys/internal/prom"
)

// pollFunc is what each source goroutine actually runs.
type pollFunc func(ctx context.Context) error

// metricsRecorder is what the aggregator needs from the metrics layer.
// *Handlers in handlers.go satisfies this interface.
type metricsRecorder interface {
	PollTotal(source, result string)
	LastSuccessSeconds(source string, seconds float64)
}

const defaultBackoff = 10 * time.Second //nolint:unused // wired by main.go (Task 15)

// runSource is the production wrapper around runSourceWithBackoff, using
// the spec's 10-second post-panic backoff.
func runSource(ctx context.Context, name string, poll pollFunc, interval time.Duration, m metricsRecorder) { //nolint:unused // wired by main.go (Task 15)
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
		// The goroutine returned because of either ctx-cancel (we exit
		// the outer loop on the next iteration) or a recovered panic (we
		// sleep and retry).
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
