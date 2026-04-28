package main

import (
	"sync"
	"testing"
	"time"
)

func TestState_SnapshotIsCopy(t *testing.T) {
	s := NewState()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)

	s.SetArgoCD(ArgoCDData{Healthy: 5, Degraded: 1, OutOfSync: 0,
		Bad: []ArgoCDApp{{Name: "foo", Sync: "Synced", Health: "Degraded"}}}, now)

	snap := s.Snapshot()
	if snap.ArgoCD.Data.Healthy != 5 {
		t.Fatalf("snapshot.ArgoCD.Data.Healthy = %d", snap.ArgoCD.Data.Healthy)
	}
	// Mutating the snapshot must not leak back to the live State.
	snap.ArgoCD.Data.Bad[0].Name = "mutated"
	snap2 := s.Snapshot()
	if snap2.ArgoCD.Data.Bad[0].Name != "foo" {
		t.Errorf("snapshot mutation leaked back: %q", snap2.ArgoCD.Data.Bad[0].Name)
	}
}

func TestState_AllGreen(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 10}, now)
	s.SetLonghorn(LonghornData{Healthy: 12}, now)
	s.SetCerts(CertsData{Total: 0}, now)
	s.SetRestarts(RestartsData{Total: 0}, now)
	if !s.AllGreen(now) {
		t.Errorf("AllGreen = false, want true")
	}

	// One Degraded ArgoCD app → not green.
	s.SetArgoCD(ArgoCDData{Degraded: 1}, now)
	if s.AllGreen(now) {
		t.Errorf("AllGreen with Degraded ArgoCD = true, want false")
	}
}

func TestState_AllGreen_StaleSourceSkipped(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	old := now.Add(-10 * time.Minute) // > 5 minutes
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 10}, old) // stale (loaded but old)
	s.SetLonghorn(LonghornData{Healthy: 12}, now)
	s.SetCerts(CertsData{Total: 0}, now)
	s.SetRestarts(RestartsData{Total: 0}, now)
	// Stale source is excluded from AllGreen but the rest must still be green.
	if !s.AllGreen(now) {
		t.Errorf("AllGreen with one stale-but-otherwise-green source = false, want true")
	}
}

func TestState_AllGreen_FalseDuringInit(t *testing.T) {
	// Spec: "Init phase = first 30s. All sources start in a 'loading' state,
	// tiles show skeleton boxes, bad-news-mode is suppressed." Translated:
	// AllGreen must NOT return true while sources are still unloaded —
	// otherwise the page would briefly flash "CLUSTER OK" at startup.
	s := NewState()
	if s.AllGreen(time.Now()) {
		t.Errorf("AllGreen on a fresh State = true, want false (no source loaded yet)")
	}
}

func TestState_AllGreen_RequiresAtLeastOneFreshSource(t *testing.T) {
	// All four sources loaded once but every one of them has gone stale.
	// AllGreen must be false: we have no recent evidence of cluster health.
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	old := now.Add(-10 * time.Minute)
	s := NewState()
	s.SetArgoCD(ArgoCDData{Healthy: 10}, old)
	s.SetLonghorn(LonghornData{Healthy: 12}, old)
	s.SetCerts(CertsData{Total: 0}, old)
	s.SetRestarts(RestartsData{Total: 0}, old)
	if s.AllGreen(now) {
		t.Errorf("AllGreen with every source stale = true, want false")
	}
}

func TestState_ConcurrentAccess(t *testing.T) {
	s := NewState()
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.SetArgoCD(ArgoCDData{Healthy: 1}, now)
		}()
		go func() {
			defer wg.Done()
			_ = s.Snapshot()
		}()
	}
	wg.Wait()
}
