package main

import (
	"sync"
	"time"
)

// stalenessWindow is the threshold beyond which a source's data is
// considered stale. Stale sources are still rendered (greyed out) but do
// not contribute to AllGreen — the spec calls this out explicitly so a
// brief ArgoCD outage doesn't flip the screen to bad-news.
const stalenessWindow = 5 * time.Minute

// ---------- per-source data shapes ----------

type ArgoCDApp struct {
	Name   string `json:"name"`
	Sync   string `json:"sync"`
	Health string `json:"health"`
}

type ArgoCDData struct {
	Healthy   int         `json:"healthy"`
	Degraded  int         `json:"degraded"`
	OutOfSync int         `json:"out_of_sync"`
	Bad       []ArgoCDApp `json:"bad,omitempty"`
}

type LonghornData struct {
	Healthy  int `json:"healthy"`
	Degraded int `json:"degraded"`
	Faulted  int `json:"faulted"`
	Unknown  int `json:"unknown"`
}

type CertEntry struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	NotAfter  string `json:"not_after"` // RFC3339
}

type CertsData struct {
	Total    int         `json:"total"`
	Expiring []CertEntry `json:"expiring,omitempty"`
}

type RestartingPod struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
	Restarts  int    `json:"restarts"`
}

type RestartsData struct {
	Total int             `json:"total"`
	Pods  []RestartingPod `json:"pods,omitempty"`
}

// ---------- generic slot ----------

// Slot holds the most recent successful payload plus heartbeat metadata
// for one source. Splitting LastSuccess and LastFailure lets the UI show
// "last update X ago" while keeping a separate "last error" message.
type Slot[T any] struct {
	Data        T         `json:"data"`
	LastSuccess time.Time `json:"last_success"`
	LastFailure time.Time `json:"last_failure,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	Loaded      bool      `json:"loaded"` // false until the first successful poll
}

func (s Slot[T]) IsStale(now time.Time) bool {
	if !s.Loaded {
		return true
	}
	return now.Sub(s.LastSuccess) > stalenessWindow
}

// ---------- aggregate State ----------

type Snapshot struct {
	ArgoCD   Slot[ArgoCDData]   `json:"argocd"`
	Longhorn Slot[LonghornData] `json:"longhorn"`
	Certs    Slot[CertsData]    `json:"certs"`
	Restarts Slot[RestartsData] `json:"restarts"`
}

type State struct {
	mu   sync.RWMutex
	snap Snapshot
}

func NewState() *State {
	return &State{}
}

func (s *State) SetArgoCD(d ArgoCDData, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.ArgoCD.Data = d
	s.snap.ArgoCD.LastSuccess = now
	s.snap.ArgoCD.LastError = ""
	s.snap.ArgoCD.Loaded = true
}

func (s *State) SetArgoCDError(err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.ArgoCD.LastFailure = now
	s.snap.ArgoCD.LastError = err.Error()
}

func (s *State) SetLonghorn(d LonghornData, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Longhorn.Data = d
	s.snap.Longhorn.LastSuccess = now
	s.snap.Longhorn.LastError = ""
	s.snap.Longhorn.Loaded = true
}

func (s *State) SetLonghornError(err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Longhorn.LastFailure = now
	s.snap.Longhorn.LastError = err.Error()
}

func (s *State) SetCerts(d CertsData, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Certs.Data = d
	s.snap.Certs.LastSuccess = now
	s.snap.Certs.LastError = ""
	s.snap.Certs.Loaded = true
}

func (s *State) SetCertsError(err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Certs.LastFailure = now
	s.snap.Certs.LastError = err.Error()
}

func (s *State) SetRestarts(d RestartsData, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Restarts.Data = d
	s.snap.Restarts.LastSuccess = now
	s.snap.Restarts.LastError = ""
	s.snap.Restarts.Loaded = true
}

func (s *State) SetRestartsError(err error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Restarts.LastFailure = now
	s.snap.Restarts.LastError = err.Error()
}

// Snapshot returns a deep copy of the current state. Slices are cloned so
// callers can mutate freely without holding the lock.
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.snap
	if len(out.ArgoCD.Data.Bad) > 0 {
		out.ArgoCD.Data.Bad = append([]ArgoCDApp(nil), out.ArgoCD.Data.Bad...)
	}
	if len(out.Certs.Data.Expiring) > 0 {
		out.Certs.Data.Expiring = append([]CertEntry(nil), out.Certs.Data.Expiring...)
	}
	if len(out.Restarts.Data.Pods) > 0 {
		out.Restarts.Data.Pods = append([]RestartingPod(nil), out.Restarts.Data.Pods...)
	}
	return out
}

// AllGreen reports true iff at least one source is fresh AND every fresh
// source is green. We require ≥ 1 fresh source so the UI never flashes
// "CLUSTER OK" while sources are still loading — the spec's init-phase
// rule. Stale-but-loaded sources are excluded so a brief outage doesn't
// flip the screen to bad-news.
func (s *State) AllGreen(now time.Time) bool {
	snap := s.Snapshot()

	type sourceVerdict struct {
		stale, bad bool
	}
	verdicts := []sourceVerdict{
		{snap.ArgoCD.IsStale(now), snap.ArgoCD.Data.Degraded > 0 || snap.ArgoCD.Data.OutOfSync > 0},
		{snap.Longhorn.IsStale(now), snap.Longhorn.Data.Degraded > 0 || snap.Longhorn.Data.Faulted > 0},
		{snap.Certs.IsStale(now), snap.Certs.Data.Total > 0},
		{snap.Restarts.IsStale(now), snap.Restarts.Data.Total > 0},
	}

	freshCount := 0
	for _, v := range verdicts {
		if v.stale {
			continue
		}
		freshCount++
		if v.bad {
			return false
		}
	}
	return freshCount > 0
}

// StaleCount counts stale loaded sources, for the "N source(s) stale" banner.
func (s *State) StaleCount(now time.Time) int {
	snap := s.Snapshot()
	c := 0
	for _, slot := range []struct{ stale bool }{
		{snap.ArgoCD.IsStale(now) && snap.ArgoCD.Loaded},
		{snap.Longhorn.IsStale(now) && snap.Longhorn.Loaded},
		{snap.Certs.IsStale(now) && snap.Certs.Loaded},
		{snap.Restarts.IsStale(now) && snap.Restarts.Loaded},
	} {
		if slot.stale {
			c++
		}
	}
	return c
}
