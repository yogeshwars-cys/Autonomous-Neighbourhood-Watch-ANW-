package agent

import (
	"testing"
	"time"
)

func evtAt(kind EventKind, danger float64, secOffset int) Event {
	return Event{
		Timestamp:   time.Unix(int64(secOffset), 0),
		Kind:        kind,
		DangerScore: danger,
	}
}

func TestEpisodicMemoryRecordAndCount(t *testing.T) {
	m := NewEpisodicMemory()
	if m.Count() != 0 {
		t.Fatalf("expected empty memory, got count=%d", m.Count())
	}
	m.Record(evtAt(EventSpike, 0.8, 0))
	m.Record(evtAt(EventRecovery, 0.1, 1))
	if m.Count() != 2 {
		t.Fatalf("expected count=2, got %d", m.Count())
	}
}

func TestEpisodicMemoryCapacityIsBounded(t *testing.T) {
	m := NewEpisodicMemory()
	for i := 0; i < episodicCapacity+50; i++ {
		// Alternate kinds/scores so nothing gets pruned by importance —
		// this test is specifically about the HARD capacity bound.
		m.Record(evtAt(EventSpike, 0.9, i))
	}
	if m.Count() != episodicCapacity {
		t.Fatalf("expected memory capped at %d, got %d", episodicCapacity, m.Count())
	}
}

func TestEpisodicMemoryForgetsLowImportanceOverTime(t *testing.T) {
	m := NewEpisodicMemory()
	base := time.Unix(0, 0)
	// A mild event, far enough in the past that it should have decayed
	// below forgetThreshold by "now".
	m.Record(Event{Timestamp: base, Kind: EventRecovery, DangerScore: 0.1})

	farFuture := base.Add(10 * importanceHalfLife)
	m.Prune(farFuture)

	if m.Count() != 0 {
		t.Fatalf("expected mild old event to be forgotten, but memory still has %d event(s)", m.Count())
	}
}

func TestEpisodicMemoryRemembersSevereEventsLonger(t *testing.T) {
	m := NewEpisodicMemory()
	base := time.Unix(0, 0)
	m.Record(Event{Timestamp: base, Kind: EventSustained, DangerScore: 1.0})

	// Just past one half-life: a severe event should still clear the
	// forget threshold even though a mild one wouldn't.
	soon := base.Add(importanceHalfLife)
	m.Prune(soon)

	if m.Count() != 1 {
		t.Fatalf("expected severe event to survive one half-life, got count=%d", m.Count())
	}
}

func TestEpisodicMemoryRecentOrderingIsOldestFirst(t *testing.T) {
	m := NewEpisodicMemory()
	m.Record(evtAt(EventSpike, 0.5, 0))
	m.Record(evtAt(EventRecovery, 0.1, 1))
	m.Record(evtAt(EventSpike, 0.6, 2))

	recent := m.Recent(2)
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent events, got %d", len(recent))
	}
	if recent[0].Kind != EventRecovery || recent[1].Kind != EventSpike {
		t.Fatalf("unexpected ordering: got %v, %v", recent[0].Kind, recent[1].Kind)
	}
}

func TestEpisodicMemoryTopImportantRanksBySeverityAndRecency(t *testing.T) {
	m := NewEpisodicMemory()
	base := time.Unix(1000, 0)

	// An old, mild event...
	m.Record(Event{Timestamp: base.Add(-30 * time.Second), Kind: EventRecovery, DangerScore: 0.1})
	// ...and a recent, severe one.
	m.Record(Event{Timestamp: base, Kind: EventSustained, DangerScore: 1.0})

	top := m.TopImportant(1, base)
	if len(top) != 1 {
		t.Fatalf("expected 1 result, got %d", len(top))
	}
	if top[0].Kind != EventSustained {
		t.Fatalf("expected the severe, recent event to rank first, got %v", top[0].Kind)
	}
}

func TestEpisodicMemoryTopImportantHandlesFewerThanN(t *testing.T) {
	m := NewEpisodicMemory()
	m.Record(evtAt(EventSpike, 0.5, 0))
	top := m.TopImportant(5, time.Unix(0, 0))
	if len(top) != 1 {
		t.Fatalf("expected TopImportant to clamp to available events, got %d", len(top))
	}
}

func TestEpisodicMemorySummaryMentionsMostRecentEvent(t *testing.T) {
	m := NewEpisodicMemory()
	if got := m.Summary(time.Now()); got != "no memorable events" {
		t.Fatalf("expected empty-memory summary, got %q", got)
	}
	m.Record(evtAt(EventSpike, 0.7, 0))
	got := m.Summary(time.Unix(0, 0))
	if got == "no memorable events" {
		t.Fatalf("expected non-empty summary after recording an event")
	}
}
