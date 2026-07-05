package agent

import "time"

// Status is the agent's local conclusion about its own situation.
// It is opinion, not fact — two agents can legitimately disagree.
type Status int

const (
	StatusCalm Status = iota
	StatusWatching
	StatusAlert
)

func (s Status) String() string {
	switch s {
	case StatusCalm:
		return "CALM"
	case StatusWatching:
		return "WATCHING"
	case StatusAlert:
		return "ALERT"
	default:
		return "UNKNOWN"
	}
}

// Observation is a single timestamped sample from the agent's local
// environment. It never contains information about any other agent.
type Observation struct {
	Timestamp time.Time
	Value     float64
}

// State is everything the agent privately knows.
//
// Research question this answers (Objective 2 — Local Intelligence):
// "What information should never be global?" Answer, for now: all of it.
// Nothing in this struct is shared automatically. Communication (Objective 3)
// will later decide, deliberately, what subset of this gets sent to a peer —
// state and communication are kept as separate concerns from day one.
type State struct {
	ID          string
	Status      Status
	DangerScore float64 // 0.0 = nothing unusual, 1.0 = certain anomaly
	Baseline    float64 // running expectation of "normal", learned locally
	History     []Observation
	LastUpdated time.Time

	// CooperativeDanger is the blended danger score incorporating peer
	// signals via trust-weighted cooperation. Zero when no peers are
	// available (Milestone 1 behavior). Added in Milestone 5.
	CooperativeDanger float64

	// Adaptive holds Objective 7's volatility-driven thresholds. Nil by
	// default (see EnableAdaptiveThresholds in adaptive.go) so every
	// State from Milestones 1-5 keeps behaving exactly as before unless
	// an agent explicitly opts in.
	Adaptive *AdaptiveThresholds
}

const historyLimit = 50

func (s *State) recordHistory(obs Observation) {
	s.History = append(s.History, obs)
	if len(s.History) > historyLimit {
		s.History = s.History[len(s.History)-historyLimit:]
	}
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// updateStatusFromCooperative re-evaluates the agent's status using
// CooperativeDanger instead of the raw local DangerScore. This is
// the moment where Milestone 5's cooperation actually takes effect:
// a peer's alarm can push this agent into WATCHING or ALERT even if
// its own local sensor is calm.
func (s *State) updateStatusFromCooperative() {
	if s.CooperativeDanger == 0 {
		return // no cooperation data — keep status from local reasoning
	}
	watch, alert := s.effectiveThresholds()
	switch {
	case s.CooperativeDanger >= alert:
		s.Status = StatusAlert
	case s.CooperativeDanger >= watch:
		s.Status = StatusWatching
	default:
		s.Status = StatusCalm
	}
}
