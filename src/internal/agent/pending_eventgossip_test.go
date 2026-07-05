package agent

import (
	"testing"
	"time"
)

func TestSelectForGossipReturnsNilForEmptyMemory(t *testing.T) {
	m := NewEpisodicMemory()
	got := SelectForGossip("node-a", m, time.Now())
	if got != nil {
		t.Fatalf("expected nil for empty memory, got %v", got)
	}
}

func TestSelectForGossipCapsAtMaxGossipEvents(t *testing.T) {
	m := NewEpisodicMemory()
	now := time.Unix(1000, 0)
	for i := 0; i < maxGossipEvents+10; i++ {
		m.Record(Event{Timestamp: now, Kind: EventSpike, DangerScore: 0.9})
	}
	got := SelectForGossip("node-a", m, now)
	if len(got) != maxGossipEvents {
		t.Fatalf("expected selection capped at %d, got %d", maxGossipEvents, len(got))
	}
}

func TestSelectForGossipStampsOriginAndZeroHops(t *testing.T) {
	m := NewEpisodicMemory()
	now := time.Unix(1000, 0)
	m.Record(Event{Timestamp: now, Kind: EventSpike, DangerScore: 0.5})

	got := SelectForGossip("node-x", m, now)
	if len(got) != 1 {
		t.Fatalf("expected 1 digest, got %d", len(got))
	}
	if got[0].OriginID != "node-x" {
		t.Errorf("expected OriginID=node-x, got %q", got[0].OriginID)
	}
	if got[0].Hops != 0 {
		t.Errorf("expected fresh local digest to have Hops=0, got %d", got[0].Hops)
	}
}

func TestRelayCandidatesExcludesSelfOriginatedDigests(t *testing.T) {
	digests := []EventDigest{
		{OriginID: "node-a", Hops: 0},
		{OriginID: "node-b", Hops: 0},
	}
	got := RelayCandidates("node-a", digests)
	if len(got) != 1 || got[0].OriginID != "node-b" {
		t.Fatalf("expected only node-b's digest to be relay-eligible, got %v", got)
	}
}

func TestRelayCandidatesDropsDigestsAtHopLimit(t *testing.T) {
	digests := []EventDigest{
		{OriginID: "node-b", Hops: maxRelayHops},     // at the limit — drop
		{OriginID: "node-c", Hops: maxRelayHops - 1}, // one below — relay
	}
	got := RelayCandidates("node-a", digests)
	if len(got) != 1 || got[0].OriginID != "node-c" {
		t.Fatalf("expected only node-c's digest under the hop limit to survive, got %v", got)
	}
}

func TestRelayCandidatesIncrementsHops(t *testing.T) {
	digests := []EventDigest{{OriginID: "node-b", Hops: 1}}
	got := RelayCandidates("node-a", digests)
	if len(got) != 1 {
		t.Fatalf("expected 1 relay candidate, got %d", len(got))
	}
	if got[0].Hops != 2 {
		t.Errorf("expected Hops incremented from 1 to 2, got %d", got[0].Hops)
	}
}

func TestAppendBoundedTrimsFromFront(t *testing.T) {
	var s []EventDigest
	for i := 0; i < 5; i++ {
		s = appendBounded(s, EventDigest{OriginID: "node-a", Hops: i}, 3)
	}
	if len(s) != 3 {
		t.Fatalf("expected bounded slice of length 3, got %d", len(s))
	}
	// Oldest entries (Hops 0, 1) should have been trimmed; newest (4) kept.
	if s[len(s)-1].Hops != 4 {
		t.Errorf("expected most recent entry retained, got Hops=%d", s[len(s)-1].Hops)
	}
}
