package agent

import "math"

// ── Pattern templates: Stage B's "Seen" half ──────────────────────────
//
// A PatternTemplate is a pure function over FeatureVector — deliberately
// NOT a struct of numeric thresholds compared field-by-field, and
// deliberately NOT anything trained or fitted. It's a hand-written rule,
// same as every other piece of reasoning in this codebase: readable,
// inspectable, and exactly as defensible as the comment justifying it.
// Rule-based only, per the README's non-goals — no ML anywhere in this
// file.
//
// Templates are tried in a fixed priority order (DefaultPatternLibrary,
// below) and the FIRST match wins. The order matters and is itself a
// design decision, not an accident of slice literal ordering:
//
//  1. SustainedSpike and SuddenDrop are checked before the slower-moving
//     patterns (GradualDrift, Plateau) because they're the most
//     time-sensitive — a real spike or drop that's still unfolding
//     shouldn't be mistaken for a plateau just because it's evaluated a
//     tick too late.
//  2. Oscillation is checked before GradualDrift/Plateau because a
//     genuinely oscillating signal can transiently produce a nonzero
//     Slope that might otherwise look like drift — checking the
//     higher-variance, higher-ZeroCrossings explanation first avoids
//     misreading noise as trend.
//  3. Plateau is checked last among the "shape" patterns because it's
//     the least specific: IsStable-and-elevated is also compatible with
//     the tail end of a spike that hasn't fully classified as
//     SustainedSpike yet. Giving the more specific patterns first pick
//     avoids Plateau "stealing" ticks that a sharper pattern should
//     have claimed.

// ── Stage B thresholds ────────────────────────────────────────────
//
// All operate on the SAME relative scale as watchThreshold/alertThreshold
// (decision.go) and FeatureVector (feature.go) — see feature.go's header
// comment for why. Each constant below is named for the one condition it
// gates, matching the table in PLAN_2's Milestone 6 spec.
const (
	// spikeSteepThreshold: minimum per-tick Slope to call a rise "steep"
	// enough to be a spike rather than an ordinary drift.
	spikeSteepThreshold = 0.15

	// oscillationVarianceThreshold: minimum rolling stddev (FeatureVector.Variance)
	// to call a window "noisy" enough to be oscillation rather than a
	// single clean transition.
	oscillationVarianceThreshold = 0.08

	// minZeroCrossings: fewest sign changes within the window to call
	// it oscillation rather than one up-then-down (or down-then-up) move.
	minZeroCrossings = 3

	// driftSlopeThreshold: minimum |Slope| to call a window "drifting" —
	// deliberately smaller than spikeSteepThreshold, since GradualDrift
	// is explicitly the SLOW-moving counterpart to SustainedSpike.
	driftSlopeThreshold = 0.04

	// dropDeltaThreshold: minimum single-tick negative Delta magnitude
	// to call it a sudden drop.
	dropDeltaThreshold = 0.25

	// minPatternDuration / longPatternDuration: how many trailing
	// abnormal ticks (FeatureVector.Duration) a pattern needs to have
	// lasted before it's trusted as that pattern rather than noise.
	// longPatternDuration is deliberately larger — GradualDrift, by
	// definition, needs more history to distinguish from a single sharp
	// move than the other patterns do.
	minPatternDuration  = 3
	longPatternDuration = 6
)

// PatternTemplate is one recognized "Seen" shape: a name, the EventType
// it maps to, a pure match predicate, and the confidence assigned on a
// match. MinConfidence is a fixed constant rather than a computed score
// deliberately — matching PLAN_2's spec table exactly, and keeping
// "why did this get confidence 0.7" answerable by pointing at one
// number in one table, not a formula.
type PatternTemplate struct {
	Type          EventType
	Name          string
	Match         func(FeatureVector) bool
	MinConfidence float64
}

// PatternLibrary holds the ordered set of known templates Stage B tries
// against a FeatureVector.
type PatternLibrary struct {
	Templates []PatternTemplate
}

// DefaultPatternLibrary builds the five built-in "Seen" templates from
// PLAN_2's Milestone 6 spec table.
func DefaultPatternLibrary() *PatternLibrary {
	return &PatternLibrary{Templates: []PatternTemplate{
		{
			Type:          EventSustainedSpike,
			Name:          "sustained_spike",
			MinConfidence: 0.7,
			Match: func(f FeatureVector) bool {
				return f.IsRising &&
					f.Slope > spikeSteepThreshold &&
					f.Duration >= minPatternDuration &&
					f.MaxDeviation > alertThreshold
			},
		},
		{
			Type:          EventSuddenDrop,
			Name:          "sudden_drop",
			MinConfidence: 0.7,
			Match: func(f FeatureVector) bool {
				return f.IsFalling &&
					f.Delta < -dropDeltaThreshold &&
					f.Duration < 3
			},
		},
		{
			Type:          EventOscillation,
			Name:          "oscillation",
			MinConfidence: 0.6,
			Match: func(f FeatureVector) bool {
				return f.Variance > oscillationVarianceThreshold &&
					f.ZeroCrossings >= minZeroCrossings &&
					f.Duration >= minPatternDuration
			},
		},
		{
			Type:          EventGradualDrift,
			Name:          "gradual_drift",
			MinConfidence: 0.5,
			Match: func(f FeatureVector) bool {
				return math.Abs(f.Slope) > driftSlopeThreshold &&
					f.Duration >= longPatternDuration &&
					f.MaxDeviation < alertThreshold
			},
		},
		{
			Type:          EventPlateau,
			Name:          "plateau",
			MinConfidence: 0.6,
			Match: func(f FeatureVector) bool {
				return f.IsStable &&
					f.MaxDeviation > watchThreshold &&
					f.Duration >= minPatternDuration
			},
		},
	}}
}

// Match tries every template in order and returns the first one whose
// predicate matches. The bool return is false only when NO template
// recognizes the shape — Stage B's signal to the caller (detector.go)
// that this tick is Unseen (NovelPattern), not Seen.
func (l *PatternLibrary) Match(fv FeatureVector) (EventType, float64, bool) {
	for _, tpl := range l.Templates {
		if tpl.Match(fv) {
			return tpl.Type, tpl.MinConfidence, true
		}
	}
	return EventNovelPattern, 0, false
}
