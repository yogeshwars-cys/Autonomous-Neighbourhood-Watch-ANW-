package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ── Taxonomy ──────────────────────────────────────────────────────────

// TestEventHierarchy proves the Normal/Abnormal, Seen/Unseen split PLAN_2's
// Milestone 6 spec requires: Normal is not abnormal, every Seen type is
// abnormal-and-recognized, and NovelPattern is abnormal-and-unrecognized.
func TestEventHierarchy(t *testing.T) {
	if EventNormal.IsAbnormal() {
		t.Error("EventNormal should not be abnormal")
	}
	if EventNormal.IsSeen() || EventNormal.IsUnseen() {
		t.Error("EventNormal should be neither Seen nor Unseen")
	}
	if got := EventNormal.Category(); got != "Normal" {
		t.Errorf("EventNormal.Category() = %q, want %q", got, "Normal")
	}

	seenTypes := []EventType{EventSustainedSpike, EventOscillation, EventGradualDrift, EventPlateau, EventSuddenDrop}
	for _, et := range seenTypes {
		if !et.IsAbnormal() {
			t.Errorf("%s should be abnormal", et)
		}
		if !et.IsSeen() {
			t.Errorf("%s should be Seen", et)
		}
		if et.IsUnseen() {
			t.Errorf("%s should not be Unseen", et)
		}
		if got := et.Category(); got != "Seen" {
			t.Errorf("%s.Category() = %q, want %q", et, got, "Seen")
		}
	}

	if !EventNovelPattern.IsAbnormal() {
		t.Error("NovelPattern should be abnormal")
	}
	if EventNovelPattern.IsSeen() {
		t.Error("NovelPattern should not be Seen")
	}
	if !EventNovelPattern.IsUnseen() {
		t.Error("NovelPattern should be Unseen")
	}
	if got := EventNovelPattern.Category(); got != "Unseen" {
		t.Errorf("NovelPattern.Category() = %q, want %q", got, "Unseen")
	}
}

// TestEventTypeStringIsSnakeCase locks down the wire spelling used both in
// EventSummary.Type (JSON) and PatternTemplate.Name — one canonical name,
// never translated back and forth between a display and a wire form.
func TestEventTypeStringIsSnakeCase(t *testing.T) {
	cases := map[EventType]string{
		EventNormal:         "normal",
		EventSustainedSpike: "sustained_spike",
		EventOscillation:    "oscillation",
		EventGradualDrift:   "gradual_drift",
		EventPlateau:        "plateau",
		EventSuddenDrop:     "sudden_drop",
		EventNovelPattern:   "novel_pattern",
	}
	for et, want := range cases {
		if got := et.String(); got != want {
			t.Errorf("EventType(%d).String() = %q, want %q", int(et), got, want)
		}
	}
}

// ── DangerWeight ─────────────────────────────────────────────────────

// TestDangerScoreFromEvent locks down the two headline numbers PLAN_2's
// Milestone 6 spec calls out explicitly: an ACTIVE SustainedSpike weighs
// 0.8, and an ACTIVE NovelPattern weighs the maximum, 1.0 — "unknown is
// scarier than known."
func TestDangerScoreFromEvent(t *testing.T) {
	spike := Event{Type: EventSustainedSpike, Status: EventActive}
	if got := spike.DangerWeight(); !floatsClose(got, 0.8, 1e-9) {
		t.Errorf("active SustainedSpike DangerWeight() = %v, want 0.8", got)
	}
	novel := Event{Type: EventNovelPattern, Status: EventActive}
	if got := novel.DangerWeight(); !floatsClose(got, 1.0, 1e-9) {
		t.Errorf("active NovelPattern DangerWeight() = %v, want 1.0", got)
	}
}

// TestDangerWeightAppliesStatusModifier checks the full lifecycle
// modifier table (statusWeight, event.go) against every EventStatus, for
// a type whose base weight is well known (SustainedSpike = 0.8).
func TestDangerWeightAppliesStatusModifier(t *testing.T) {
	base := eventDangerWeights[EventSustainedSpike]
	cases := []struct {
		status EventStatus
		factor float64
	}{
		{EventDetected, 0.3},
		{EventConfirmed, 0.6},
		{EventActive, 1.0},
		{EventDecaying, 0.5},
		{EventResolved, 0.0},
	}
	for _, c := range cases {
		e := Event{Type: EventSustainedSpike, Status: c.status}
		want := base * c.factor
		if got := e.DangerWeight(); !floatsClose(got, want, 1e-9) {
			t.Errorf("status=%s: DangerWeight() = %v, want %v", c.status, got, want)
		}
	}
}

// ── EventLog: bounded history ────────────────────────────────────────

// TestEventLogBounded is the direct proof of the design constraint stated
// in MILESTONE6_PROMPT.md section 3: "When full, oldest resolved events
// are dropped first; active events are never dropped." 90 resolved events
// plus 20 active events (110 total, 10 over eventLogLimit) should trim
// down to exactly eventLogLimit entries, and every active one must survive.
func TestEventLogBounded(t *testing.T) {
	var log []Event
	base := time.Unix(1000, 0)

	for i := 0; i < 90; i++ {
		log = recordEvent(log, Event{
			ID: fmt.Sprintf("resolved-%d", i), Type: EventPlateau, Status: EventResolved,
			StartTime: base, LastSeen: base,
		}, eventLogLimit)
	}
	for i := 0; i < 20; i++ {
		log = recordEvent(log, Event{
			ID: fmt.Sprintf("active-%d", i), Type: EventSustainedSpike, Status: EventActive,
			StartTime: base, LastSeen: base,
		}, eventLogLimit)
	}

	if len(log) != eventLogLimit {
		t.Fatalf("expected log bounded at %d, got %d", eventLogLimit, len(log))
	}
	activeCount := 0
	for _, e := range log {
		if e.Status == EventActive {
			activeCount++
		}
	}
	if activeCount != 20 {
		t.Errorf("expected all 20 active events retained (only resolved entries should be trimmed), got %d active", activeCount)
	}
}

