package agent

import (
	"math"
	"testing"
)

func floatsClose(a, b, eps float64) bool {
	return math.Abs(a-b) <= eps
}

func TestTrustOfDefaultsToInitialTrust(t *testing.T) {
	tt := NewTrustTable()
	got := tt.TrustOf("node-unknown")
	if !floatsClose(got, initialTrust, 1e-9) {
		t.Fatalf("TrustOf(unseen) = %v, want initialTrust (%v)", got, initialTrust)
	}
}

func TestReinforceIncreasesTrustOnAgreement(t *testing.T) {
	tt := NewTrustTable()
	before := tt.TrustOf("node-a")

	// Peer's danger score exactly matches the cooperative score: perfect agreement.
	tt.Reinforce("node-a", 0.5, 0.5)

	after := tt.TrustOf("node-a")
	if after <= before {
		t.Fatalf("trust did not increase on perfect agreement: before=%v after=%v", before, after)
	}
}

func TestReinforceDecreasesTrustOnDisagreement(t *testing.T) {
	tt := NewTrustTable()
	before := tt.TrustOf("node-b")

	// Peer reports maximal danger while the cooperative conclusion is calm: total disagreement.
	tt.Reinforce("node-b", 1.0, 0.0)

	after := tt.TrustOf("node-b")
	if after >= before {
		t.Fatalf("trust did not decrease on total disagreement: before=%v after=%v", before, after)
	}
}

func TestReinforceClampsToBounds(t *testing.T) {
	tt := NewTrustTable()

	// Hammer agreement repeatedly; trust should approach but never exceed maxTrust.
	for i := 0; i < 10000; i++ {
		tt.Reinforce("node-loyal", 0.42, 0.42)
	}
	if got := tt.TrustOf("node-loyal"); got > maxTrust || got <= 0 {
		t.Fatalf("trust out of bounds after repeated agreement: %v", got)
	}

	// Hammer disagreement repeatedly; trust should settle at minTrust, never below.
	for i := 0; i < 10000; i++ {
		tt.Reinforce("node-liar", 1.0, 0.0)
	}
	got := tt.TrustOf("node-liar")
	if got < minTrust || got > maxTrust {
		t.Fatalf("trust out of bounds after repeated disagreement: %v", got)
	}
	if !floatsClose(got, minTrust, 1e-6) {
		t.Fatalf("trust of a consistent liar should settle at minTrust; got %v want %v", got, minTrust)
	}
}

func TestCooperativeDangerScoreNoLivePeersReturnsLocal(t *testing.T) {
	tt := NewTrustTable()
	peers := []PeerSignal{
		{ID: "node-a", DangerScore: 0.9, Live: false},
		{ID: "node-b", DangerScore: 0.8, Live: false},
	}
	got := CooperativeDangerScore(0.1, peers, tt)
	if !floatsClose(got, 0.1, 1e-9) {
		t.Fatalf("with no live peers, cooperative score should equal local score; got %v want %v", got, 0.1)
	}
}

func TestCooperativeDangerScoreMixOfLiveAndDeadPeers(t *testing.T) {
	tt := NewTrustTable()
	peers := []PeerSignal{
		{ID: "node-live", DangerScore: 1.0, Live: true},
		{ID: "node-dead", DangerScore: 1.0, Live: false}, // must be fully ignored
	}
	got := CooperativeDangerScore(0.0, peers, tt)

	want := LocalWeight*0.0 + (1-LocalWeight)*1.0
	if !floatsClose(got, want, 1e-9) {
		t.Fatalf("dead peer influenced the blended score: got %v want %v", got, want)
	}
}

func TestCooperativeDangerScoreBlendsWithEqualTrust(t *testing.T) {
	tt := NewTrustTable()
	// Two live peers, both starting at initialTrust (equal weight), so the
	// peer signal is a plain average of their DangerScores.
	peers := []PeerSignal{
		{ID: "node-a", DangerScore: 1.0, Live: true},
		{ID: "node-b", DangerScore: 0.0, Live: true},
	}
	local := 0.0
	peerAvg := 0.5 // (1.0 + 0.0) / 2, equal trust weights
	want := LocalWeight*local + (1-LocalWeight)*peerAvg

	got := CooperativeDangerScore(local, peers, tt)
	if !floatsClose(got, want, 1e-9) {
		t.Fatalf("CooperativeDangerScore = %v, want %v", got, want)
	}
}

