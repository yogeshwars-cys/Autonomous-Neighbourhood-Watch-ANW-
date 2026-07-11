package agent

import (
	"fmt"
	"math"
	"sync"
	"time"
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

	// ── Milestone 6: event-type agreement ─────────────────────────
	//
	// eventAgreementBonus / eventDisagreementPenalty are deliberately
	// LARGER than trustGain / trustLoss: two independent agents
	// classifying the exact same named pattern (both "sustained_spike",
	// say) is much stronger, much less coincidental evidence than their
	// raw DangerScores merely landing close together (which Reinforce,
	// above, already rewards on its own). A numeric score is a single
	// dimension that can agree by chance; a categorical pattern match
	// is not. This is a direct implementation of PLAN_2's Milestone 6
	// requirement: "weight event type agreement higher than raw danger
	// score agreement."
	eventAgreementBonus      = 2 * trustGain
	eventDisagreementPenalty = 2 * trustLoss
)

// ── TrustTable ──────────────────────────────────────────────────────

// TrustTable tracks how much one agent trusts each of its peers.
// It is local knowledge — no two agents share a trust table, and
// trust is never communicated over the network. That asymmetry
// (I trust you differently than you trust me) is deliberate.
type TrustTable struct {
	mu     sync.RWMutex
	scores map[string]float64

	// Objective 7 additions — reputation is more than an instantaneous
	// number. Objective 12 (Reflection) asks "how should trust evolve
	// after experience?" and Objective 9 (Memory) asks what should be
	// forgotten. These three maps give trust an actual HISTORY:
	//
	//   lastInteraction — when a peer was last reinforced at all. Feeds
	//   DecayStale (reputation.go): a relationship nobody has touched
	//   in a while should drift back toward "undecided" rather than
	//   staying frozen at whatever extreme it last reached — the same
	//   "let go of what's stale" idea as EpisodicMemory's Prune, applied
	//   to reputation instead of events.
	//
	//   agreements / disagreements — simple running counts, purely for
	//   EXPLAINABILITY (ReputationTrace). The trust score alone answers
	//   "how much," these answer "why" — a two-sentence audit trail
	//   instead of one opaque float.
	lastInteraction map[string]time.Time
	agreements      map[string]int
	disagreements   map[string]int
}

func NewTrustTable() *TrustTable {
	return &TrustTable{
		scores:          make(map[string]float64),
		lastInteraction: make(map[string]time.Time),
		agreements:      make(map[string]int),
		disagreements:   make(map[string]int),
	}
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

	// Objective 7: record this interaction for reputation history.
	// reputationCrossover is the same break-even point the delta formula
	// above already implies (trustGain/(trustGain+trustLoss)) — below it,
	// this interaction counted as agreement; at or above it, disagreement.
	t.lastInteraction[id] = time.Now()
	if normalizedGap < reputationCrossover {
		t.agreements[id]++
	} else {
		t.disagreements[id]++
	}
}

// ReinforceWithEvent (Milestone 6) does everything Reinforce already
// does (the danger-score-gap adjustment is unchanged and always
// applied first), THEN layers an additional, larger adjustment based on
// whether the peer's reported event TYPE agrees with this agent's own.
//
// Event agreement is only evaluated when BOTH sides report an abnormal
// event ("" or "normal" on either side means one of the two agents
// isn't currently tracking anything abnormal, so there's nothing
// meaningful to compare — silence is not disagreement). When both sides
// do have an opinion, exact agreement is rewarded more than a numeric
// score merely being close (eventAgreementBonus > trustGain), and a
// genuine mismatch — one agent seeing a sustained_spike while another
// sees an oscillation, say — is penalized more than a numeric gap alone
// would be (eventDisagreementPenalty > trustLoss). This is what makes
// event-type evidence STRONGER than raw-score evidence, as PLAN_2's
// Milestone 6 spec requires.
func (t *TrustTable) ReinforceWithEvent(id string, peerDanger, cooperativeScore float64, selfEventType, peerEventType string) {
	t.Reinforce(id, peerDanger, cooperativeScore)

	if selfEventType == "" || peerEventType == "" ||
		selfEventType == EventNormal.String() || peerEventType == EventNormal.String() {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	current, ok := t.scores[id]
	if !ok {
		current = initialTrust
	}
	if selfEventType == peerEventType {
		current += eventAgreementBonus
	} else {
		current -= eventDisagreementPenalty
	}
	t.scores[id] = clamp(current, minTrust, maxTrust)
}

// reputationCrossover is trustGain/(trustGain+trustLoss) — the
// normalized-gap value at which Reinforce's delta is exactly zero.
// Below it a peer is, on net, being rewarded (an "agreement" for
// ReputationTrace's purposes); at or above it, penalized.
const reputationCrossover = trustGain / (trustGain + trustLoss)

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

	// EventType (Milestone 6) is the peer's most recently reported
	// active event type (EventType.String() form, e.g.
	// "sustained_spike"), or "" if the peer reported no active event on
	// its last heartbeat. Optional and additive: every Milestone 1-5
	// call site that builds a PeerSignal without setting this field
	// gets the zero value "", and CooperateWithEvent treats "" exactly
	// like Cooperate always has — no event-type signal, danger-score
	// agreement only.
	EventType string
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
// This was the single entry point the agent called each tick through
// Milestone 5. Signature and behavior are UNCHANGED by Milestone 6 —
// existing callers (and cooperation_test.go's assertions about exact
// trust deltas) keep working exactly as before. It's now implemented as
// CooperateWithEvent with an empty selfEventType, which
// ReinforceWithEvent treats identically to "no event information at
// all" — see that function's doc comment.
func Cooperate(local float64, peers []PeerSignal, trust *TrustTable) float64 {
	return CooperateWithEvent(local, peers, trust, "")
}

// CooperateWithEvent (Milestone 6) is Cooperate plus event-type-aware
// trust reinforcement: selfEventType is THIS agent's own current active
// event type (EventType.String() form, or "" if none/disabled). Agent's
// cooperate() (agent.go) is the one real caller; exported so
// cooperation_test.go / integration_test.go can exercise it directly
// without spinning up a full networked Agent.
func CooperateWithEvent(local float64, peers []PeerSignal, trust *TrustTable, selfEventType string) float64 {
	score := CooperativeDangerScore(local, peers, trust)

	for _, p := range peers {
		if !p.Live {
			continue
		}
		trust.ReinforceWithEvent(p.ID, p.DangerScore, score, selfEventType, p.EventType)
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
