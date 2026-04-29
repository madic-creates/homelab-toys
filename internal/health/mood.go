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
