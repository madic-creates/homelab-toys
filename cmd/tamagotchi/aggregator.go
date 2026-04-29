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
