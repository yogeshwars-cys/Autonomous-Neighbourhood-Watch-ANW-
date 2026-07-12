package agent

import (
	"testing"
	"time"
)

// ── Welford accuracy ────────────────────────────────────────────────

func TestWelfordStat_BasicAccuracy(t *testing.T) {
	w := &WelfordStat{}
	values := []float64{0.5, 0.6, 0.7, 0.8, 0.9}
	for _, v := range values {
		w.Record(v)
	}

	if w.N != 5 {
		t.Fatalf("N = %d, want 5", w.N)
	}

	// Mean should be 0.7
	wantMean := 0.7
	if diff := w.Mean - wantMean; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("Mean = %f, want %f", w.Mean, wantMean)
	}

	// Population variance of [0.5, 0.6, 0.7, 0.8, 0.9] = 0.02
	wantVar := 0.02
	if diff := w.Variance() - wantVar; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("Variance = %f, want %f", w.Variance(), wantVar)
	}
}

func TestWelfordStat_SingleSample(t *testing.T) {
	w := &WelfordStat{}
	w.Record(0.5)
	if w.Variance() != 0 {
		t.Errorf("Variance with 1 sample should be 0, got %f", w.Variance())
	}
}

// ── Familiarity scoring ─────────────────────────────────────────────

func TestFamiliarity_ZeroWithTooFewEpisodes(t *testing.T) {
	lt := NewLongTermSummary()
	now := time.Now()

	// Record only 2 episodes — below familiarityMinN (5)
	lt.Record(EventSustainedSpike, 0.8, now)
	lt.Record(EventSustainedSpike, 0.85, now.Add(time.Second))

	phi := lt.Familiarity(EventSustainedSpike, 0.82)
	// With N=2 and familiarityMinN=5, confidence ramp = 2/7 ≈ 0.286
	// Phi should be low but not zero (N >= 1)
	if phi > 0.35 {
		t.Errorf("Familiarity with 2 episodes should be low, got %f", phi)
	}
}

func TestFamiliarity_RampsWithExperience(t *testing.T) {
	lt := NewLongTermSummary()
	now := time.Now()

	// Record 20 episodes of the same type at danger ~0.8
	for i := 0; i < 20; i++ {
		lt.Record(EventSustainedSpike, 0.8, now.Add(time.Duration(i)*time.Second))
	}

	phi := lt.Familiarity(EventSustainedSpike, 0.8)
	// With N=20, confidence ramp = 20/25 = 0.80
	// With currentDanger exactly matching mean, severity match ≈ 1.0
	// So phi should be close to 0.80
	if phi < 0.70 {
		t.Errorf("Familiarity with 20 matching episodes should be high, got %f", phi)
	}
}

func TestFamiliarity_DropsForMismatchedSeverity(t *testing.T) {
	lt := NewLongTermSummary()
	now := time.Now()

	// Record 20 episodes of SustainedSpike at danger ~0.3
	for i := 0; i < 20; i++ {
		lt.Record(EventSustainedSpike, 0.3, now.Add(time.Duration(i)*time.Second))
	}

	// Now ask for familiarity with danger 0.9 — very different from mean
	phi := lt.Familiarity(EventSustainedSpike, 0.9)
	// The severity match should be very low because 0.9 is far from mean 0.3
	if phi > 0.30 {
		t.Errorf("Familiarity should drop when severity doesn't match, got %f", phi)
	}
}

func TestFamiliarity_ZeroForUnknownType(t *testing.T) {
	lt := NewLongTermSummary()
	phi := lt.Familiarity(EventNovelPattern, 0.5)
	if phi != 0 {
		t.Errorf("Familiarity for unseen EventType should be 0, got %f", phi)
	}
}

func TestFamiliarity_TypesAreIndependent(t *testing.T) {
	lt := NewLongTermSummary()
	now := time.Now()

	// Record lots of SustainedSpike episodes
	for i := 0; i < 20; i++ {
		lt.Record(EventSustainedSpike, 0.8, now.Add(time.Duration(i)*time.Second))
	}

	// Familiarity for SuddenDrop should be 0 — different type, no data
	phi := lt.Familiarity(EventSuddenDrop, 0.8)
	if phi != 0 {
		t.Errorf("Familiarity for a different EventType should be 0, got %f", phi)
	}
}

// ── Suppression math ────────────────────────────────────────────────

func TestSuppression_CappedAt40Percent(t *testing.T) {
	// Even with perfect familiarity (phi=1.0), the discount can't
	// exceed familiarityMaxDiscount (0.40). That means the danger
	// floor is 60% of the original.
	danger := 1.0
	phi := 1.0 // hypothetical perfect familiarity
	suppressed := danger * (1 - familiarityMaxDiscount*phi)
	if suppressed < 0.59 || suppressed > 0.61 {
		t.Errorf("Max-suppressed danger = %f, want ~0.60", suppressed)
	}
}

// ── Periodic detection ──────────────────────────────────────────────

