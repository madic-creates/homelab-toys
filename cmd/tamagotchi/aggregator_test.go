package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madic-creates/homelab-toys/internal/kube"
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
