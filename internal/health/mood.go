// Package health provides the pure mood-calculation algorithm for the
// tamagotchi binary. It contains no I/O, no goroutines, and no globals —
// callers supply all inputs as values and receive results as values.
package health

import "time"

// Mood is the discrete pet state shown to the user.
type Mood struct {
	Level int // 0..4
}

// Name returns the string label for the mood level. Out-of-range levels
// return "happy" as a defensive fallback — the algorithm clamps before
// constructing Mood, so this branch is unreachable in production.
func (m Mood) Name() string {
	switch m.Level {
	case 0:
		return "ecstatic"
	case 1:
		return "happy"
	case 2:
		return "meh"
	case 3:
		return "sick"
	case 4:
		return "dying"
	default:
		return "happy"
	}
}

// Source is one upstream signal as the aggregator hands it to the mood
// calculator. Penalty is computed by the aggregator from the raw source
// data (e.g. "≥1 degraded ArgoCD app" → 1) so the calculator stays free
// of source-specific shapes.
type Source struct {
	Loaded      bool
	LastSuccess time.Time
	Penalty     int
}

// Sources bundles all five inputs the mood calculator considers.
type Sources struct {
	ArgoCD   Source
	Longhorn Source
	Certs    Source
	Restarts Source
	Nodes    Source
}

// stalenessWindow is how old LastSuccess can be before a source is
// excluded from the penalty sum. Matches the cluster-tv staleness rule.
const stalenessWindow = 5 * time.Minute

// SumPenalty applies the spec's penalty table to fresh, loaded sources
// and clamps the total to [0, 4]. Stale or unloaded sources are skipped;
// see Compute() for staleness reporting.
func SumPenalty(s Sources, now time.Time) int {
	total := 0
	for _, src := range []Source{s.ArgoCD, s.Longhorn, s.Certs, s.Restarts, s.Nodes} {
		if !isFresh(src, now) {
			continue
		}
		total += src.Penalty
	}
	if total < 0 {
		total = 0
	}
	if total > 4 {
		total = 4
	}
	return total
}

func isFresh(s Source, now time.Time) bool {
	if !s.Loaded {
		return false
	}
	return now.Sub(s.LastSuccess) <= stalenessWindow
}

// Pending is an in-flight improvement candidate that the algorithm holds
// before applying. Target is the better mood the calculator wants to
// move to; Since is when that target first appeared. After
// improvementHold time has elapsed at the same Target, the algorithm
// promotes Pending into Current and clears Pending.
type Pending struct {
	Target Mood
	Since  time.Time
}

// History is the persisted state the mood algorithm threads through ticks.
// It lives in cmd/tamagotchi/state.go and is updated atomically per tick.
type History struct {
	Current      Mood
	Pending      *Pending
	FirstSuccess *time.Time // nil until the first tick where any source is Loaded
}

// Result is what Compute returns: the updated history plus presentation
// data for the handlers.
type Result struct {
	History      History
	Current      Mood
	StaleSources []string // names of sources that have data but are older than stalenessWindow
	Confused     bool     // ≥2 stale sources (per spec)
}

const improvementHold = 5 * time.Minute

// Compute applies the spec's mood algorithm to the given Sources, threading
// History across ticks. The function is pure — all time inputs come via
// `now` — so tests can drive it deterministically without a Clock interface.
func Compute(s Sources, h History, now time.Time) Result {
	// Init grace + staleness reporting are added in Task 5; this task
	// implements the hysteresis state machine over fresh, loaded sources.

	level := SumPenalty(s, now)
	next := Mood{Level: level}

	switch {
	case next.Level > h.Current.Level:
		// Worsening — single-step jump, discard any pending improvement.
		h.Current = next
		h.Pending = nil
	case next.Level < h.Current.Level:
		// Improvement candidate.
		switch {
		case h.Pending == nil || h.Pending.Target.Level != next.Level:
			// New candidate or target shift — (re)start the window.
			h.Pending = &Pending{Target: next, Since: now}
		case now.Sub(h.Pending.Since) >= improvementHold:
			// Window has elapsed at the same target — apply.
			h.Current = h.Pending.Target
			h.Pending = nil
		}
	default:
		// Stable — discard any pending improvement (it's no longer needed).
		h.Pending = nil
	}

	return Result{History: h, Current: h.Current}
}
