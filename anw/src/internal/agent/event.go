package agent

import (
	"encoding/json"
	"fmt"
	"time"
)

// ── Milestone 6: Semantic Events (Objective 8) ───────────────────────
//
// Everything up through Milestone 5 reasons about a single float:
// DangerScore. That number answers "how worried should I be," but never
// "worried about WHAT." Two completely different situations — a sensor
// ramping steadily upward, and one flapping wildly between high and low
// — can produce an identical DangerScore while meaning very different
// things operationally. Objective 8 asks the obvious next question:
// "what is an event, and how can raw sensor values become meaningful
// knowledge?" This file is the taxonomy that answers it.
//
// The hierarchy is deliberately a hierarchy, not a flat enum:
//
//	Event
//	├── Normal
//	└── Abnormal
//	    ├── Seen    (SustainedSpike, Oscillation, GradualDrift, Plateau, SuddenDrop)
//	    └── Unseen  (NovelPattern)
//
// The Seen/Unseen split is the actual cognitive leap this milestone
// adds. Every event pipeline before this one implicitly assumed
// "abnormal" was a single bucket. Splitting it forces the agent to be
// honest about the difference between "this is a shape of trouble I
// have a name for" and "this is trouble, and I genuinely don't know
// what kind." The second case is scored as MORE dangerous, not less —
// see eventDangerWeights below — because an unrecognized pattern is
// exactly the case where a rule-based, non-ML system has the least
// basis for confidence.
//
// Deliberately still true after this file: no ML anywhere. Every
// classification here is a deterministic function of a handful of
// arithmetic features (feature.go) run through hand-written pattern
// templates (pattern.go). Nothing is learned; everything is explainable
// by construction (Explain, below) — matching the README's stated
// design principle, not just this milestone's own aspiration.

// EventType is the leaf classification an Event currently holds. It
// spans the WHOLE hierarchy (Normal included) rather than being
// Abnormal-only, because a detector needs a single return value to
// express "nothing is happening" alongside every abnormal shape.
type EventType int

const (
	// EventNormal means Stage A of classification (see detector.go)
	// never even reached Stage B — the observation was within
	// watchThreshold of baseline. Never itself becomes a live Event
	// (see EventDetector.Update): "normal" isn't a thing to track,
	// it's the absence of one.
	EventNormal EventType = iota

	// ── Seen: abnormal, and recognized by a PatternTemplate ─────────
	EventSustainedSpike
	EventOscillation
	EventGradualDrift
	EventPlateau
	EventSuddenDrop

	// ── Baseline: abnormal, but learned during the calibration phase
	// as a shape this environment routinely produces. Scored low —
	// familiar noise, not an unknown threat.
	EventBaselinePattern

	// ── Unseen: abnormal, but no template matched ────────────────────
	EventNovelPattern
)

// String renders the EventType in the same snake_case form used on the
// wire (EventSummary.Type) and in PatternTemplate.Name — one name,
// never translated back and forth between a "display" and a "wire"
// spelling.
func (t EventType) String() string {
	switch t {
	case EventNormal:
		return "normal"
	case EventSustainedSpike:
		return "sustained_spike"
	case EventOscillation:
		return "oscillation"
	case EventGradualDrift:
		return "gradual_drift"
	case EventPlateau:
		return "plateau"
	case EventSuddenDrop:
		return "sudden_drop"
	case EventBaselinePattern:
		return "baseline_pattern"
	case EventNovelPattern:
		return "novel_pattern"
	default:
		return "unknown"
	}
}

