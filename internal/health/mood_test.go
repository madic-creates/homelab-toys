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
			// Node penalty is +3; a single node-down reaches "sick" (3).
			// Two nodes down (penalty 6, clamped) reach "dying" (4).
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
		{
			name: "stale source excluded",
			s: Sources{
				ArgoCD:   Source{Loaded: true, LastSuccess: now.Add(-6 * time.Minute), Penalty: 3},
				Longhorn: fresh(0), Certs: fresh(0), Restarts: fresh(0), Nodes: fresh(0),
			},
			want: 0,
		},
		{
			name: "unloaded source excluded",
			s: Sources{
				ArgoCD:   Source{Loaded: false, Penalty: 3},
				Longhorn: fresh(0), Certs: fresh(0), Restarts: fresh(0), Nodes: fresh(0),
			},
			want: 0,
		},
		{
			name: "exactly at staleness boundary is fresh",
			s: Sources{
				ArgoCD:   Source{Loaded: true, LastSuccess: now.Add(-5 * time.Minute), Penalty: 1},
				Longhorn: fresh(0), Certs: fresh(0), Restarts: fresh(0), Nodes: fresh(0),
			},
			want: 1,
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

func TestCompute_Hysteresis_ImmediateWorsening(t *testing.T) {
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
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
	mid := allFresh(t0, 1, 0, 0, 0, 0)  // level 1
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
