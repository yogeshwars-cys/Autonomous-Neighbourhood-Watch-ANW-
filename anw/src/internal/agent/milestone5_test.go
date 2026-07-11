package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestCooperativeAlertPropagation is the integration-level proof of
// Milestone 5's headline claim: an agent whose local sensor is calm
// should see its CooperativeDanger rise when a networked peer is
// experiencing anomalies — intelligence emerging from cooperation.
func TestCooperativeAlertPropagation(t *testing.T) {
	commA, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commA: %v", err)
	}
	commB, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commB: %v", err)
	}

	addrB := commB.LocalAddr().String()

	const tick = 20 * time.Millisecond
	const heartbeat = 30 * time.Millisecond

	// Agent A: calm sensor (no spikes)
	calmSensor := NewSyntheticSensor(10, 0.1, 0, 0)
	agentA := New("node-a", calmSensor, tick).WithNetwork(commA, []string{addrB}, heartbeat)

	// Agent B: noisy sensor with HIGH spike probability to drive danger up
	noisySensor := NewSyntheticSensor(10, 1.0, 0.8, 15.0)
	agentB := New("node-b", noisySensor, tick).WithNetwork(commB, nil, heartbeat)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		agentA.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		agentB.Run(ctx)
	}()

	wg.Wait()

	// Agent A's local danger score should be low (calm sensor), but its
	// CooperativeDanger should be elevated by Agent B's high danger signal.
	if agentA.State.CooperativeDanger <= agentA.State.DangerScore {
		t.Errorf("expected cooperative danger (%.3f) > local danger (%.3f) — peer alarm did not propagate",
			agentA.State.CooperativeDanger, agentA.State.DangerScore)
	}

	// Agent B should have high danger AND high cooperative danger.
	if agentB.State.DangerScore < 0.1 {
		t.Errorf("expected agent B to have elevated local danger from noisy sensor, got %.3f",
			agentB.State.DangerScore)
	}
}

// TestTrustErosionFromDisagreement proves that a peer whose reports
// consistently diverge from the cooperative consensus has its trust
// eroded over time.
func TestTrustErosionFromDisagreement(t *testing.T) {
	commA, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commA: %v", err)
	}
	commB, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commB: %v", err)
	}
	commC, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commC: %v", err)
	}

	addrB := commB.LocalAddr().String()
	addrC := commC.LocalAddr().String()

	const tick = 20 * time.Millisecond
	const heartbeat = 30 * time.Millisecond

	// A and C are calm. B is wild (disagreeing with the group).
	calmSensor := func() Sensor { return NewSyntheticSensor(10, 0.1, 0, 0) }
	wildSensor := NewSyntheticSensor(10, 1.0, 0.9, 20.0)

	agentA := New("node-a", calmSensor(), tick).WithNetwork(commA, []string{addrB, addrC}, heartbeat)
	agentB := New("node-b", wildSensor, tick).WithNetwork(commB, nil, heartbeat)
	agentC := New("node-c", calmSensor(), tick).WithNetwork(commC, nil, heartbeat)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for _, a := range []*Agent{agentA, agentB, agentC} {
		wg.Add(1)
		a := a
		go func() {
			defer wg.Done()
			a.Run(ctx)
		}()
	}
	wg.Wait()

	trustB := agentA.Trust.TrustOf("node-b")
	trustC := agentA.Trust.TrustOf("node-c")

	// Agent B's trust should be lower than C's, because B's danger score
	// consistently disagreed with the calm consensus of A and C.
	if trustB >= trustC {
		t.Errorf("expected trust in disagreeing node-b (%.3f) < trust in agreeing node-c (%.3f)",
			trustB, trustC)
	}
}
