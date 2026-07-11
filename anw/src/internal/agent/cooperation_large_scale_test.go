package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCollectivePictureAssemblesFromGossip is Milestone 5 Extended's core proof:
// a significant event that happens on one agent should become visible
// in ANOTHER agent's independently-assembled PeerEvents/NetworkPicture
// through nothing but selective heartbeat gossip — no central store,
// no direct query, exactly the "emergent global situational awareness
// from local observations and peer-to-peer communication" the README
// opens with.
func TestCollectivePictureAssemblesFromGossip(t *testing.T) {
	commA, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commA: %v", err)
	}
	commB, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commB: %v", err)
	}
	addrA := commA.LocalAddr().String()
	addrB := commB.LocalAddr().String()

	const tick = 10 * time.Millisecond
	const heartbeat = 15 * time.Millisecond

	calmSensor := NewSyntheticSensor(10, 0.1, 0, 0)
	// node-b's own sensor is calm too — what matters for this test is
	// that node-b already has a significant event in EPISODIC MEMORY,
	// seeded directly below, not whether a live sensor spike survives
	// cooperative blending with a calm neighbor (that interaction is
	// already covered by Milestone 5's cooperation tests and Milestone
	// 6's memory tests separately). This keeps the test focused and
	// deterministic: does selective gossip propagate a real event from
	// node-b's memory into node-a's collective picture?
	quietSensor := NewSyntheticSensor(10, 0.1, 0, 0)

	agentA := New("node-a", calmSensor, tick).WithNetwork(commA, []string{addrB}, heartbeat)
	agentB := New("node-b", quietSensor, tick).WithNetwork(commB, []string{addrA}, heartbeat)

	agentB.Memory.Record(Episode{
		Timestamp:   time.Now(),
		Kind:        EpisodeSustained,
		DangerScore: 0.9,
		Note:        "seeded significant event for gossip propagation test",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); agentA.Run(ctx) }()
	go func() { defer wg.Done(); agentB.Run(ctx) }()
	wg.Wait()

	events, heard := agentA.PeerEvents["node-b"]
	if !heard || len(events) == 0 {
		t.Fatal("expected node-a to have heard about at least one of node-b's significant events via gossip")
	}

	picture := agentA.NetworkPicture()
	if !strings.Contains(picture, "node-b") {
		t.Errorf("expected NetworkPicture to mention node-b, got:\n%s", picture)
	}
}

// TestGossipDoesNotFabricateEventsForSilentPeers ensures the collective
// picture stays honest: an agent that never says anything meaningful
// (no ALERT episodes) should not show up with fabricated events in a
// peer's picture — collective intelligence here is assembled ONLY from
// what was actually gossiped, never inferred.
func TestGossipDoesNotFabricateEventsForSilentPeers(t *testing.T) {
	commA, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commA: %v", err)
	}
	commB, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commB: %v", err)
	}
	addrB := commB.LocalAddr().String()

	const tick = 10 * time.Millisecond
	const heartbeat = 15 * time.Millisecond

	calmSensorA := NewSyntheticSensor(10, 0.05, 0, 0)
	calmSensorB := NewSyntheticSensor(10, 0.05, 0, 0)

	agentA := New("node-a", calmSensorA, tick).WithNetwork(commA, []string{addrB}, heartbeat)
	agentB := New("node-b", calmSensorB, tick).WithNetwork(commB, nil, heartbeat)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); agentA.Run(ctx) }()
	go func() { defer wg.Done(); agentB.Run(ctx) }()
	wg.Wait()

	if events := agentA.PeerEvents["node-b"]; len(events) != 0 {
		t.Errorf("expected no fabricated events for a perfectly calm peer, got %v", events)
	}
}

// TestEventDigestRelayTravelsMultipleHops checks the pure relay logic
// end-to-end (not over real sockets, to keep this fast and
// deterministic): a digest fed through RelayCandidates repeatedly
// should survive up to maxRelayHops relays and then stop, demonstrating
// bounded-but-nonzero propagation distance — the mechanism that lets
// Milestone 5 Extended's collective picture reach beyond direct neighbors
// without flooding forever.
func TestEventDigestRelayTravelsMultipleHops(t *testing.T) {
	digest := []EpisodeDigest{{OriginID: "node-origin", Kind: EpisodeSpike, DangerScore: 0.9}}

	relayed := 0
	current := digest
	for i := 0; i < maxRelayHops+5; i++ {
		next := RelayCandidates("node-relay", current)
		if len(next) == 0 {
			break
		}
		relayed++
		current = next
	}

	if relayed == 0 {
		t.Fatal("expected the digest to be relayable at least once")
	}
	if relayed > maxRelayHops {
		t.Errorf("expected relay to stop within maxRelayHops=%d, got %d successful relays", maxRelayHops, relayed)
	}
}
