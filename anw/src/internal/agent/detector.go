package agent

import (
	"fmt"
	"time"
)

// ── EventDetector: two-stage classification + lifecycle ──────────────
//
// Two genuinely separate jobs live in this file, kept as two separate
// methods rather than one do-everything function, because they answer
// two different questions:
//
//   - Classify answers "what does THIS tick look like, in isolation?"
//     It is a pure function of (signal, FeatureVector) — call it twice
//     with the same inputs and it returns the same answer. No memory
//     of previous ticks.
//   - Update answers "given what THIS tick looks like, how should the
//     event I'm already tracking (if any) evolve?" This is where
//     memory of previous ticks lives — the lifecycle state machine.
//
// Splitting them is what makes both independently testable: pattern.go
// / detector_test.go can assert "this feature shape classifies as
// SustainedSpike" without any lifecycle bookkeeping getting in the way,
// and lifecycle transitions can be tested by feeding a canned sequence
// of classifications without needing real sensor data to produce them.

// ── Lifecycle tuning ───────────────────────────────────────────────
const (
	// confirmTicks: consecutive abnormal ticks (same event, not
	// necessarily the same Seen sub-type) before Detected -> Confirmed.
	confirmTicks = 2

	// activeTicks: consecutive abnormal ticks before Confirmed ->
	// Active — PLAN_2.md's "sustained for duration threshold."
	activeTicks = 5

	// cooldownTicks: consecutive NORMAL ticks while Decaying before
	// Decaying -> Resolved — the event-level analogue of decision.go's
	// DangerScore decay, expressed as a tick count instead of a rate so
	// "how long until this counts as over" has one clear answer instead
	// of an asymptotic curve.
	cooldownTicks = 3

	// novelMinDuration: minimum trailing abnormal ticks (FeatureVector.
	// Duration) before an unrecognized shape is classified NovelPattern.
	// Anything shorter is treated as Normal — a single noisy tick that
	// happens not to match a template shouldn't immediately trigger the
	// highest-danger classification. This is Option C of the learning-
	// phase design: delay novel-pattern classification for transient
	// blips even OUTSIDE the learning window.
	novelMinDuration = 3
)

// EventDetector holds the (stateless, reusable) pattern library used
// for Stage B classification. Everything that DOES carry state between
// ticks — matches, normalStreak — lives on the Event itself (event.go),
// not here, so a single EventDetector could in principle classify for
// many independent events without them interfering with each other.
type EventDetector struct {
	Library *PatternLibrary
}

// NewEventDetector builds a detector wired to the built-in pattern
// library (pattern.go). The only constructor Agent.WithEvents uses —
// a custom Library can still be swapped in afterward for tests that
// want to exercise lifecycle logic against synthetic templates.
func NewEventDetector() *EventDetector {
	return &EventDetector{Library: DefaultPatternLibrary()}
}

// Classify performs the two-stage classifier described in PLAN_2's
// Milestone 6 spec:
//
//	Stage A (Normal vs Abnormal): is this tick's relative deviation
//	  ("signal" — decision.go's relativeSignal, the same quantity
//	  DangerScore is built from) past watchThreshold at all?
//	Stage B (Seen vs Unseen): if abnormal, does any PatternTemplate in
//	  the library recognize the shape (FeatureVector)? If yes, that
//	  template's Type/MinConfidence is the classification. If no
//	  template matches, it's classified NovelPattern with maximum
//	  confidence — the agent is certain it doesn't know what this is,
//	  which is itself a confident (if unhappy) conclusion.
//
// Learning-phase integration: when learning is true, an unrecognized
// abnormal shape is captured as a baseline pattern in the library
// rather than flagged as NovelPattern — the agent is still
// calibrating what "normal noise" looks like.
//
// Duration gating (Option C): even outside the learning phase, an
// unrecognized shape with fewer than novelMinDuration trailing
// abnormal ticks is downgraded to Normal rather than immediately
// triggering the highest-danger NovelPattern — a transient blip
// that doesn't persist isn't worth the scariest classification.
func (d *EventDetector) Classify(signal float64, fv FeatureVector, learning bool) (EventType, float64) {
	if signal < watchThreshold {
		return EventNormal, 1.0
	}
	if t, conf, ok := d.Library.Match(fv); ok {
		return t, conf
	}
	// Abnormal, but nothing in the library recognized the shape.

	// Learning phase: capture this shape as a baseline pattern rather
	// than panicking — the agent is still learning what its sensor's
	// normal noise looks like.
	if learning {
		d.Library.RegisterBaselinePattern(fv)
		return EventBaselinePattern, 0.3
	}

	// Duration gating (Option C): a transient unrecognized blip that
	// hasn't persisted for novelMinDuration ticks is more likely sensor
	// noise than a genuine novel threat. Downgrade to Normal so the
	// lifecycle doesn't immediately create a max-danger event from a
	// single noisy tick.
	if fv.Duration < novelMinDuration {
		return EventNormal, 1.0
	}

	return EventNovelPattern, 1.0
}

