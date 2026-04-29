package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madic-creates/homelab-toys/internal/argocd"
	"github.com/madic-creates/homelab-toys/internal/certs"
	"github.com/madic-creates/homelab-toys/internal/kube"
	"github.com/madic-creates/homelab-toys/internal/prom"
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

// ---------- MakeNodesPoll tests ----------

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

// ---------- MakeArgoCDPoll tests ----------

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

// ---------- MakeLonghornPoll tests ----------

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

// ---------- MakeRestartsPoll tests ----------

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

// ---------- MakeCertsPoll tests ----------

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
