package agent

import (
	"math"
	"time"
)

// ── Milestone 7: Long-Term Memory (Objective 9) ─────────────────────
//
// EpisodicMemory (episodic_memory.go) is the agent's diary — bounded,
// importance-decayed, written to on every meaningful status transition
// or resolved event. But until this file, that diary was WRITE-ONLY:
// the agent recorded things and never read them back. Nothing about
// what happened yesterday changed how it reacted today.
//
// LongTermSummary fixes that by maintaining a running statistical
// summary PER EventType (the rich Milestone 6 taxonomy, not the
// coarse EpisodeKind). This is NOT another store of individual
// episodes — it's a set of incremental statistics (Welford's online
// algorithm, the same technique adaptive.go already uses for volatility
// tracking) that answer: "for events of type X, what danger level is
// typical, how much does it vary, and how often does it happen?"
//
// That summary feeds two downstream features:
//
//  1. Familiarity suppression — if the current anomaly's type and
//     severity closely match what this agent has seen many times
//     before, the final DangerScore gets a bounded discount (up to 40%,
//     never more). Applied AFTER the event fold in decision.go, so it
//     cannot be silently overridden by the max-fold.
//
//  2. Periodic detection — a separate, non-decaying timestamp buffer
//     per EventType notices if events of a given type recur on a
//     regular schedule (low coefficient of variation in inter-arrival
//     times). If so, the agent pre-calms itself around the expected
//     next occurrence.
//
// Both are deterministic functions of arithmetic features — no learned
// weights, no ML, per the project's philosophy.

// ── Tuning constants ────────────────────────────────────────────────

const (
	// familiarityMinN is the minimum number of past episodes of a
	// given EventType before any familiarity suppression applies at
	// all. With only 1–2 past episodes, the agent has no statistical
	// basis for "I've seen this before" — the confidence ramp
	// (N / (N + familiarityMinN)) is still close to zero.
	familiarityMinN = 5

	// familiarityMaxDiscount (w_M) caps how much a perfectly familiar
	// event can reduce the DangerScore. 0.40 means even an event the
	// agent has seen 100 times can only be discounted by 40%, never
	// eliminated. This is an intentional guardrail against
	// normalization of deviance: familiarity ≠ safety.
	familiarityMaxDiscount = 0.40

	// periodicMinPoints is the minimum number of timestamps required
	// before the PeriodicTracker will even attempt to compute a
	// coefficient of variation. Below this, there isn't enough
	// history to distinguish "regular" from "coincidence."
	periodicMinPoints = 4

	// periodicCVThreshold is the maximum coefficient of variation
	// (σ/μ of inter-arrival gaps) for a pattern to be considered
	// periodic. 0.30 means the gaps must be within roughly ±30% of
	// the mean gap — enough regularity to be a genuine rhythm.
	periodicCVThreshold = 0.30

	// periodicWindowFraction defines how close the current time must
	// be to the predicted next occurrence (as a fraction of the
	// estimated period) for the "expecting this" bonus to apply.
	// 0.25 means ±25% of the period around the expected arrival.
	periodicWindowFraction = 0.25

	// periodicBufferSize caps the timestamp buffer per EventType.
	// Large enough to detect multi-minute periods, small enough to
	// stay cheap.
	periodicBufferSize = 50
)

// ── Welford per-EventType statistics ────────────────────────────────

// WelfordStat tracks incremental mean and variance for one EventType's
// danger levels using Welford's online algorithm — the same technique
// adaptive.go uses for volatility, proven correct for single-pass,
// bounded-memory statistics without storing raw values.
type WelfordStat struct {
	N    int
	Mean float64
	M2   float64 // running sum of squared deviations from mean
}

// Record folds one new danger observation into the running stats.
func (w *WelfordStat) Record(danger float64) {
	w.N++
	delta := danger - w.Mean
	w.Mean += delta / float64(w.N)
	delta2 := danger - w.Mean
	w.M2 += delta * delta2
}

// Variance returns the population variance, or 0 if fewer than 2
// samples have been recorded.
func (w *WelfordStat) Variance() float64 {
	if w.N < 2 {
		return 0
	}
	return w.M2 / float64(w.N)
}

// Stddev returns the population standard deviation.
func (w *WelfordStat) Stddev() float64 {
	return math.Sqrt(w.Variance())
}

// ── LongTermSummary ─────────────────────────────────────────────────

// LongTermSummary is the agent's aggregated, per-EventType statistical
// memory. It never stores individual episodes — only their incremental
// statistics and a separate timestamp buffer for periodicity detection.
type LongTermSummary struct {
	stats   map[EventType]*WelfordStat
	tracker *PeriodicTracker
}

// NewLongTermSummary creates an empty long-term memory.
func NewLongTermSummary() *LongTermSummary {
	return &LongTermSummary{
		stats:   make(map[EventType]*WelfordStat),
		tracker: NewPeriodicTracker(),
	}
}

// Record updates the running statistics for the given EventType with
// a new resolved episode's danger level, and records the timestamp for
// periodic pattern detection.
func (lt *LongTermSummary) Record(kind EventType, danger float64, now time.Time) {
	w, ok := lt.stats[kind]
	if !ok {
		w = &WelfordStat{}
		lt.stats[kind] = w
	}
	w.Record(danger)
	lt.tracker.AddTimestamp(kind, now)
}

