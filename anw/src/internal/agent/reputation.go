package agent

import (
	"fmt"
	"math"
	"time"
)

// ── Reputation evolution ────────────────────────────────────────────
//
// Milestone 5's TrustTable already answers "how much do I trust you
// right now, given what you just said." What it does NOT answer —
// flagged explicitly in milestone-5-design.md as a gap for Objective 7
// — is what happens to that number when a peer goes quiet. Without
// this file, a peer that earned minTrust during one noisy episode
// stays at minTrust forever, even if it's been perfectly silent (and
// therefore never wrong about anything) for the last ten minutes.
// That's not reputation, that's a grudge.
//
// DecayStale fixes this the same way EpisodicMemory.Prune fixes stale
// memories: an explicit, deterministic decay toward neutral over time,
// not an learned adjustment.

// reputationHalfLife controls how quickly an unreinforced trust score
// relaxes back toward initialTrust. Chosen to be a few multiples of a
// typical heartbeat interval, so a peer that's merely mid-partition
// (Milestone 4) doesn't lose its earned reputation before it even has
// a chance to reconnect — decay should track "this relationship has
// gone cold," not "this peer missed one heartbeat."
const reputationHalfLife = 30 * time.Second

// DecayStale relaxes every tracked peer's trust score partially back
// toward initialTrust, proportional to how long it's been since that
// peer was last reinforced. A peer reinforced a moment ago is
// essentially untouched (factor≈1); a peer not heard from in several
// half-lives ends up almost exactly at initialTrust again — "prove
// yourself" resets for relationships that have gone quiet, exactly as
// it would for a peer this agent has never met.
//
// Call once per cooperation cycle (see Agent.cooperate) — cheap even
// for large peer counts: one exponential per tracked peer, no
// allocation.
func (t *TrustTable) DecayStale(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for id, last := range t.lastInteraction {
		age := now.Sub(last)
		if age <= 0 {
			continue
		}
		factor := math.Exp(-float64(age) / float64(reputationHalfLife) * math.Ln2)
		current, ok := t.scores[id]
		if !ok {
			continue
		}
		t.scores[id] = current*factor + initialTrust*(1-factor)
	}
}

// ReputationTrace returns a one-line, human-readable explanation of
// why a peer is trusted the amount it is — the README's "explainable
// behavior" principle applied to reputation instead of danger scores.
// Where TrustOf answers "how much," ReputationTrace answers "why":
// how many times the peer agreed vs disagreed with consensus, and how
// long ago this agent last heard from it.
func (t *TrustTable) ReputationTrace(id string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	score, ok := t.scores[id]
	if !ok {
		return fmt.Sprintf("%s: no interactions yet (default trust=%.3f)", id, initialTrust)
	}

	agree := t.agreements[id]
	disagree := t.disagreements[id]
	lastSeen := "never"
	if last, ok := t.lastInteraction[id]; ok {
		lastSeen = trimFloat(time.Since(last).Seconds()) + "s ago"
	}

	return fmt.Sprintf("%s: trust=%.3f (%d agree / %d disagree, last interaction %s)",
		id, score, agree, disagree, lastSeen)
}
