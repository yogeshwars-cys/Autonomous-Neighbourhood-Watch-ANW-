package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNodeFailureDetection(t *testing.T) {
	commA, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commA: %v", err)
	}
	commB, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commB: %v", err)
	}

	addrB := commB.LocalAddr().String()
	quietSensor := func() Sensor { return NewSyntheticSensor(10, 0.1, 0, 0) }

	const tick = 20 * time.Millisecond
	const heartbeat = 30 * time.Millisecond

	agentA := New("node-a", quietSensor(), tick).WithNetwork(commA, []string{addrB}, heartbeat)
	agentB := New("node-b", quietSensor(), tick).WithNetwork(commB, nil, heartbeat)

	agentA.StaleThreshold = 100 * time.Millisecond

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		agentA.Run(ctxA)
	}()
	go func() {
		defer wg.Done()
		agentB.Run(ctxB)
	}()

	// Allow nodes to discover each other and exchange heartbeats
	time.Sleep(150 * time.Millisecond)

	if !agentA.Neighbors.IsAlive("node-b", agentA.StaleThreshold) {
		t.Error("expected node-b to be alive initially")
	}

	// Kill agentB to simulate node failure
	cancelB()

	// Wait for agentA's stale threshold to elapse
	time.Sleep(200 * time.Millisecond)

	if agentA.Neighbors.IsAlive("node-b", agentA.StaleThreshold) {
		t.Error("expected node-b to be detected as unreachable/dead after stale threshold")
	}

	dead := agentA.Neighbors.DeadPeers(agentA.StaleThreshold)
	found := false
	for _, id := range dead {
		if id == "node-b" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected node-b to be in dead peers list, got %v", dead)
	}

	cancelA()
	wg.Wait()
}

func TestCommunicationFailureDetection(t *testing.T) {
	commA, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commA: %v", err)
	}
	commB, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commB: %v", err)
	}

	addrB := commB.LocalAddr().String()
	quietSensor := func() Sensor { return NewSyntheticSensor(10, 0.1, 0, 0) }

	const tick = 20 * time.Millisecond
	const heartbeat = 30 * time.Millisecond

	agentA := New("node-a", quietSensor(), tick).WithNetwork(commA, []string{addrB}, heartbeat)
	agentB := New("node-b", quietSensor(), tick).WithNetwork(commB, nil, heartbeat)

	agentA.StaleThreshold = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
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

	// Let them connect
	time.Sleep(150 * time.Millisecond)

	if !agentA.Neighbors.IsAlive("node-b", agentA.StaleThreshold) {
		t.Error("expected node-b to be alive initially")
	}

	// Close communicator of B directly to simulate a network partition or socket failure
	commB.conn.Close()

	// Wait for failure detection
	time.Sleep(200 * time.Millisecond)

	if agentA.Neighbors.IsAlive("node-b", agentA.StaleThreshold) {
		t.Error("expected node-b to be detected as unreachable after communicator socket closure")
	}

	cancel()
	wg.Wait()
}

func TestAutomaticRecovery(t *testing.T) {
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
	quietSensor := func() Sensor { return NewSyntheticSensor(10, 0.1, 0, 0) }

	const tick = 20 * time.Millisecond
	const heartbeat = 30 * time.Millisecond

	agentA := New("node-a", quietSensor(), tick).WithNetwork(commA, []string{addrB}, heartbeat)
	agentB := New("node-b", quietSensor(), tick).WithNetwork(commB, nil, heartbeat)

	agentA.StaleThreshold = 100 * time.Millisecond

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB1, cancelB1 := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB1()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		agentA.Run(ctxA)
	}()
	go func() {
		defer wg.Done()
		agentB.Run(ctxB1)
	}()

	// Exchange initial heartbeats
	time.Sleep(150 * time.Millisecond)
	if !agentA.Neighbors.IsAlive("node-b", agentA.StaleThreshold) {
		t.Error("expected node-b to be alive initially")
	}

	// Kill agent B1
	cancelB1()

	// Wait until A registers it as dead
	time.Sleep(200 * time.Millisecond)
	if agentA.Neighbors.IsAlive("node-b", agentA.StaleThreshold) {
		t.Error("expected node-b to be dead")
	}

	// Start B2 representing node-b restarting (possibly on a new port, seeded to A)
	commB2, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commB2: %v", err)
	}
	agentB2 := New("node-b", quietSensor(), tick).WithNetwork(commB2, []string{addrA}, heartbeat)

	ctxB2, cancelB2 := context.WithCancel(context.Background())
	defer cancelB2()

	wg.Add(1)
	go func() {
		defer wg.Done()
		agentB2.Run(ctxB2)
	}()

	// Give it some time to send heartbeats and recover
	time.Sleep(150 * time.Millisecond)

	if !agentA.Neighbors.IsAlive("node-b", agentA.StaleThreshold) {
		t.Error("expected node-a to automatically recover and mark node-b alive again")
	}

	// Check it is removed from dead peers
	dead := agentA.Neighbors.DeadPeers(agentA.StaleThreshold)
	for _, id := range dead {
		if id == "node-b" {
			t.Error("expected node-b to be removed from dead peers list")
		}
	}

	cancelA()
	cancelB2()
	wg.Wait()
}

func TestPartialNetworkFailure(t *testing.T) {
	// Chain topology: A -> B -> C
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

	const tick = 20 * time.Millisecond
	const heartbeat = 30 * time.Millisecond

	agentA := New("node-a", quietSensor(), tick).WithNetwork(commA, []string{addrB}, heartbeat)
	agentB := New("node-b", quietSensor(), tick).WithNetwork(commB, []string{addrC}, heartbeat)
	agentC := New("node-c", quietSensor(), tick).WithNetwork(commC, nil, heartbeat)

	agentA.StaleThreshold = 150 * time.Millisecond
	agentC.StaleThreshold = 150 * time.Millisecond

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	ctxC, cancelC := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()
	defer cancelC()

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		agentA.Run(ctxA)
	}()
	go func() {
		defer wg.Done()
		agentB.Run(ctxB)
	}()
	go func() {
		defer wg.Done()
		agentC.Run(ctxC)
	}()

	// Wait for convergence of the chain topology
	time.Sleep(300 * time.Millisecond)

	// A should know about both B and C, and both should be alive
	if !agentA.Neighbors.IsAlive("node-b", agentA.StaleThreshold) {
		t.Error("expected node-b to be alive")
	}
	if !agentA.Neighbors.IsAlive("node-c", agentA.StaleThreshold) {
		t.Error("expected node-c to be alive via gossip-discovered connection")
	}

	// Kill middle node B
	cancelB()

	// Wait for stale threshold
	time.Sleep(250 * time.Millisecond)

	// A should detect B as dead, but C should remain alive since C has A's address
	// via gossip and they communicate directly now
	if agentA.Neighbors.IsAlive("node-b", agentA.StaleThreshold) {
		t.Error("expected node-b to be detected as unreachable/dead")
	}
	if !agentA.Neighbors.IsAlive("node-c", agentA.StaleThreshold) {
		t.Error("expected node-c to remain alive and direct communication to survive B's failure")
	}

	cancelA()
	cancelC()
	wg.Wait()
}
