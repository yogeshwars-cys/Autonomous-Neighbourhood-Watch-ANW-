package agent

import (
	"fmt"
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

	// Milestone 7 (Objective 9): familiarity suppression. Applied
	// AFTER the event fold (updateEvents above) and adaptive
	// thresholds, so the max-fold cannot silently override it. If the
	// current anomaly's EventType closely matches what this agent has
	// seen many times before, apply a bounded discount (up to 40%).
	//
	// The suppression is the very last transform on DangerScore before
	// status evaluation — documented in the M7 design doc as:
	// "Familiarity discounts the final DangerScore by up to 40%,
	// applied after event classification."
	s.MemoryDiscount = 0
	if s.MemorySuppressEnabled && s.LongTerm != nil {
		currentKind := EventNormal
		if s.ActiveEvent != nil {
			currentKind = s.ActiveEvent.Type
		}
		phi := s.LongTerm.Familiarity(currentKind, s.DangerScore)
		// Periodic bonus: if this event type is expected around now,
		// apply maximum suppression regardless of severity match.
		if s.LongTerm.IsExpected(currentKind, obs.Timestamp) {
			phi = float64(s.LongTerm.Stat(currentKind).N) / float64(s.LongTerm.Stat(currentKind).N+familiarityMinN)
		}
		if phi > 0 {
			discount := familiarityMaxDiscount * phi
			s.MemoryDiscount = discount
			s.DangerScore = clamp(s.DangerScore*(1-discount), 0, 1)
		}
	}

	s.updateStatus()
	s.LastUpdated = obs.Timestamp
}

// ── Status updates ──────────────────────────────────────────────────

// updateStatus evaluates the final local DangerScore against the
// agent's thresholds to determine its status.
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

// Explain (Milestone 1) gives human-readable insight into the agent's
// current state. Biological immune cells don't have "black boxes"; they
// react to specific, inspectable chemical gradients. In a distributed
// trust network, explainability isn't a feature, it's a structural
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
	
	if s.MemoryDiscount > 0 {
		base += fmt.Sprintf(" (familiarity_discount=%d%%)", int(s.MemoryDiscount*100))
	}

	if s.ActiveEvent != nil {
		base += " | " + s.ActiveEvent.Explain(time.Now())
	}
	return base
}
