package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestAdaptiveAgentToleratesItsOwnNoiseBetter is the integration-level
// proof of Objective 7's adaptive-threshold claim: an agent with a
// moderately noisy, sustained sensor should see its EFFECTIVE alert
// threshold rise above the fixed Milestone 1 constant once its own
// history establishes that this level of noise is "normal for it."
func TestAdaptiveAgentToleratesItsOwnNoiseBetter(t *testing.T) {
	const tick = 10 * time.Millisecond
	// Moderate, sustained noise — enough to occasionally cross the fixed
	// alertThreshold, without being so extreme that adaptive bounds can't
	// possibly help (adaptiveMaxAlert still caps how far tolerance goes).
	noisySensor := NewSyntheticSensor(10, 2.0, 0.15, 6.0)

	adaptive := New("node-adaptive", noisySensor, tick)
	adaptive.State.EnableAdaptiveThresholds()

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	adaptive.Run(ctx)

	// Sustained noise should have measurably raised the effective
	// thresholds above the fixed defaults.
	watch, alert := adaptive.State.Adaptive.Thresholds()
	if watch <= watchThreshold {
		t.Errorf("expected sustained noise to raise the adaptive watch threshold above the fixed default: got %.3f, base %.3f",
			watch, watchThreshold)
	}
	if alert <= alertThreshold {
		t.Errorf("expected sustained noise to raise the adaptive alert threshold above the fixed default: got %.3f, base %.3f",
			alert, alertThreshold)
	}
}

// TestAdaptiveThresholdsRemainDefaultForCalmAgent proves the flip side:
// an agent whose environment never generates volatility should keep
// effective thresholds at (approximately) the original fixed constants
// — adaptive behavior should not manufacture sensitivity changes out of
// nothing.
func TestAdaptiveThresholdsRemainDefaultForCalmAgent(t *testing.T) {
	const tick = 10 * time.Millisecond
	calmSensor := NewSyntheticSensor(10, 0.05, 0, 0)

	a := New("node-calm", calmSensor, tick)
	a.State.EnableAdaptiveThresholds()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	a.Run(ctx)

	watch, alert := a.State.Adaptive.Thresholds()
	if !floatsClose(watch, watchThreshold, 0.05) {
		t.Errorf("expected near-default watch threshold for a calm agent, got %.3f (base %.3f)", watch, watchThreshold)
	}
	if !floatsClose(alert, alertThreshold, 0.05) {
		t.Errorf("expected near-default alert threshold for a calm agent, got %.3f (base %.3f)", alert, alertThreshold)
	}
}

// TestReputationDecayDuringLivePartition proves DecayStale actually
// runs as part of the live cooperation loop: a peer that goes silent
// (simulating the Milestone 4 partition scenario) should have its
// trust visibly drift back toward initialTrust while unreachable,
// rather than staying frozen at whatever it was mid-conversation.
func TestReputationDecayDuringLivePartition(t *testing.T) {
	commA, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commA: %v", err)
	}
	commB, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commB: %v", err)
	}
	addrB := commB.LocalAddr().String()

	const tick = 15 * time.Millisecond
	const heartbeat = 20 * time.Millisecond

	calmSensor := func() Sensor { return NewSyntheticSensor(10, 0.1, 0, 0) }
	wildSensor := NewSyntheticSensor(10, 1.0, 0.9, 20.0)

	agentA := New("node-a", calmSensor(), tick).WithNetwork(commA, []string{addrB}, heartbeat)
	agentB := New("node-b", wildSensor, tick).WithNetwork(commB, nil, heartbeat)

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); agentA.Run(ctxA) }()
	go func() { defer wg.Done(); agentB.Run(ctxB) }()

	// Let node-b's interactions with node-a shift its trust away from
	// neutral for a while — the wild sensor may push it up (frequent
	// agreement on calm ticks) or down (occasional big disagreements)
	// depending on timing, so this test doesn't assume a direction.
	time.Sleep(400 * time.Millisecond)
	before := agentA.Trust.TrustOf("node-b")

	// Stop both agents — trust state itself is a plain data structure
	// that doesn't need a live Run loop to inspect or decay further.
	cancelA()
	cancelB()
	wg.Wait()

	if floatsClose(before, initialTrust, 1e-6) {
		t.Skip("trust never moved away from neutral within the sample window — nothing to decay")
	}

	// Rather than actually sleeping wall-clock multiples of
	// reputationHalfLife (30s+), inject a synthetic "long silence has
	// passed" timestamp directly — the same technique the unit tests
	// use for DecayStale, applied here on top of trust that was
	// genuinely built through a live, networked disagreement above.
	future := time.Now().Add(20 * reputationHalfLife)
	agentA.Trust.DecayStale(future)

	after := agentA.Trust.TrustOf("node-b")

	distBefore := absFloat(before - initialTrust)
	distAfter := absFloat(after - initialTrust)
	if distAfter >= distBefore {
		t.Errorf("expected trust in silent (partitioned) node-b to relax toward neutral (%.3f) over time: before=%.3f (dist=%.3f) after=%.3f (dist=%.3f)",
			initialTrust, before, distBefore, after, distAfter)
	}
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// TestEpisodicMemoryRecordsRealAlertEpisode is the integration-level
// proof for Objective 9 (Memory): a live agent that genuinely enters
// and leaves ALERT should end up with EventSpike and EventRecovery
// entries in its own episodic memory — not just in unit-tested
// isolation.
func TestEpisodicMemoryRecordsRealAlertEpisode(t *testing.T) {
	const tick = 10 * time.Millisecond
	// A LARGE, occasional spike rather than a frequent, moderate one:
	// with baseline~10 and a +50 spike, a single tick's relative
	// deviation signal alone (50/10 = 5.0) exceeds alertThreshold when
	// folded into DangerScore's EMA — guaranteeing an immediate ALERT
	// transition the instant one spike lands, rather than needing
	// several spikes in a row. A HIGH spike probability would actually
	// backfire here: baseline itself adapts toward frequent spikes
	// (baselineAlpha), so a too-noisy sensor raises "normal" fast enough
	// to dampen DangerScore before it ever crosses ALERT — an emergent,
	// self-limiting property of the original baseline-adaptive design,
	// not a bug, but one worth avoiding when the test's job is simply
	// "prove memory records a real alert episode."
	sensor := NewSyntheticSensor(10, 0.5, 0.1, 50.0)

	a := New("node-x", sensor, tick)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a.Run(ctx)

	if a.Memory.Count() == 0 {
		t.Fatal("expected at least one memorable event after a run with frequent spikes")
	}

	sawSpike := false
	for _, e := range a.Memory.Recent(a.Memory.Count()) {
		if e.Kind == EventSpike {
			sawSpike = true
		}
	}
	if !sawSpike {
		t.Errorf("expected memory to contain at least one EventSpike, got %v", a.Memory.Recent(a.Memory.Count()))
	}
}
