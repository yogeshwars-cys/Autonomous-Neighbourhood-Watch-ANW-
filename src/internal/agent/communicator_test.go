package agent

import (
	"context"
	"testing"
	"time"
)

func TestUDPCommunicatorSendAndReceive(t *testing.T) {
	commA, err := NewUDPCommunicator("127.0.0.1:0") // :0 = let the OS pick a free port
	if err != nil {
		t.Fatalf("failed to start commA: %v", err)
	}
	commB, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start commB: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	incoming := commB.Listen(ctx)

	sent := Heartbeat{
		ID:          "node-a",
		Status:      "CALM",
		DangerScore: 0.073,
		Timestamp:   time.Now(),
	}
	if err := commA.Send(commB.LocalAddr().String(), sent); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	select {
	case got := <-incoming:
		if got.ID != sent.ID {
			t.Errorf("expected ID %q, got %q", sent.ID, got.ID)
		}
		if got.Status != sent.Status {
			t.Errorf("expected status %q, got %q", sent.Status, got.Status)
		}
		if got.DangerScore != sent.DangerScore {
			t.Errorf("expected danger score %.3f, got %.3f", sent.DangerScore, got.DangerScore)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for heartbeat over real UDP socket")
	}
}

func TestUDPCommunicatorDropsMalformedPackets(t *testing.T) {
	// Sends a raw garbage UDP packet directly (bypassing Send, which
	// always produces valid JSON) to confirm Listen survives a
	// malicious or corrupted peer instead of crashing or getting stuck.
	comm, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start comm: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	incoming := comm.Listen(ctx)

	sender, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start sender: %v", err)
	}

	if err := sender.sendRaw(comm.LocalAddr().String(), []byte("not valid json")); err != nil {
		t.Fatalf("failed to send junk: %v", err)
	}

	// A real heartbeat sent right after the junk should still arrive,
	// proving the bad packet was dropped rather than wedging the listener.
	good := Heartbeat{ID: "node-x", Status: "CALM", Timestamp: time.Now()}
	if err := sender.Send(comm.LocalAddr().String(), good); err != nil {
		t.Fatalf("failed to send good heartbeat: %v", err)
	}

	select {
	case got := <-incoming:
		if got.ID != "node-x" {
			t.Errorf("expected to recover after junk packet, got ID %q", got.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listener appears stuck after malformed packet")
	}
}

func TestNeighborListTracksLatestHeartbeat(t *testing.T) {
	nl := NewNeighborList()
	nl.Update(Heartbeat{ID: "node-b", Status: "CALM", DangerScore: 0.1, Timestamp: time.Unix(100, 0)})
	nl.Update(Heartbeat{ID: "node-b", Status: "WATCHING", DangerScore: 0.4, Timestamp: time.Unix(200, 0)})

	all := nl.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 neighbor, got %d", len(all))
	}
	if all[0].Status != "WATCHING" {
		t.Errorf("expected latest status WATCHING, got %s", all[0].Status)
	}
	if all[0].DangerScore != 0.4 {
		t.Errorf("expected latest danger score 0.4, got %.3f", all[0].DangerScore)
	}
}

func TestNeighborListTracksMultiplePeersIndependently(t *testing.T) {
	nl := NewNeighborList()
	nl.Update(Heartbeat{ID: "node-b", Status: "CALM", Timestamp: time.Unix(1, 0)})
	nl.Update(Heartbeat{ID: "node-c", Status: "ALERT", Timestamp: time.Unix(1, 0)})

	if len(nl.All()) != 2 {
		t.Fatalf("expected 2 distinct neighbors, got %d", len(nl.All()))
	}

	nb, ok := nl.Get("node-c")
	if !ok {
		t.Fatal("expected to find node-c")
	}
	if nb.Status != "ALERT" {
		t.Errorf("expected node-c status ALERT, got %s", nb.Status)
	}
}
