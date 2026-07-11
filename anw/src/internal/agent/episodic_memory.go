package agent

import (
	"fmt"
	"math"
	"time"
)

// ── Episodic memory (groundwork for Objective 8 — Semantic Perception) ─
//
// State.History (decision.go/state.go) is WORKING MEMORY: every raw
// sample, bounded to historyLimit, no interpretation attached. It answers
// "what did I just see." It is deliberately dumb.
//
// EpisodicMemory below is a second, smaller, smarter memory: not every
// tick, only the ticks that MEANT something. Turning a raw Status
// transition into a labeled Episode was the first, small piece of
// "semantic perception" — before Objective 8 had a real event model to
// build on.
//
// Milestone 6 (event.go, feature.go, pattern.go, detector.go) now
// answers Objective 8's question properly: "what is an event, and how
// can raw sensor values become meaningful knowledge?" gets a real
// taxonomy (Normal/Abnormal, Seen/Unseen), real feature extraction, and
// a real classification lifecycle — that richer, LIVE concept is called
// Event, deliberately a different name from Episode.
//
// The two are not the same idea wearing two names — they're genuinely
// different memories, kept separate on purpose:
//
//   - Event (event.go)    — what's happening RIGHT NOW: a live,
//     evolving classification with its own lifecycle
//     (Detected→Confirmed→Active→Decaying→Resolved). At most one is
//     "current" at a time (State.ActiveEvent).
//   - Episode (this file) — what's WORTH REMEMBERING after the fact:
//     a bounded, importance-ranked log entry. An Event becomes an
//     Episode once it resolves (see Agent.recordMemory's Milestone 6
//     integration in agent.go) — the same way a lived moment only
//     becomes a memory once it's over.
//
// Renaming the original type from "Event" to "Episode" (Milestone 6)
// keeps that distinction visible in the code itself, not just in this
// comment — a reader importing EpisodicMemory should never wonder
// whether it's the same "Event" the detector is actively tracking.

// EpisodeKind labels why an Episode was considered worth remembering.
type EpisodeKind int

const (
	// EpisodeSpike marks the FIRST tick an agent enters StatusAlert from
	// something calmer. One anomalous reading, not yet known to be more
	// than a blip.
	EpisodeSpike EpisodeKind = iota
	// EpisodeSustained marks an alert that has persisted across multiple
	// consecutive ticks (see sustainedTicks in agent.go) — this is no
	// longer "one weird reading," it's a standing situation.
	EpisodeSustained
	// EpisodeRecovery marks the tick an agent leaves StatusAlert and
	// returns to something calmer — the situation resolved.
	EpisodeRecovery
)

func (k EpisodeKind) String() string {
	switch k {
	case EpisodeSpike:
		return "SPIKE"
	case EpisodeSustained:
		return "SUSTAINED"
	case EpisodeRecovery:
		return "RECOVERY"
	default:
		return "UNKNOWN"
	}
}

// Episode is one semantically meaningful thing that happened to THIS
// agent. Deliberately much smaller than the full Observation history:
// no raw sensor value, just the interpretation of it. This is also
// exactly what gossip.go later chooses to share with peers — an agent
// tells the network what it concluded, never what it measured, the
// same boundary Heartbeat already drew for Status/DangerScore.
type Episode struct {
	Timestamp   time.Time
	Kind        EpisodeKind
	DangerScore float64
	Note        string
}

// ── Forgetting ──────────────────────────────────────────────────────
//
// Objective 9 asks two questions with equal weight: "what should an
// agent remember?" AND "what should it forget?" EpisodicMemory answers
// both with two independent, deliberately simple mechanisms rather than
// one clever one:
//
//  1. Hard capacity (episodicCapacity) — a memory that grows forever
//     is not a design, it's a leak. This is the same bounded-history
//     philosophy as state.go's historyLimit, applied one level up.
//  2. Soft importance decay (importance()) — even well within capacity,
//     old and mild events fade faster than severe or recent ones. This
//     is what makes memory "long-term" for the events that matter: a
//     single severe ALERT can outlive a dozen forgotten near-misses.
//
// Both are deterministic functions of elapsed time and severity — no
// learned weights, per the README's "no Machine Learning" non-goal.
const (
	episodicCapacity = 200

	// importanceHalfLife: an event's remembered importance halves every
	// this-many seconds. Chosen so a severe (DangerScore~1.0) event is
	// still clearly "memorable" (importance > forgetThreshold) for
	// several minutes, while a mild event fades within tens of seconds.
	importanceHalfLife = 45 * time.Second

	// forgetThreshold is the importance floor. Once an event's decayed
	// importance drops below this, Prune lets it go even if capacity
	// hasn't been reached yet — "boring and old" is forgotten before
	// "interesting and old."
	forgetThreshold = 0.03

	// minSeverityFloor stops perfectly calm recovery events (DangerScore
	// can legitimately be ~0) from being treated as having *zero*
	// importance and vanishing on the very next Prune — a recovery is
	// still worth remembering for a little while as an explanation of
	// what happened, even though it's good news.
	minSeverityFloor = 0.08
)

