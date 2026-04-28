package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madic-creates/homelab-toys/internal/argocd"
	"github.com/madic-creates/homelab-toys/internal/prom"
)

func TestArgoPayloadToData(t *testing.T) {
	in := []argocd.Application{
		{Name: "a", Sync: "Synced", Health: "Healthy"},
		{Name: "b", Sync: "OutOfSync", Health: "Healthy"},
		{Name: "c", Sync: "Synced", Health: "Degraded"},
		{Name: "d", Sync: "OutOfSync", Health: "Degraded"},
	}
	got := argoPayloadToData(in)
	if got.Healthy != 1 {
		t.Errorf("Healthy = %d, want 1", got.Healthy)
	}
	if got.OutOfSync != 2 {
		t.Errorf("OutOfSync = %d, want 2 (b, d)", got.OutOfSync)
	}
	if got.Degraded != 2 {
		t.Errorf("Degraded = %d, want 2 (c, d)", got.Degraded)
	}
	if len(got.Bad) != 3 {
		t.Errorf("len(Bad) = %d, want 3", len(got.Bad))
	}
}

func TestLonghornSamplesToData(t *testing.T) {
	in := []prom.Sample{
		{Metric: map[string]string{"state": "healthy"}, Value: "10"},
		{Metric: map[string]string{"state": "degraded"}, Value: "2"},
		{Metric: map[string]string{"state": "faulted"}, Value: "1"},
		{Metric: map[string]string{"state": "unknown"}, Value: "0"},
	}
	got := longhornSamplesToData(in)
	if got.Healthy != 10 || got.Degraded != 2 || got.Faulted != 1 || got.Unknown != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestRestartSamplesToData(t *testing.T) {
	in := []prom.Sample{
		{Metric: map[string]string{"namespace": "default", "pod": "p1", "container": "app"}, Value: "7"},
		{Metric: map[string]string{"namespace": "kube-system", "pod": "p2", "container": "core"}, Value: "12"},
	}
	got := restartSamplesToData(in)
	if got.Total != 2 {
		t.Errorf("Total = %d, want 2", got.Total)
	}
	if len(got.Pods) != 2 {
		t.Errorf("len(Pods) = %d, want 2", len(got.Pods))
	}
	if got.Pods[0].Restarts != 7 || got.Pods[1].Restarts != 12 {
		t.Errorf("Pods = %+v", got.Pods)
	}
}

func TestRunSource_FirstTickWritesState(t *testing.T) {
	s := NewState()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var pollCalls atomic.Int32
	poll := func(_ context.Context) error {
		pollCalls.Add(1)
		s.SetArgoCD(ArgoCDData{Healthy: 42}, time.Now())
		return nil
	}

	done := make(chan struct{})
	go func() {
		runSource(ctx, "argocd", poll, 5*time.Millisecond)
		close(done)
	}()

	// Wait for at least one poll, then cancel.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if s.Snapshot().ArgoCD.Data.Healthy == 42 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if pollCalls.Load() < 1 {
		t.Errorf("pollCalls = %d, want >= 1", pollCalls.Load())
	}
	if s.Snapshot().ArgoCD.Data.Healthy != 42 {
		t.Errorf("Healthy = %d, want 42", s.Snapshot().ArgoCD.Data.Healthy)
	}
}

func TestRunSource_RecoversFromPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	poll := func(_ context.Context) error {
		n := calls.Add(1)
		if n == 1 {
			panic("boom")
		}
		return nil
	}

	done := make(chan struct{})
	go func() {
		// Use a short backoff for the test.
		runSourceWithBackoff(ctx, "test", poll, 5*time.Millisecond, 20*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if calls.Load() >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if calls.Load() < 2 {
		t.Errorf("calls = %d, want >= 2 (panic should be recovered and the goroutine restarted)", calls.Load())
	}
}

func TestRunSource_PollErrorKeepsRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	poll := func(_ context.Context) error {
		calls.Add(1)
		return errors.New("upstream down")
	}

	done := make(chan struct{})
	go func() {
		runSource(ctx, "test", poll, 5*time.Millisecond)
		close(done)
	}()

	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done

	if calls.Load() < 3 {
		t.Errorf("calls = %d, want >= 3 (errors must not stop the loop)", calls.Load())
	}
	// We don't assert on log output; this test is about not exiting.
}