func TestCooperativeDangerScoreWeightsByTrust(t *testing.T) {
	peers := []PeerSignal{
		{ID: "node-a", DangerScore: 1.0, Live: true},
		{ID: "node-b", DangerScore: 0.0, Live: true},
	}

	// Baseline: both peers at equal (initial) trust.
	baseline := CooperativeDangerScore(0.0, peers, NewTrustTable())

	// Skew trust heavily toward node-a (the one reporting HIGH danger) by
	// reinforcing many rounds of agreement with it and disagreement with node-b.
	tt := NewTrustTable()
	for i := 0; i < 50; i++ {
		tt.Reinforce("node-a", 0.5, 0.5) // node-a always agrees with consensus
		tt.Reinforce("node-b", 1.0, 0.0) // node-b always disagrees
	}
	if tt.TrustOf("node-a") <= tt.TrustOf("node-b") {
		t.Fatalf("setup failed: expected node-a more trusted than node-b, got trustA=%v trustB=%v",
			tt.TrustOf("node-a"), tt.TrustOf("node-b"))
	}

	skewed := CooperativeDangerScore(0.0, peers, tt)

	// With node-a (the high-danger reporter) now more trusted, the blend
	// should lean higher than the equal-trust baseline.
	if skewed <= baseline {
		t.Fatalf("trust weighting had no effect: baseline=%v skewed=%v (want skewed > baseline)", baseline, skewed)
	}
}

func TestCooperateReturnsScoreAndUpdatesTrust(t *testing.T) {
	tt := NewTrustTable()
	peers := []PeerSignal{
		{ID: "node-a", DangerScore: 0.0, Live: true},
	}

	before := tt.TrustOf("node-a")
	score := Cooperate(0.0, peers, tt)
	after := tt.TrustOf("node-a")

	if !floatsClose(score, 0.0, 1e-9) {
		t.Fatalf("expected cooperative score of 0 when everyone agrees at 0; got %v", score)
	}
	if after <= before {
		t.Fatalf("Cooperate should have reinforced trust after full agreement: before=%v after=%v", before, after)
	}
}

// TestEmergentPropagationFromSingleAnomalousNeighbor is the unit-level
// version of Milestone 5's headline claim: an agent whose OWN local
// reading is completely calm should still see its cooperative score
// rise when a trusted neighbor is alarmed — intelligence emerging from
// cooperation, not from local sensing.
func TestEmergentPropagationFromSingleAnomalousNeighbor(t *testing.T) {
	tt := NewTrustTable()
	localCalm := 0.0
	peers := []PeerSignal{
		{ID: "node-sick", DangerScore: 0.95, Live: true},
	}

	coop := CooperativeDangerScore(localCalm, peers, tt)
	if coop <= localCalm {
		t.Fatalf("expected an alarmed neighbor to raise the cooperative score above the calm local reading (%v); got %v",
			localCalm, coop)
	}
}

func TestAverageWithNoPeersIsInitialTrust(t *testing.T) {
	tt := NewTrustTable()
	if got := tt.Average(); !floatsClose(got, initialTrust, 1e-9) {
		t.Fatalf("Average() on empty table = %v, want initialTrust (%v)", got, initialTrust)
	}
}

func TestSnapshotIsIndependentCopy(t *testing.T) {
	tt := NewTrustTable()
	tt.Reinforce("node-a", 0.5, 0.5)

	snap := tt.Snapshot()
	snap["node-a"] = 999 // mutating the snapshot must not affect the table

	if got := tt.TrustOf("node-a"); got == 999 {
		t.Fatalf("Snapshot() leaked a live reference into the trust table")
	}
}

func TestExplainCooperationMentionsNoLivePeers(t *testing.T) {
	tt := NewTrustTable()
	peers := []PeerSignal{{ID: "node-a", DangerScore: 0.9, Live: false}}
	got := ExplainCooperation(0.1, 0.1, peers, tt)
	if got == "" {
		t.Fatalf("ExplainCooperation returned an empty string")
	}
}
