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
	s.SetNodes(3, now) // node-down → penalty 3
	s.UpdateMood(now)

	snap := s.Snapshot()
	if snap.Mood.Level != 3 {
		t.Errorf("Mood.Level = %d, want 3 (sick)", snap.Mood.Level)
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
