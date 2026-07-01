package agent

import "math"

// Tuning constants for the local reasoning. These are deliberately simple
// and deliberately named — Milestone 1's goal is "an agent that decides
// something, and can explain why," not a good anomaly detector. Better
// reasoning is a later research question (Objective 7 — Adaptive Behaviour),
// not something to smuggle in early.
const (
	// watchThreshold / alertThreshold turn a continuous danger score into
	// a discrete status. Two thresholds instead of one means the agent has
	// a "getting suspicious" state before it commits to "alert" — that
	// hysteresis is itself a design decision: it stops a single borderline
	// reading from flipping status on its own.
	watchThreshold = 0.30
	alertThreshold = 0.70

	// baselineAlpha controls how fast "normal" is allowed to drift.
	// Too high: real anomalies get absorbed into the baseline before the
	// agent notices them. Too low: legitimate slow change looks like a
	// permanent anomaly. 0.1 means roughly the last ~10 observations
	// dominate what "normal" means.
	baselineAlpha = 0.10

	// decayRate controls how quickly danger score relaxes back toward 0
	// when nothing unusual is happening. Without decay, danger score would
	// only ever go up — the agent needs to be able to calm down.
	decayRate = 0.15
)

// Observe is the agent's entire local reasoning step: fold one new
// observation into state, and decide what it implies. No neighbor
// information is involved — this is reasoning about "myself," which is
// the whole point of Milestone 1 / Objective 1's question:
// "How does an agent make decisions?"
func (s *State) Observe(obs Observation) {
	s.recordHistory(obs)

	if s.Baseline == 0 && len(s.History) == 1 {
		// First-ever observation: nothing to compare against yet, so it
		// defines the starting baseline rather than counting as a surprise.
		s.Baseline = obs.Value
	}

	deviation := math.Abs(obs.Value - s.Baseline)
	// Surprise is relative, not absolute — a deviation of 5 means something
	// different at baseline=1000 than at baseline=10.
	signal := deviation / (math.Abs(s.Baseline) + 1e-6)

	s.DangerScore = clamp(
		s.DangerScore*(1-decayRate)+signal*decayRate,
		0, 1,
	)

	// Baseline adapts AFTER danger score is computed against the OLD
	// baseline, so a spike is still seen as a spike on the tick it happens,
	// not silently absorbed in the same step.
	s.Baseline = s.Baseline*(1-baselineAlpha) + obs.Value*baselineAlpha

	s.updateStatus()
	s.LastUpdated = obs.Timestamp
}

func (s *State) updateStatus() {
	switch {
	case s.DangerScore >= alertThreshold:
		s.Status = StatusAlert
	case s.DangerScore >= watchThreshold:
		s.Status = StatusWatching
	default:
		s.Status = StatusCalm
	}
}

// Explain returns a short human-readable justification for the agent's
// current status. The README lists "Explainable behavior" as a design
// principle — this is that principle enforced at the type level: there's
// always a one-line answer to "why do you think that?"
func (s *State) Explain() string {
	return s.Status.String() + ": danger=" +
		trimFloat(s.DangerScore) + " baseline=" + trimFloat(s.Baseline)
}