// MarshalJSON renders an EventType the same snake_case way String()
// does. Without this, -event-log's dumped JSON (cmd/agent/main.go) would
// show Type as a bare integer (6, say) — unreadable, and a direct
// violation of the README's "explainable behavior" principle for the one
// artifact this codebase writes specifically so a human can inspect it
// later, offline, without the running agent.
func (t EventType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

// The one predicate Stage A's result collapses to.
func (t EventType) IsAbnormal() bool {
	return t != EventNormal
}

// IsSeen reports whether this is a recognized (templated) abnormal
// pattern — the "I know what this is" branch of the hierarchy.
func (t EventType) IsSeen() bool {
	return t.IsAbnormal() && t != EventNovelPattern && t != EventBaselinePattern
}

// IsUnseen reports whether this is abnormal but unrecognized — the "I
// have no model for this" branch. Currently only NovelPattern, but kept
// as its own predicate (rather than inlined as `== EventNovelPattern`)
// so a future second "unseen" category doesn't require touching every
// call site that only cares about the Seen/Unseen distinction.
func (t EventType) IsUnseen() bool {
	return t == EventNovelPattern
}

// Category returns the top-level hierarchy branch this type belongs
// to, exactly as PLAN_2.md's taxonomy names it — used verbatim in
// Explain() output ("Seen event (...)" / "Unseen event (...)").
func (t EventType) Category() string {
	switch {
	case !t.IsAbnormal():
		return "Normal"
	case t == EventBaselinePattern:
		return "Baseline"
	case t.IsUnseen():
		return "Unseen"
	default:
		return "Seen"
	}
}

// eventDangerWeights is the deterministic severity table Stage
// classification results are worth, BEFORE the lifecycle status
// modifier (statusWeight, below) is applied. NovelPattern is
// deliberately the maximum: unknown is scored as scarier than known,
// because a rule-based system has no way to bound the risk of a
// pattern it has no template for — the opposite assumption (treating
// unrecognized as merely "average") would be optimism this codebase
// has no evidence to support.
var eventDangerWeights = map[EventType]float64{
	EventNormal:         0.0,
	EventSustainedSpike:  0.8,
	EventOscillation:     0.5,
	EventGradualDrift:    0.4,
	EventPlateau:         0.6,
	EventSuddenDrop:      0.7,
	EventBaselinePattern: 0.1,
	EventNovelPattern:    1.0,
}

// EventStatus is an Event's position in its own lifecycle — see the
// state diagram in detector.go's EventDetector.Update. Unlike Status
// (CALM/WATCHING/ALERT, all-caps, decision.go), EventStatus values are
// rendered Title-case: these are two unrelated concepts that happen to
// both describe "how sure are we," and using a visibly different
// naming convention is a small, deliberate signal that they should
// never be confused for each other in a log line.
type EventStatus int

const (
	// EventDetected: the tick that FIRST classified as abnormal. One
	// data point — not yet trusted enough to say more than "something
	// happened."
	EventDetected EventStatus = iota
	// EventConfirmed: the SAME abnormal classification held for
	// confirmTicks consecutive ticks (see detector.go) — no longer a
	// single noisy reading.
	EventConfirmed
	// EventActive: sustained long enough (activeTicks) to be treated as
	// an ongoing situation, not a transient one. This is the status
	// that contributes its full danger weight (statusWeight below).
	EventActive
	// EventDecaying: readings have returned to Normal, but not for long
	// enough yet to call it over. Mirrors DangerScore's own decay
	// philosophy (decision.go) at the event level: de-escalation is
	// gradual, not a light switch.
	EventDecaying
	// EventResolved: back within Normal for cooldownTicks consecutive
	// ticks. Terminal — a Resolved Event is cleared from
	// State.ActiveEvent the same tick it's recorded into EventLog (see
	// agent.go's Milestone 6 integration), so "Resolved" never lingers
	// as the live state of anything.
	EventResolved
)

func (s EventStatus) String() string {
	switch s {
	case EventDetected:
		return "Detected"
	case EventConfirmed:
		return "Confirmed"
	case EventActive:
		return "Active"
	case EventDecaying:
		return "Decaying"
	case EventResolved:
		return "Resolved"
	default:
		return "Unknown"
	}
}

// MarshalJSON mirrors EventType.MarshalJSON above, for the same reason:
// -event-log's dumped JSON should read "Active", not "2".
func (s EventStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// statusWeight is the lifecycle modifier applied on top of
// eventDangerWeights: a Detected SustainedSpike (one noisy tick) should
// not carry the same weight as an Active one (confirmed, ongoing).
// Resolved contributes 0 — a resolved event is history, not a current
// danger, and DangerScore should already be relaxing back via its own
// decay (decision.go) by the time anything reaches this state.
func (s EventStatus) statusWeight() float64 {
	switch s {
	case EventDetected:
		return 0.3
	case EventConfirmed:
		return 0.6
	case EventActive:
		return 1.0
	case EventDecaying:
		return 0.5
	default: // EventResolved, or anything unrecognized
		return 0.0
	}
}

// Event is a live, evolving semantic classification — "what does the
// agent currently believe is happening, and how sure is it." Compare
// this deliberately to Episode (episodic_memory.go): an Event is the
// PRESENT-tense, single, currently-tracked situation (State.ActiveEvent
// holds at most one); an Episode is a PAST-tense, permanent-ish log
// entry an Event leaves behind once it resolves. "Live vs. remembered"
// is the same working-memory/episodic-memory split History and
// EpisodicMemory already drew for raw data, now drawn again one level
// up, for interpreted data.
type Event struct {
	// ID is a process-local, human-inspectable identifier
	// ("<agentID>-evt-<unixnano>") — unique enough to find one event in
	// a log or EventLog, never meant to be globally unique or to cross
	// the network (EventSummary, the wire form, carries no ID at all —
	// see episode_gossip.go's reasoning for why Heartbeat payloads stay
	// narrow).
	ID string

	Type       EventType
	Status     EventStatus
	Confidence float64

	StartTime time.Time // set once, at EventDetected
	LastSeen  time.Time // refreshed every tick this event is still current

	// Features is the most recent FeatureVector that produced this
	// event's current Type/Confidence — kept on the event itself
	// (rather than recomputed at Explain()-time) so Explain() always
	// describes the reasoning that was ACTUALLY used, even after
	// History has moved on.
	Features FeatureVector

	// matches counts consecutive abnormal ticks since StartTime — the
	// counter Detected→Confirmed→Active promotion is measured against.
	matches int
	// normalStreak counts consecutive Normal ticks since Status first
	// became Decaying — the counter Decaying→Resolved is measured
	// against. Reset to 0 the instant the anomaly resumes.
	normalStreak int
}

// DangerWeight is this event's contribution to State.DangerScore (see
// decision.go's Observe / the "2.5 Event-Driven Danger Scoring"
// integration): severity × how far along its lifecycle it is.
func (e Event) DangerWeight() float64 {
	return eventDangerWeights[e.Type] * e.Status.statusWeight()
}

// Duration reports how long this event has been current, measured from
// its first Detected tick.
func (e Event) Duration(now time.Time) time.Duration {
	return now.Sub(e.StartTime)
}

// EventSummary is the network-safe, compact representation of an
// Event — the Milestone 6 analogue of EpisodeDigest (episode_gossip.go)
// and, going back further, of Heartbeat's original Status/DangerScore
// fields (communicator.go): a peer is told the CONCLUSION ("I'm seeing
// a sustained_spike, confidence 0.85, 8s so far"), never the evidence
// (no FeatureVector, no raw Observation, no ID). Objective 3's "what
// should agents communicate?" answered the same way, a third time, for
// a third kind of local knowledge.
type EventSummary struct {
	Type       string        `json:"type"`
	Confidence float64       `json:"confidence"`
	Duration   time.Duration `json:"duration"`
}

// Summary produces the wire-safe digest of this event as of now.
func (e Event) Summary(now time.Time) *EventSummary {
	return &EventSummary{
		Type:       e.Type.String(),
		Confidence: e.Confidence,
		Duration:   e.Duration(now),
	}
}

// Explain returns the rich, event-aware justification the README's
// "explainable behavior" principle calls for — every field a human (or
// a cooperating peer's operator) would need to answer "why does this
// agent think that?" without re-deriving it from raw history.
//
// Two formats, matching the taxonomy split: a Seen event names its
// recognized pattern and the features that mattered for THAT pattern;
// an Unseen event says plainly that nothing matched, and shows the
// raw shape descriptors instead — there's no template name to point to.
func (e Event) Explain(now time.Time) string {
	header := fmt.Sprintf("%s event (%s): %s since %s, confidence %.2f, duration %s.",
		e.Type.Category(), e.Type.String(), e.Status.String(),
		e.StartTime.Format("15:04:05"), e.Confidence, e.Duration(now).Round(time.Second))

	if e.Type.IsUnseen() {
		return header + fmt.Sprintf(
			" No known pattern matched. Features: slope=%.2f, variance=%.2f, zero_crossings=%d.",
			e.Features.Slope, e.Features.Variance, e.Features.ZeroCrossings)
	}
	return header + fmt.Sprintf(
		" Features: slope=%.2f, duration=%d, max_dev=%.2f.",
		e.Features.Slope, e.Features.Duration, e.Features.MaxDeviation)
}

// ── EventLog: bounded history of this agent's OWN events ─────────────
//
// eventLogLimit mirrors historyLimit (state.go) and episodicCapacity
// (episodic_memory.go) — the same bounded-memory philosophy applied a
// third time, now to interpreted events instead of raw samples or
// remembered episodes.
const eventLogLimit = 100

// recordEvent folds one event snapshot into a bounded EventLog. If an
// entry with the same ID already exists (this event was already being
// tracked), it's updated in place — EventLog is a timeline of "the
// current state of each event as of the last time it changed," not a
// tick-by-tick diff. Only once capacity is exceeded does trimming
// happen, and it trims RESOLVED entries first: per the design
// constraint, an event still Active right now must never be the one
// dropped just because the log is full of old, harmless, resolved
// history.
func recordEvent(log []Event, e Event, limit int) []Event {
	for i := range log {
		if log[i].ID == e.ID {
			log[i] = e
			return trimEventLog(log, limit)
		}
	}
	log = append(log, e)
	return trimEventLog(log, limit)
}

func trimEventLog(log []Event, limit int) []Event {
	if len(log) <= limit {
		return log
	}
	// First pass: drop the oldest RESOLVED entries until back at limit
	// or out of resolved entries to drop.
	for i := 0; i < len(log) && len(log) > limit; {
		if log[i].Status == EventResolved {
			log = append(log[:i], log[i+1:]...)
			continue // slice shifted; re-check the same index
		}
		i++
	}
	// Fallback: if capacity is still exceeded (e.g. more than `limit`
	// distinct events are simultaneously non-resolved — not expected in
	// practice, since only one event is ever active per agent, but kept
	// as a hard safety net), trim the oldest overall rather than let
	// the log grow unbounded.
	if len(log) > limit {
		log = log[len(log)-limit:]
	}
	return log
}
