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
