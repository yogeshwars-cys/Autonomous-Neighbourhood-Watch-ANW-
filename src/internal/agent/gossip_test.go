package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestGossipDiscoversTransitivePeer is Milestone 3's centerpiece proof.
//
// Topology is a deliberately OPEN chain, not a ring:
//
//	node-a --(seed)--> node-b --(seed)--> node-c
//
// node-c has NO seed of its own, and node-a is never told node-c's
// address anywhere. If node-a ends up with node-c in its AddressBook,
// that knowledge could only have arrived via gossip relayed through
// node-b — which is exactly the property Milestone 3 needs to
// demonstrate: a sparse, linear bootstrap converging into a connected
// network on its own.
//
// (A closed ring — c seeded back to a — would NOT prove this, since
// node-a would then learn node-c's address from direct contact alone,
// with gossip contributing nothing observable.)
func TestGossipDiscoversTransitivePeer(t *testing.T) {
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

	quietSensor := func() Sensor { return NewSyntheticSensor(10, 0.1, 0, 0) }

	const tick = 30 * time.Millisecond
	const heartbeat = 50 * time.Millisecond

	agentA := New("node-a", quietSensor(), tick).WithNetwork(commA, []string{addrB}, heartbeat)
	agentB := New("node-b", quietSensor(), tick).WithNetwork(commB, []string{addrC}, heartbeat)
	agentC := New("node-c", quietSensor(), tick).WithNetwork(commC, nil, heartbeat) // no seed at all

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
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
	wg.Wait() // safe to read AddressBook below: no Run goroutine is touching it anymore

	addr, ok := agentA.AddressBook.Get("node-c")
	if !ok {
		t.Fatal("expected node-a to discover node-c transitively via node-b's gossip within 3s, but it never did")
	}
	if addr != addrC {
		t.Errorf("node-a's discovered address for node-c is wrong: got %q, want %q", addr, addrC)
	}
}
