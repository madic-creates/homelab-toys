package main

import (
	"sync"
	"time"

	"github.com/madic-creates/homelab-toys/internal/health"
)

// stalenessWindow mirrors cluster-tv's threshold. Stale sources are
// excluded from the mood penalty sum and reported via StaleSources.
const stalenessWindow = 5 * time.Minute

// Slot holds the most recent successful payload plus heartbeat metadata
// for one source. Layout matches cluster-tv's Slot[T] — duplicated rather
// than shared because both binaries are small enough that a 30-line
// generic type doesn't justify a new internal/ package.
type Slot[T any] struct {
	Data        T         `json:"data"`
	LastSuccess time.Time `json:"last_success"`
	LastFailure time.Time `json:"last_failure,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	Loaded      bool      `json:"loaded"`
}

// IsStale reports whether the source has not had a successful poll within
// the staleness window. Unloaded sources are always stale.
func (s Slot[T]) IsStale(now time.Time) bool {
	if !s.Loaded {
		return true
	}
	return now.Sub(s.LastSuccess) > stalenessWindow
}

// Snapshot is the public, deep-copied view of State at one moment.
type Snapshot struct {
	ArgoCD       Slot[int] `json:"argocd"`
	Longhorn     Slot[int] `json:"longhorn"`
	Certs        Slot[int] `json:"certs"`
	Restarts     Slot[int] `json:"restarts"`
	Nodes        Slot[int] `json:"nodes"`
	Birthday     time.Time `json:"birthday"`
	Mood         health.Mood
	Confused     bool
	StaleSources []string
	HasFirstTick bool // mirrors history.FirstSuccess != nil — used by handlers for the Hallo bubble
}

// State is the aggregator's shared store. Per-source fields hold an int
// penalty (computed by the pollFunc) plus heartbeat metadata. Birthday
// and history are tamagotchi-specific.
type State struct {
	mu      sync.RWMutex
	snap    Snapshot
	history health.History
}

// NewState returns a zero-valued State ready for use.
func NewState() *State {
	return &State{}
}

// SetArgoCD records a successful ArgoCD poll with the given penalty.
func (s *State) SetArgoCD(p int, now time.Time) { s.setSource(&s.snap.ArgoCD, p, now) }

// SetLonghorn records a successful Longhorn poll with the given penalty.
func (s *State) SetLonghorn(p int, now time.Time) { s.setSource(&s.snap.Longhorn, p, now) }

// SetCerts records a successful cert-manager poll with the given penalty.
func (s *State) SetCerts(p int, now time.Time) { s.setSource(&s.snap.Certs, p, now) }

// SetRestarts records a successful pod-restart poll with the given penalty.
func (s *State) SetRestarts(p int, now time.Time) { s.setSource(&s.snap.Restarts, p, now) }

// SetNodes records a successful node poll with the given penalty.
func (s *State) SetNodes(p int, now time.Time) { s.setSource(&s.snap.Nodes, p, now) }

// SetArgoCDError records an ArgoCD poll failure; preserves the previous penalty.
func (s *State) SetArgoCDError(err error, now time.Time) { s.setSourceError(&s.snap.ArgoCD, err, now) }

// SetLonghornError records a Longhorn poll failure; preserves the previous penalty.
func (s *State) SetLonghornError(err error, now time.Time) {
	s.setSourceError(&s.snap.Longhorn, err, now)
}

// SetCertsError records a cert-manager poll failure; preserves the previous penalty.
func (s *State) SetCertsError(err error, now time.Time) { s.setSourceError(&s.snap.Certs, err, now) }

// SetRestartsError records a pod-restart poll failure; preserves the previous penalty.
func (s *State) SetRestartsError(err error, now time.Time) {
	s.setSourceError(&s.snap.Restarts, err, now)
}

// SetNodesError records a node poll failure; preserves the previous penalty.
func (s *State) SetNodesError(err error, now time.Time) { s.setSourceError(&s.snap.Nodes, err, now) }

func (s *State) setSource(slot *Slot[int], p int, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot.Data = p
	slot.LastSuccess = now
	slot.LastError = ""
	slot.Loaded = true
}

func (s *State) setSourceError(slot *Slot[int], err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot.LastFailure = now
	slot.LastError = err.Error()
}

// SetBirthday is called once at process startup with the pod's
// creationTimestamp. Subsequent calls overwrite (cheap; would only happen
// in test code).
func (s *State) SetBirthday(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Birthday = t
}

// UpdateMood runs the mood algorithm against the current source state and
// stores the result on the snapshot. The aggregator calls this at the end
// of every tick (after writing per-source updates), so the read side
// always observes a coherent (sources, mood) pair under one snapshot.
func (s *State) UpdateMood(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := health.Sources{
		ArgoCD:   slotToSource(s.snap.ArgoCD),
		Longhorn: slotToSource(s.snap.Longhorn),
		Certs:    slotToSource(s.snap.Certs),
		Restarts: slotToSource(s.snap.Restarts),
		Nodes:    slotToSource(s.snap.Nodes),
	}
	res := health.Compute(src, s.history, now)
	s.history = res.History
	s.snap.Mood = res.Current
	s.snap.Confused = res.Confused
	s.snap.StaleSources = append(s.snap.StaleSources[:0], res.StaleSources...)
	s.snap.HasFirstTick = s.history.FirstSuccess != nil
}

func slotToSource(sl Slot[int]) health.Source {
	return health.Source{
		Loaded:      sl.Loaded,
		LastSuccess: sl.LastSuccess,
		Penalty:     sl.Data,
	}
}

// Snapshot returns a deep-copied view. The StaleSources slice is cloned
// so callers can mutate freely.
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.snap
	if len(s.snap.StaleSources) > 0 {
		out.StaleSources = append([]string(nil), s.snap.StaleSources...)
	} else {
		out.StaleSources = nil
	}
	return out
}
