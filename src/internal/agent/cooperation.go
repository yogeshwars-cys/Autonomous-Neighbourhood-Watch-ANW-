package agent

import (
	"fmt"
	"math"
	"sync"
)

// ── Tuning constants for cooperation ────────────────────────────────
//
// These are the Milestone 5 equivalents of watchThreshold/alertThreshold
// in decision.go — deliberately simple, deliberately named, and
// deliberately separate from the local-reasoning constants.

const (
	// LocalWeight controls how much an agent trusts its OWN sensor over
	// the collective peer signal. 0.6 means local reasoning still
	// dominates — a peer's alarm can raise suspicion, but can't
	// single-handedly override what the agent sees with its own eyes.
	// This is a real design decision: too low and a single lying peer
	// can hijack the network; too high and cooperation adds nothing.
	LocalWeight = 0.6

	// initialTrust is the starting trust for any newly-seen peer.
	// 0.5 means "I don't know you yet — prove yourself."
	initialTrust = 0.5

	// trustGain / trustLoss are asymmetric on purpose: trust is easier
	// to lose than to earn, matching real-world intuition. A peer that
	// lies once costs more credibility than one agreement earns.
	trustGain = 0.02
	trustLoss = 0.05

	// minTrust prevents a peer from being fully ignored — even the
	// least-trusted peer still contributes a tiny signal, because
	// complete deafness is worse than skeptical listening.
	minTrust = 0.05
	maxTrust = 1.0

	// maxExpectedDangerGap calibrates how sensitive the trust system is.
	// In practice, danger scores rarely exceed 0.3, so a gap of 0.2
	// already represents a significant disagreement. Setting this to
	// the theoretical maximum (1.0) made the system too insensitive —
	// every peer looked like they "agreed" because real gaps never
	// reached the 0.5 crossover point. 0.2 matches the empirical range.
	maxExpectedDangerGap = 0.2
)

// ── TrustTable ──────────────────────────────────────────────────────

// TrustTable tracks how much one agent trusts each of its peers.
// It is local knowledge — no two agents share a trust table, and
// trust is never communicated over the network. That asymmetry
// (I trust you differently than you trust me) is deliberate.
type TrustTable struct {
	mu     sync.RWMutex
	scores map[string]float64
}

func NewTrustTable() *TrustTable {
	return &TrustTable{scores: make(map[string]float64)}
}

// TrustOf returns the current trust score for a peer. Unknown peers
// get initialTrust — not zero, not one, just "undecided."
func (t *TrustTable) TrustOf(id string) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if v, ok := t.scores[id]; ok {
		return v
	}
	return initialTrust
}

// Reinforce adjusts trust based on whether a peer's reported danger
// score agreed with the cooperative consensus. Uses a continuous
// formula where trustGain and trustLoss compete proportionally:
//
//	delta = trustGain×(1 - g) - trustLoss×g
//
// where g is the normalized gap capped at 1.0. The crossover point
// (zero change) is at g = trustGain/(trustGain+trustLoss) ≈ 0.286,
// which is naturally asymmetric because trustLoss > trustGain.
func (t *TrustTable) Reinforce(id string, peerDanger, cooperativeScore float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	current, ok := t.scores[id]
	if !ok {
		current = initialTrust
	}

	gap := math.Abs(peerDanger - cooperativeScore)
	normalizedGap := math.Min(gap/maxExpectedDangerGap, 1.0)

	// Continuous reinforcement: small gaps reward, large gaps penalize.
	// The asymmetry (trustLoss > trustGain) means trust erodes faster
	// than it builds — a deliberate design choice.
	delta := trustGain*(1-normalizedGap) - trustLoss*normalizedGap
	current += delta

	// Clamp to bounds.
	if current < minTrust {
		current = minTrust
	}
	if current > maxTrust {
		current = maxTrust
	}
	t.scores[id] = current
}

// Average returns the mean trust across all tracked peers, or
// initialTrust if no peers have been tracked yet.
func (t *TrustTable) Average() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.scores) == 0 {
		return initialTrust
	}
	sum := 0.0
	for _, v := range t.scores {
		sum += v
	}
	return sum / float64(len(t.scores))
}

// Snapshot returns an independent copy of the trust table. Safe to
// read while the agent is still running.
func (t *TrustTable) Snapshot() map[string]float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]float64, len(t.scores))
	for k, v := range t.scores {
		out[k] = v
	}
	return out
}

// ── PeerSignal ──────────────────────────────────────────────────────

// PeerSignal is the cooperation module's view of one peer — decoupled
// from the Neighbor struct so cooperation logic stays testable without
// needing real heartbeats or UDP sockets.
type PeerSignal struct {
	ID          string
	DangerScore float64
	Live        bool // true if the peer is considered alive (heartbeat within threshold)
}

// ── Cooperative scoring ─────────────────────────────────────────────

// CooperativeDangerScore blends the agent's local danger score with
// trust-weighted peer signals. Dead peers are excluded entirely.
// If no live peers exist, the local score is returned unchanged —
// cooperation degrades gracefully to independent reasoning.
func CooperativeDangerScore(local float64, peers []PeerSignal, trust *TrustTable) float64 {
	var totalWeight float64
	var weightedSum float64

	for _, p := range peers {
		if !p.Live {
			continue
		}
		w := trust.TrustOf(p.ID)
		totalWeight += w
		weightedSum += w * p.DangerScore
	}

	if totalWeight == 0 {
		return local // no live peers — fall back to independent reasoning
	}

	peerSignal := weightedSum / totalWeight
	return LocalWeight*local + (1-LocalWeight)*peerSignal
}

// Cooperate computes the cooperative danger score AND reinforces trust
// for every live peer based on whether they agreed with the result.
// This is the single entry point the agent calls each tick.
func Cooperate(local float64, peers []PeerSignal, trust *TrustTable) float64 {
	score := CooperativeDangerScore(local, peers, trust)

	for _, p := range peers {
		if !p.Live {
			continue
		}
		trust.Reinforce(p.ID, p.DangerScore, score)
	}

	return score
}

// ExplainCooperation returns a human-readable explanation of the
// cooperative decision, following the project's explainability
// principle from README.md.
func ExplainCooperation(local, cooperative float64, peers []PeerSignal, trust *TrustTable) string {
	liveCount := 0
	for _, p := range peers {
		if p.Live {
			liveCount++
		}
	}

	if liveCount == 0 {
		return fmt.Sprintf("coop=%.3f (no live peers, using local=%.3f)", cooperative, local)
	}

	return fmt.Sprintf("coop=%.3f (local=%.3f, %d live peer(s), avg_trust=%.3f)",
		cooperative, local, liveCount, trust.Average())
}