func TestPeriodicTracker_RegularPeriodDetected(t *testing.T) {
	tracker := NewPeriodicTracker()
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Plant events exactly every 60 seconds
	for i := 0; i < 6; i++ {
		tracker.AddTimestamp(EventSustainedSpike, base.Add(time.Duration(i)*60*time.Second))
	}

	// Check right around when the 7th event would be expected (at +360s)
	expected := base.Add(360 * time.Second)
	if !tracker.IsExpected(EventSustainedSpike, expected) {
		t.Error("Should be expecting periodic event at +360s")
	}
}

func TestPeriodicTracker_IrregularNotDetected(t *testing.T) {
	tracker := NewPeriodicTracker()
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Plant events at wildly irregular intervals
	tracker.AddTimestamp(EventSustainedSpike, base)
	tracker.AddTimestamp(EventSustainedSpike, base.Add(10*time.Second))
	tracker.AddTimestamp(EventSustainedSpike, base.Add(100*time.Second))
	tracker.AddTimestamp(EventSustainedSpike, base.Add(105*time.Second))
	tracker.AddTimestamp(EventSustainedSpike, base.Add(200*time.Second))

	// The next one is unpredictable — should NOT be expected
	if tracker.IsExpected(EventSustainedSpike, base.Add(210*time.Second)) {
		t.Error("Should NOT detect periodic pattern for irregular intervals")
	}
}

func TestPeriodicTracker_TooFewPointsNoDetection(t *testing.T) {
	tracker := NewPeriodicTracker()
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Only 2 events — below periodicMinPoints (4)
	tracker.AddTimestamp(EventSustainedSpike, base)
	tracker.AddTimestamp(EventSustainedSpike, base.Add(60*time.Second))

	if tracker.IsExpected(EventSustainedSpike, base.Add(120*time.Second)) {
		t.Error("Should NOT detect periodic pattern with fewer than 4 data points")
	}
}

func TestPeriodicTracker_NotExpectedOutsideWindow(t *testing.T) {
	tracker := NewPeriodicTracker()
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Plant events exactly every 60 seconds
	for i := 0; i < 6; i++ {
		tracker.AddTimestamp(EventSustainedSpike, base.Add(time.Duration(i)*60*time.Second))
	}

	// Check 45 seconds AFTER the expected next arrival (way outside the ±15s window)
	tooLate := base.Add(405 * time.Second) // expected at 360s, 45s late
	if tracker.IsExpected(EventSustainedSpike, tooLate) {
		t.Error("Should NOT be expected 45s after the predicted arrival for a 60s period")
	}
}

// ── Recall reinforcement ────────────────────────────────────────────

func TestImportance_RecallRefreshesDecay(t *testing.T) {
	now := time.Now()

	// An old episode with no recall — should have decayed significantly
	old := Episode{
		Timestamp:   now.Add(-2 * importanceHalfLife),
		DangerScore: 1.0,
	}
	importanceWithoutRecall := importance(old, now)

	// Same episode, but recalled recently — should be much fresher
	recalled := Episode{
		Timestamp:    now.Add(-2 * importanceHalfLife),
		DangerScore:  1.0,
		LastRecalled: now.Add(-5 * time.Second),
	}
	importanceWithRecall := importance(recalled, now)

	if importanceWithRecall <= importanceWithoutRecall {
		t.Errorf("Recalled episode should have higher importance: recalled=%f, not=%f",
			importanceWithRecall, importanceWithoutRecall)
	}
	if importanceWithRecall < 0.80 {
		t.Errorf("Recently-recalled severe episode should still be very important, got %f",
			importanceWithRecall)
	}
}

// ── Integration: Episode.OriginType is populated ────────────────────

func TestRecordMemory_PopulatesOriginType(t *testing.T) {
	sensor := NewSyntheticSensor(10.0, 0.0, 0.0, 0.0)
	a := New("test", sensor, 50*time.Millisecond)

	// Episode without events should get EventNormal
	now := time.Now()
	a.Memory.Record(Episode{
		Timestamp:  now,
		Kind:       EpisodeSpike,
		OriginType: a.currentEventType(),
	})

	recent := a.Memory.Recent(1)
	if len(recent) != 1 {
		t.Fatal("Expected 1 episode")
	}
	if recent[0].OriginType != EventNormal {
		t.Errorf("OriginType = %v, want EventNormal", recent[0].OriginType)
	}
}

// ── LongTermSummary.Record feeds both stats and tracker ─────────────

func TestLongTermSummary_RecordUpdatesStatsAndTracker(t *testing.T) {
	lt := NewLongTermSummary()
	now := time.Now()

	lt.Record(EventOscillation, 0.5, now)
	lt.Record(EventOscillation, 0.7, now.Add(time.Second))

	w := lt.Stat(EventOscillation)
	if w == nil || w.N != 2 {
		t.Fatalf("Expected 2 records, got %v", w)
	}

	// Tracker should have 2 timestamps
	buf := lt.tracker.buffers[EventOscillation]
	if buf == nil || len(buf.times) != 2 {
		t.Errorf("Tracker should have 2 timestamps")
	}
}
