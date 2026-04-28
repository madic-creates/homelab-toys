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
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
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
		n, err := strconv.Atoi(s.Value)
		if err != nil {
			slog.Warn("prometheus value not int", "value", s.Value, "metric", s.Metric, "error", err)
			continue
		}
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
	out := RestartsData{}
	for _, s := range samples {
		n, err := strconv.Atoi(s.Value)
		if err != nil {
			slog.Warn("prometheus value not int", "value", s.Value, "metric", s.Metric, "error", err)
			continue
		}
		out.Pods = append(out.Pods, RestartingPod{
			Namespace: s.Metric["namespace"],
			Pod:       s.Metric["pod"],
			Container: s.Metric["container"],
			Restarts:  n,
		})
	}
	out.Total = len(out.Pods)
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
