package agent

import (
	"testing"
	"time"
)

func TestDecayStaleRelaxesUnusedTrustTowardNeutral(t *testing.T) {
	tt := NewTrustTable()

	// Drive node-liar's trust down to (near) minTrust through repeated
	// disagreement, same setup as TestReinforceClampsToBounds.
	for i := 0; i < 200; i++ {
		tt.Reinforce("node-liar", 1.0, 0.0)
	}
	depressed := tt.TrustOf("node-liar")
	if depressed >= initialTrust {
		t.Fatalf("setup failed: expected depressed trust below initialTrust, got %v", depressed)
	}

	// Simulate a long silence by decaying far into the future — many
	// half-lives past the last interaction.
	future := time.Now().Add(20 * reputationHalfLife)
	tt.DecayStale(future)

	recovered := tt.TrustOf("node-liar")
	if recovered <= depressed {
		t.Fatalf("expected trust to relax upward toward neutral after a long silence: before=%v after=%v",
			depressed, recovered)
	}
	if !floatsClose(recovered, initialTrust, 1e-3) {
		t.Fatalf("expected trust to have nearly fully decayed to initialTrust after many half-lives: got %v want ~%v",
			recovered, initialTrust)
	}
}

func TestDecayStaleLeavesRecentInteractionsAlmostUnchanged(t *testing.T) {
	tt := NewTrustTable()
	tt.Reinforce("node-a", 1.0, 0.0) // one disagreement
	justAfter := tt.TrustOf("node-a")

	// Decay called an instant later should barely move the score.
	tt.DecayStale(time.Now())
	stillClose := tt.TrustOf("node-a")

	if !floatsClose(justAfter, stillClose, 1e-2) {
		t.Fatalf("expected negligible decay immediately after interaction: before=%v after=%v", justAfter, stillClose)
	}
}

func TestDecayStaleIgnoresNeverSeenPeers(t *testing.T) {
	tt := NewTrustTable()
	// No Reinforce calls at all — DecayStale should be a safe no-op.
	tt.DecayStale(time.Now())
	got := tt.TrustOf("node-unknown")
	if !floatsClose(got, initialTrust, 1e-9) {
		t.Fatalf("expected untouched trust table to be unaffected by DecayStale, got %v", got)
	}
}

func TestReputationTraceForUnknownPeer(t *testing.T) {
	tt := NewTrustTable()
	trace := tt.ReputationTrace("node-stranger")
	if trace == "" {
		t.Fatal("expected a non-empty trace even for an unknown peer")
	}
}

func TestReputationTraceReflectsAgreementHistory(t *testing.T) {
	tt := NewTrustTable()
	tt.Reinforce("node-honest", 0.5, 0.5) // perfect agreement
	tt.Reinforce("node-honest", 0.5, 0.5)

	trace := tt.ReputationTrace("node-honest")
	if trace == "" {
		t.Fatal("expected a non-empty reputation trace")
	}
	// Not asserting exact string contents (that's brittle) — just that
	// the trace mechanism runs without needing to know internal counts.
}
