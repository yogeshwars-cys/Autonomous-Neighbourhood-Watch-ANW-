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

	// ── Milestone 6 (Objective 8): semantic events ──────────────────
	//
	// Detector is nil by default, matching every other milestone-gated
	// capability in this codebase (Communicator, Trust, Adaptive): a
	// bare &State{} behaves EXACTLY like Milestones 1-5 unless an agent
	// opts in via Agent.WithEvents(). Observe() (decision.go) only ever
	// touches ActiveEvent/EventLog when Detector != nil.
	Detector *EventDetector

	// ActiveEvent is the CURRENTLY tracked semantic event, if any — at
	// most one at a time. nil means either events aren't enabled, or
	// they are and nothing abnormal is currently happening.
	ActiveEvent *Event

	// EventLog is this agent's own bounded timeline of events it has
	// classified, most-recently-updated entries included even while
	// still live (see recordEvent, event.go). Distinct from
	// EpisodicMemory.events: EventLog is Milestone 6's structured,
	// typed record of live classification; Episodes are Objective 9's
	// importance-ranked, decaying memory of what happened, which an
	// Event feeds into only once it resolves (see Agent.recordMemory).
	EventLog []Event

	// LastResolvedEvent is a one-tick PULSE, not persistent state: set
	// only on the exact tick an Event transitions to Resolved, and
	// cleared again at the very start of the next Observe() call.
	// Agent.recordMemory (agent.go) reads it to decide whether to fold
	// a just-finished Event into a permanent Episode — "a lived moment
	// becomes a memory once it's over" (episodic_memory.go). nil on
	// every tick nothing resolved.
	LastResolvedEvent *Event

	// ── Learning Phase ─────────────────────────────────────────────────
	//
	// LearningTicksRemaining counts down the agent's initial learning
	// period. While positive, any abnormal feature shape that doesn't
	// match an existing template is captured as a baseline pattern in
	// the detector's library rather than flagged as NovelPattern — the
	// agent is still calibrating what "normal noise" looks like for THIS
	// sensor in THIS environment. Zero (the default) means no learning
	// phase: the agent treats unrecognized shapes as novel immediately,
	// exactly as Milestone 6 always did.
	LearningTicksRemaining int

	// ── Milestone 7: Memory-influenced reasoning ────────────────────
	//
	// MemorySuppressEnabled, when true, allows the agent's long-term
	// memory statistics to apply a bounded familiarity discount to the
	// final DangerScore (up to 40%, applied AFTER the event fold and
	// adaptive thresholds). Default false — exactly like Adaptive,
	// Detector, and every other milestone-gated capability: a bare
	// &State{} behaves identically to Milestones 1-6.
	MemorySuppressEnabled bool

	// MemoryDiscount tracks the actual discount percentage (0.0 to 0.40)
	// applied to the DangerScore during the current tick, used strictly
	// for visibility in Explain().
	MemoryDiscount float64

	// LongTerm (Milestone 7) is the agent's aggregated statistical
	// memory — running Welford stats per EventType and a periodic
	// pattern detector. Nil by default; set by Agent.WithMemorySuppress()
	// (which also sets MemorySuppressEnabled). Observe() checks both
	// fields before consulting it.
	LongTerm *LongTermSummary
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