// Stat returns the WelfordStat for a given EventType, or nil if
// no episodes of that type have been recorded.
func (lt *LongTermSummary) Stat(kind EventType) *WelfordStat {
	return lt.stats[kind]
}

// Familiarity computes Φ_t ∈ [0,1] for the given EventType and
// current danger level. Returns 0 if fewer than familiarityMinN
// episodes have been seen (the confidence ramp hasn't reached a
// meaningful level yet).
//
// Formula:
//
//	Φ = (N / (N + N_min)) × exp(-(D - μ)² / (2σ² + ε))
//
// The first term is the confidence ramp — more history means more
// trust in the summary. The second term is the severity match — a
// current reading that exactly matches the historical mean gets a
// full ramp, while one that deviates widely gets discounted.
func (lt *LongTermSummary) Familiarity(kind EventType, currentDanger float64) float64 {
	w := lt.stats[kind]
	if w == nil || w.N < 1 {
		return 0
	}

	// Confidence ramp: N / (N + N_min)
	confidenceRamp := float64(w.N) / float64(w.N+familiarityMinN)

	// Severity match: exp(-(D - μ)² / (2σ² + ε))
	variance := w.Variance()
	diff := currentDanger - w.Mean
	severityMatch := math.Exp(-(diff * diff) / (2*variance + 1e-6))

	return confidenceRamp * severityMatch
}

// IsExpected returns true if the given EventType is periodic AND the
// current time falls within the "expecting next occurrence" window.
func (lt *LongTermSummary) IsExpected(kind EventType, now time.Time) bool {
	return lt.tracker.IsExpected(kind, now)
}

// ── PeriodicTracker ─────────────────────────────────────────────────

// PeriodicTracker maintains a non-decaying circular buffer of
// timestamps per EventType, completely independent of EpisodicMemory's
// importance-based forgetting. The diary (episodic_memory.go) forgets
// mild events quickly (half-life ~45s), but detecting a recurring
// pattern needs timestamps spanning much longer than that.
type PeriodicTracker struct {
	buffers map[EventType]*timestampBuffer
}

// NewPeriodicTracker creates an empty tracker.
func NewPeriodicTracker() *PeriodicTracker {
	return &PeriodicTracker{
		buffers: make(map[EventType]*timestampBuffer),
	}
}

// AddTimestamp records that an event of the given type occurred at t.
func (p *PeriodicTracker) AddTimestamp(kind EventType, t time.Time) {
	buf, ok := p.buffers[kind]
	if !ok {
		buf = &timestampBuffer{
			times: make([]time.Time, 0, periodicBufferSize),
		}
		p.buffers[kind] = buf
	}
	buf.Add(t)
}

// IsExpected returns true if events of the given type recur with
// low variance (CV < periodicCVThreshold) and the current time is
// close to when the next occurrence is predicted.
func (p *PeriodicTracker) IsExpected(kind EventType, now time.Time) bool {
	buf, ok := p.buffers[kind]
	if !ok {
		return false
	}
	return buf.IsExpected(now)
}

// ── timestampBuffer (internal) ──────────────────────────────────────

// timestampBuffer is a bounded, non-decaying circular buffer of
// timestamps for one EventType.
type timestampBuffer struct {
	times []time.Time
}

// Add appends a timestamp, trimming from the front if at capacity.
func (b *timestampBuffer) Add(t time.Time) {
	b.times = append(b.times, t)
	if len(b.times) > periodicBufferSize {
		b.times = b.times[len(b.times)-periodicBufferSize:]
	}
}

// IsExpected computes the coefficient of variation of inter-arrival
// gaps and checks whether `now` falls within the expected window of
// the next predicted arrival.
func (b *timestampBuffer) IsExpected(now time.Time) bool {
	n := len(b.times)
	if n < periodicMinPoints {
		return false
	}

	// Compute inter-arrival gaps
	gaps := make([]float64, n-1)
	for i := 0; i < n-1; i++ {
		gaps[i] = b.times[i+1].Sub(b.times[i]).Seconds()
	}

	// Mean gap
	var sum float64
	for _, g := range gaps {
		sum += g
	}
	meanGap := sum / float64(len(gaps))
	if meanGap <= 0 {
		return false
	}

	// Variance of gaps
	var sumSqDev float64
	for _, g := range gaps {
		d := g - meanGap
		sumSqDev += d * d
	}
	stdGap := math.Sqrt(sumSqDev / float64(len(gaps)))

	// Coefficient of variation
	cv := stdGap / meanGap
	if cv >= periodicCVThreshold {
		return false // too irregular to be periodic
	}

	// Check if `now` is within the expected window
	lastTime := b.times[n-1]
	expectedNext := lastTime.Add(time.Duration(meanGap * float64(time.Second)))
	windowSize := time.Duration(meanGap * periodicWindowFraction * float64(time.Second))

	diff := now.Sub(expectedNext)
	if diff < 0 {
		diff = -diff
	}
	return diff <= windowSize
}
