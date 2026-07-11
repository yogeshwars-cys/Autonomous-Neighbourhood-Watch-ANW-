package agent

import (
	"math"
	"time"
)

// Tuning constants for the local reasoning. These are deliberately simple
// and deliberately named — Milestone 1's goal was "an agent that decides
// something, and can explain why," not a good anomaly detector. They
// remain the FIXED defaults every agent starts from; Objective 7
// (Adaptive Behaviour, Objective 7) builds volatility-aware thresholds
// on top of them in adaptive.go rather than replacing them outright —
// see AdaptiveThresholds.BaseWatch/BaseAlert.
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

// relativeSignalSigned is "how far, and in which direction, obs.Value
// is from baseline" on the same relative scale DangerScore already
// uses — a deviation of 5 means something different at baseline=1000
// than at baseline=10. Kept signed (unlike the DangerScore path, which
// only ever needed magnitude) because Milestone 6's FeatureVector
// (feature.go) needs direction to tell rising from falling and to count
// baseline crossings — information math.Abs would destroy.
func relativeSignalSigned(value, baseline float64) float64 {
	return (value - baseline) / (math.Abs(baseline) + 1e-6)
}

// relativeSignal is the magnitude-only form Observe has used for
// DangerScore since Milestone 1 — a thin wrapper over
// relativeSignalSigned so both call sites are provably computing the
// exact same underlying quantity, not two formulas that happen to look
// similar.
func relativeSignal(value, baseline float64) float64 {
	return math.Abs(relativeSignalSigned(value, baseline))
}

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

	// Surprise is relative, not absolute — see relativeSignal above.
	signal := relativeSignal(obs.Value, s.Baseline)

	s.DangerScore = clamp(
		s.DangerScore*(1-decayRate)+signal*decayRate,
		0, 1,
	)

	// Milestone 6 (Objective 8): if event detection is enabled, classify
	// this tick's SHAPE (not just its magnitude) against the OLD
	// baseline — the same baseline this tick's DangerScore was just
	// computed against, before the EMA update below moves it. An event
	// classified against next tick's baseline would be reasoning about
	// data it hasn't seen yet.
	if s.Detector != nil {
		s.updateEvents(obs, signal)
	}

	// Baseline adapts AFTER danger score (and event detection) are
	// computed against the OLD baseline, so a spike is still seen as a
	// spike on the tick it happens, not silently absorbed in the same
	// step.
	s.Baseline = s.Baseline*(1-baselineAlpha) + obs.Value*baselineAlpha

	// Objective 7: if adaptive thresholds are enabled, feed this tick's
	// DangerScore (which Milestone 6's event weighting above may have
	// already raised — an agent should widen its tolerance in response
	// to whatever it ultimately considers dangerous, event-boosted or
	// not) into the volatility tracker BEFORE evaluating status, so a
	// sudden spike immediately widens tolerance for the tick that
	// caused it, not one tick later.
	if s.Adaptive != nil {
		s.Adaptive.Update(s.DangerScore)
	}

	s.updateStatus()
	s.LastUpdated = obs.Timestamp
}

func (s *State) updateStatus() {
	watch, alert := s.effectiveThresholds()
	switch {
	case s.DangerScore >= alert:
		s.Status = StatusAlert
	case s.DangerScore >= watch:
		s.Status = StatusWatching
	default:
		s.Status = StatusCalm
	}
}

// Explain returns a short human-readable justification for the agent's
// current status. The README lists "Explainable behavior" as a design
// principle — this is that principle enforced at the type level: there's
// always a one-line answer to "why do you think that?"
//
// Milestone 6: when event detection is enabled and something is
// currently active, the WHAT (Event.Explain, event.go) is appended
// after the base danger/baseline line rather than replacing it — an
// agent without events enabled, or one that's currently Normal, keeps
// exactly the Milestone 1-5 output untouched.
func (s *State) Explain() string {
	base := s.Status.String() + ": danger=" +
		trimFloat(s.DangerScore) + " baseline=" + trimFloat(s.Baseline)
	if s.ActiveEvent != nil {
		base += " | " + s.ActiveEvent.Explain(time.Now())
	}
	return base
}