// Update advances one tick of lifecycle for a (possibly nil) existing
// event, given this tick's fresh classification. State transitions,
// matching PLAN_2's Milestone 6 spec:
//
//	nil          -> Detected   (first abnormal tick)
//	Detected     -> Confirmed  (matches >= confirmTicks)
//	Confirmed    -> Active     (matches >= activeTicks)
//	{Detected,Confirmed,Active} -> Decaying  (tick returns to Normal)
//	Decaying     -> Active     (anomaly resumes before cooling down)
//	Decaying     -> Resolved   (normalStreak >= cooldownTicks)
//
// Returns nil only when there was nothing to track before AND this
// tick is Normal — "no event" stays representable as a nil pointer
// rather than a synthetic EventNormal Event, so State.ActiveEvent's
// zero value (nil) always means exactly "nothing is happening."
func (d *EventDetector) Update(ev *Event, agentID string, newType EventType, confidence float64, fv FeatureVector, now time.Time) *Event {
	if ev == nil {
		if !newType.IsAbnormal() {
			return nil
		}
		return &Event{
			ID:         nextEventID(agentID, now),
			Type:       newType,
			Status:     EventDetected,
			Confidence: confidence,
			StartTime:  now,
			LastSeen:   now,
			Features:   fv,
			matches:    1,
		}
	}

	next := *ev // copy — callers should treat Event as a value from here
	next.LastSeen = now
	next.Features = fv

	if newType.IsAbnormal() {
		next.Type = newType
		next.Confidence = confidence
		next.matches++
		next.normalStreak = 0

		switch next.Status {
		case EventDetected:
			if next.matches >= confirmTicks {
				next.Status = EventConfirmed
			}
		case EventConfirmed:
			if next.matches >= activeTicks {
				next.Status = EventActive
			}
		case EventDecaying:
			// The anomaly resumed before it ever fully cooled down —
			// back to Active, not back to square one: this is still
			// the same event, just not over yet.
			next.Status = EventActive
		}
		// EventActive with a fresh abnormal tick: stays EventActive.
	} else {
		next.normalStreak++
		switch next.Status {
		case EventDetected, EventConfirmed, EventActive:
			next.Status = EventDecaying
		case EventDecaying:
			if next.normalStreak >= cooldownTicks {
				next.Status = EventResolved
			}
		}
	}

	return &next
}

// nextEventID builds a process-local, human-inspectable event ID. Using
// the observation's own timestamp (rather than a shared package-level
// counter) keeps this safe to call concurrently from multiple agents in
// the same process — exactly the situation the test suite's
// multi-goroutine integration tests already rely on elsewhere (see
// cooperation_large_scale_test.go) — without needing a mutex or an
// atomic counter of its own.
func nextEventID(agentID string, now time.Time) string {
	return fmt.Sprintf("%s-evt-%d", agentID, now.UnixNano())
}

// updateEvents is Observe()'s (decision.go) Milestone 6 hook: classify
// this tick, advance the lifecycle, fold the result into DangerScore,
// and log it. Only ever called when s.Detector != nil.
func (s *State) updateEvents(obs Observation, signal float64) {
	s.LastResolvedEvent = nil // pulse: true for at most the current tick

	fv := ExtractFeatures(s.History, s.Baseline)

	// Pass learning state to Classify so it can capture baseline
	// patterns during the learning phase rather than flagging them.
	learning := s.LearningTicksRemaining > 0
	classifiedType, confidence := s.Detector.Classify(signal, fv, learning)

	// Decrement the learning counter AFTER classification so the
	// current tick still benefits from learning-phase behavior.
	if s.LearningTicksRemaining > 0 {
		s.LearningTicksRemaining--
	}

	s.ActiveEvent = s.Detector.Update(s.ActiveEvent, s.ID, classifiedType, confidence, fv, obs.Timestamp)

	if s.ActiveEvent == nil {
		return
	}

	s.EventLog = recordEvent(s.EventLog, *s.ActiveEvent, eventLogLimit)

	// "The final DangerScore is the maximum active event score, or 0 if
	// no active events" (PLAN_2's Milestone 6 spec) — implemented as a
	// max-fold on top of the existing EMA-based score, rather than a
	// full replacement. A full replacement would have thrown away
	// decision.go's decay behavior (still required: "DangerScore must
	// still decay back to 0 when events resolve") the moment ANY event,
	// however mild, became active. Folding via max means a
	// low-DangerWeight Detected event never LOWERS the agent's existing
	// danger reading, while a high-DangerWeight Active/NovelPattern
	// event can still raise it above what the raw EMA alone would say —
	// satisfying the spec's intent (events can escalate danger) without
	// regressing Milestones 1-5's decay guarantee once an event resolves
	// and stops contributing at all (statusWeight(Resolved) == 0).
	if w := s.ActiveEvent.DangerWeight(); w > s.DangerScore {
		s.DangerScore = w
	}

	if s.ActiveEvent.Status == EventResolved {
		// Resolved is terminal and, by design, never lingers as the
		// live state of anything (see EventStatus's doc comment) — it
		// was already recorded into EventLog above, so clearing here
		// loses nothing. Stash a copy in the one-tick pulse first, so
		// Agent.recordMemory (agent.go) still gets a chance to fold it
		// into episodic memory this same tick.
		resolved := *s.ActiveEvent
		s.LastResolvedEvent = &resolved
		s.ActiveEvent = nil
	}
}