// importance combines severity with exponential recency decay:
//
//	importance = max(DangerScore, floor) × 2^(-age / halfLife)
//
// A deliberately simple, fully explainable formula — anyone can
// recompute "why did the agent forget this" by hand.
func importance(e Episode, now time.Time) float64 {
	age := now.Sub(e.Timestamp)
	if age < 0 {
		age = 0
	}
	severity := e.DangerScore
	if severity < minSeverityFloor {
		severity = minSeverityFloor
	}
	decay := math.Exp(-float64(age) / float64(importanceHalfLife) * math.Ln2)
	return severity * decay
}

// EpisodicMemory is an agent's bounded log of semantically meaningful
// events about ITSELF. It is local knowledge exactly like State — no
// two agents share one, and (until gossip.go's selective sharing)
// nothing in here crosses the network at all.
type EpisodicMemory struct {
	events []Episode
}

// NewEpisodicMemory creates an empty episodic memory.
func NewEpisodicMemory() *EpisodicMemory {
	return &EpisodicMemory{}
}

// Record appends a new event and enforces the hard capacity bound.
func (m *EpisodicMemory) Record(e Episode) {
	m.events = append(m.events, e)
	if len(m.events) > episodicCapacity {
		m.events = m.events[len(m.events)-episodicCapacity:]
	}
}

// Prune removes events whose importance has decayed below
// forgetThreshold as of now. Cheap enough to call every tick: it's a
// single pass with no allocation when nothing needs forgetting.
func (m *EpisodicMemory) Prune(now time.Time) {
	if len(m.events) == 0 {
		return
	}
	kept := m.events[:0]
	for _, e := range m.events {
		if importance(e, now) >= forgetThreshold {
			kept = append(kept, e)
		}
	}
	m.events = kept
}

// Count returns how many events are currently remembered.
func (m *EpisodicMemory) Count() int {
	return len(m.events)
}

// Recent returns up to n most recent events, oldest first (same
// ordering convention as State.History).
func (m *EpisodicMemory) Recent(n int) []Episode {
	if n <= 0 || len(m.events) == 0 {
		return nil
	}
	if n > len(m.events) {
		n = len(m.events)
	}
	out := make([]Episode, n)
	copy(out, m.events[len(m.events)-n:])
	return out
}

// TopImportant returns up to n events ranked by CURRENT importance
// (highest first) — used by gossip.go to decide what's worth telling
// peers about. This is deliberately a different ordering than Recent:
// a severe event from two minutes ago may still outrank a trivial one
// from two seconds ago.
func (m *EpisodicMemory) TopImportant(n int, now time.Time) []Episode {
	if n <= 0 || len(m.events) == 0 {
		return nil
	}
	scored := make([]Episode, len(m.events))
	copy(scored, m.events)

	// Simple insertion-sort-by-importance: episodicCapacity is small
	// (200) and n is tiny (a handful), so O(n·len) here is not worth
	// pulling in sort.Slice's indirection for.
	for i := 1; i < len(scored); i++ {
		j := i
		for j > 0 && importance(scored[j], now) > importance(scored[j-1], now) {
			scored[j], scored[j-1] = scored[j-1], scored[j]
			j--
		}
	}

	if n > len(scored) {
		n = len(scored)
	}
	return scored[:n]
}

// Summary returns a short human-readable description of what's in
// memory right now, honoring the README's "explainable behavior"
// design principle.
func (m *EpisodicMemory) Summary(now time.Time) string {
	if len(m.events) == 0 {
		return "no memorable events"
	}
	last := m.events[len(m.events)-1]
	return fmt.Sprintf("%d event(s) remembered, most recent: %s (danger=%.3f, %s ago)",
		len(m.events), last.Kind, last.DangerScore, age(now, last.Timestamp))
}

func age(now, t time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