// TestEventLogUpdatesInPlaceByID proves EventLog is a timeline of "current
// state of each event as of its last change," not a tick-by-tick diff —
// re-recording the same event ID updates the existing entry rather than
// appending a duplicate.
func TestEventLogUpdatesInPlaceByID(t *testing.T) {
	var log []Event
	base := time.Unix(1000, 0)
	log = recordEvent(log, Event{ID: "evt-1", Type: EventPlateau, Status: EventDetected, StartTime: base, LastSeen: base}, eventLogLimit)
	log = recordEvent(log, Event{ID: "evt-1", Type: EventPlateau, Status: EventConfirmed, StartTime: base, LastSeen: base.Add(time.Second)}, eventLogLimit)

	if len(log) != 1 {
		t.Fatalf("expected the same event ID to update in place, got %d entries", len(log))
	}
	if log[0].Status != EventConfirmed {
		t.Errorf("expected updated status Confirmed, got %s", log[0].Status)
	}
}

// TestEventLogFallsBackToOldestTrimIfAllActive is the safety-net path:
// if (hypothetically) every tracked event were non-resolved, the log
// still must not grow past the limit.
func TestEventLogFallsBackToOldestTrimIfAllActive(t *testing.T) {
	var log []Event
	base := time.Unix(1000, 0)
	for i := 0; i < eventLogLimit+10; i++ {
		log = recordEvent(log, Event{
			ID: fmt.Sprintf("active-%d", i), Type: EventSustainedSpike, Status: EventActive,
			StartTime: base, LastSeen: base,
		}, eventLogLimit)
	}
	if len(log) != eventLogLimit {
		t.Fatalf("expected hard cap at %d even with no resolved entries to drop, got %d", eventLogLimit, len(log))
	}
}

// ── Explainability ───────────────────────────────────────────────────

// TestEventExplainSeenMentionsPatternName checks the "Seen" format from
// MILESTONE6_PROMPT.md section 2.7 verbatim: type, status, confidence,
// duration, and the shape features that justified the classification.
func TestEventExplainSeenMentionsPatternName(t *testing.T) {
	now := time.Unix(2000, 0)
	e := Event{
		Type:       EventSustainedSpike,
		Status:     EventActive,
		Confidence: 0.85,
		StartTime:  now.Add(-8 * time.Second),
		LastSeen:   now,
		Features:   FeatureVector{Slope: 2.3, Duration: 8, MaxDeviation: 4.1},
	}
	got := e.Explain(now)
	for _, want := range []string{"Seen", "sustained_spike", "Active", "0.85", "8s"} {
		if !strings.Contains(got, want) {
			t.Errorf("Explain() = %q, missing %q", got, want)
		}
	}
}

// TestEventExplainUnseenMentionsNoPatternMatched checks the "Unseen"
// format: no template name to point to, so raw shape descriptors are
// shown instead.
func TestEventExplainUnseenMentionsNoPatternMatched(t *testing.T) {
	now := time.Unix(2000, 0)
	e := Event{
		Type:       EventNovelPattern,
		Status:     EventConfirmed,
		Confidence: 1.0,
		StartTime:  now.Add(-3 * time.Second),
		LastSeen:   now,
		Features:   FeatureVector{Slope: 0.1, Variance: 5.2, ZeroCrossings: 0},
	}
	got := e.Explain(now)
	for _, want := range []string{"Unseen", "novel_pattern", "Confirmed", "No known pattern matched"} {
		if !strings.Contains(got, want) {
			t.Errorf("Explain() = %q, missing %q", got, want)
		}
	}
}

// TestEventSummaryOmitsEvidence checks the network-safety boundary
// MILESTONE6_PROMPT.md section 2.6 implies (and event.go's header
// explicitly draws): Summary() carries the conclusion (Type, Confidence,
// Duration) but nothing that would let a peer reconstruct raw readings.
func TestEventSummaryOmitsEvidence(t *testing.T) {
	now := time.Unix(500, 0)
	e := Event{
		Type: EventOscillation, Status: EventActive, Confidence: 0.6,
		StartTime: now.Add(-5 * time.Second), LastSeen: now,
		Features: FeatureVector{Slope: 9.9, Variance: 9.9, MaxDeviation: 9.9},
	}
	sum := e.Summary(now)
	if sum.Type != "oscillation" {
		t.Errorf("Summary().Type = %q, want %q", sum.Type, "oscillation")
	}
	if !floatsClose(sum.Confidence, 0.6, 1e-9) {
		t.Errorf("Summary().Confidence = %v, want 0.6", sum.Confidence)
	}
	if sum.Duration != 5*time.Second {
		t.Errorf("Summary().Duration = %v, want 5s", sum.Duration)
	}
}
