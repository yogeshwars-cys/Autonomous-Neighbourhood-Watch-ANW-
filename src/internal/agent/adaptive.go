package agent

import "math"

// ── Adaptive thresholds ─────────────────────────────────────────────
//
// decision.go's watchThreshold/alertThreshold have been fixed constants
// since Milestone 1, and milestone-5-design.md said so explicitly:
//
//	"No adaptive thresholds. The watch/alert thresholds are still
//	 fixed constants. Making them responsive to network conditions is
//	 Objective 7 (Adaptive Behaviour)."
//
// This file closes that gap for LOCAL conditions (not network — that's
// what CooperativeDanger already handles). The research question this
// answers: an agent whose sensor is naturally noisy (e.g. a busy
// service with legitimately spiky load) will otherwise sit in
// WATCHING/ALERT constantly on fixed thresholds — alarm fatigue, and
// exactly the "false propagation" the README lists as a failure metric,
// since a noisy agent's chatter would also mislead cooperating peers.
// An agent whose sensor is unusually stable, conversely, could afford
// to notice SMALLER deviations sooner.
//
// AdaptiveThresholds tracks running volatility of the agent's OWN
// DangerScore (not raw sensor values — DangerScore is already
// baseline-relative, so this adapts to "how much my own alarm level
// tends to jump around," a level removed from raw noise) using
// Welford's online algorithm: a single deterministic pass, no stored
// history required, no ML.
type AdaptiveThresholds struct {
	n    int
	mean float64
	m2   float64 // Welford's running sum of squared deviations

	// BaseWatch/BaseAlert are the thresholds this converges to when
	// DangerScore has zero volatility — deliberately initialized to
	// the same constants every non-adaptive agent already uses, so
	// enabling adaptive thresholds changes behavior gradually as
	// evidence accumulates, rather than the moment it's switched on.
	BaseWatch float64
	BaseAlert float64
}

const (
	// adaptiveSensitivity (k) controls how many standard deviations of
	// "wiggle room" get added to the base thresholds. Higher k = more
	// tolerant of a noisy history before raising alarms.
	adaptiveSensitivity = 1.5

	// Bounds prevent a pathologically noisy or pathologically quiet
	// history from pushing thresholds somewhere absurd (e.g. alert
	// only at DangerScore=1.0, or watch triggering on Baseline noise
	// alone). The gap between adaptiveMinWatch and adaptiveMaxAlert
	// always leaves room for a WATCHING state — thresholds widen, they
	// never collapse into each other.
	adaptiveMinWatch = 0.15
	adaptiveMaxWatch = 0.55
	adaptiveMinAlert = 0.55
	adaptiveMaxAlert = 0.95
)

// NewAdaptiveThresholds creates an adaptive threshold tracker seeded
// with the project's original fixed constants as its baseline.
func NewAdaptiveThresholds() *AdaptiveThresholds {
	return &AdaptiveThresholds{
		BaseWatch: watchThreshold,
		BaseAlert: alertThreshold,
	}
}

// Update folds one new DangerScore sample into the running volatility
// estimate. Call once per Observe() — see decision.go.
func (a *AdaptiveThresholds) Update(x float64) {
	a.n++
	delta := x - a.mean
	a.mean += delta / float64(a.n)
	delta2 := x - a.mean
	a.m2 += delta * delta2
}

// stddev returns the current sample standard deviation of DangerScore,
// or 0 until at least two samples have been observed (matches Welford's
// standard boundary condition — one sample has no defined variance).
func (a *AdaptiveThresholds) stddev() float64 {
	if a.n < 2 {
		return 0
	}
	return math.Sqrt(a.m2 / float64(a.n))
}

// Thresholds returns the CURRENT effective (watch, alert) cutoffs,
// widened proportionally to how volatile this agent's own DangerScore
// history has been, and clamped to sane bounds.
func (a *AdaptiveThresholds) Thresholds() (watch, alert float64) {
	sd := a.stddev()
	watch = clamp(a.BaseWatch+adaptiveSensitivity*sd, adaptiveMinWatch, adaptiveMaxWatch)
	alert = clamp(a.BaseAlert+adaptiveSensitivity*sd, adaptiveMinAlert, adaptiveMaxAlert)
	return watch, alert
}

// Explain reports the current thresholds and the volatility driving
// them, honoring the README's explainability principle.
func (a *AdaptiveThresholds) Explain() string {
	watch, alert := a.Thresholds()
	return "adaptive thresholds: watch=" + trimFloat(watch) + " alert=" + trimFloat(alert) +
		" (stddev=" + trimFloat(a.stddev()) + " over " + itoa(a.n) + " samples)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// EnableAdaptiveThresholds turns on Objective 7's adaptive sensitivity
// for this agent's local reasoning. It is opt-in and additive, matching
// every other Milestone-gated capability in this codebase (Communicator,
// Trust): a zero-value State (Adaptive == nil) behaves EXACTLY like the
// fixed-threshold agent from Milestones 1-5, so nothing about earlier
// milestones' behavior or tests changes unless this is called.
func (s *State) EnableAdaptiveThresholds() {
	s.Adaptive = NewAdaptiveThresholds()
}

// effectiveThresholds returns the (watch, alert) cutoffs to evaluate
// Status against right now — adaptive ones if enabled, otherwise the
// original Milestone 1 constants.
func (s *State) effectiveThresholds() (watch, alert float64) {
	if s.Adaptive == nil {
		return watchThreshold, alertThreshold
	}
	return s.Adaptive.Thresholds()
}
