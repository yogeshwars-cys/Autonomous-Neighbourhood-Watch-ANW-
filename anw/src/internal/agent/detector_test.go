package agent

import (
	"testing"
	"time"
)

// histFrom builds an Observation slice from raw values, one tick apart,
// starting at Unix 0 — a deterministic fixture for feature/pattern tests
// that never depends on wall-clock time.
func histFrom(values []float64) []Observation {
	obs := make([]Observation, len(values))
	for i, v := range values {
		obs[i] = Observation{Timestamp: time.Unix(int64(i), 0), Value: v}
	}
	return obs
}

// ── Feature extraction ────────────────────────────────────────────────

// TestFeatureExtraction checks every FeatureVector field against
// hand-verified expected values for a known, fixed history: a modest,
// steady rise from baseline (100 -> 160 over 10 ticks). Values below are
// cross-checked against an independent reference computation of the same
// relative-signal formula decision.go uses.
func TestFeatureExtraction(t *testing.T) {
	baseline := 100.0
	values := []float64{100, 100, 105, 112, 120, 128, 136, 144, 152, 160}
	fv := ExtractFeatures(histFrom(values), baseline)

	const eps = 0.01
	if !floatsClose(fv.Delta, 0.08, eps) {
		t.Errorf("Delta = %v, want ~0.08", fv.Delta)
	}
	if !floatsClose(fv.Slope, 0.0714, eps) {
		t.Errorf("Slope = %v, want ~0.0714", fv.Slope)
	}
	if !floatsClose(fv.Variance, 0.207, eps) {
		t.Errorf("Variance = %v, want ~0.207", fv.Variance)
	}
	if fv.ZeroCrossings != 0 {
		t.Errorf("ZeroCrossings = %d, want 0 (monotonically rising, never below baseline)", fv.ZeroCrossings)
	}
	if !floatsClose(fv.MaxDeviation, 0.6, eps) {
		t.Errorf("MaxDeviation = %v, want ~0.6", fv.MaxDeviation)
	}
	if fv.Duration != 4 {
		t.Errorf("Duration = %d, want 4 (trailing ticks with |signal| > watchThreshold=0.30)", fv.Duration)
	}
	if !fv.IsRising {
		t.Error("expected IsRising for a steady upward climb")
	}
	if fv.IsFalling || fv.IsStable {
		t.Error("a steadily rising series should be neither IsFalling nor IsStable")
	}
}

// TestFeatureExtractionHandlesShortHistory checks the degrade-gracefully
// path: fewer than 2 observations has no defined slope/variance/delta,
// and ExtractFeatures must return zero values rather than panicking on a
// division by zero.
func TestFeatureExtractionHandlesShortHistory(t *testing.T) {
	fv := ExtractFeatures(nil, 100)
	if !fv.IsStable {
		t.Error("empty history should report IsStable (nothing is happening)")
	}

	fv = ExtractFeatures(histFrom([]float64{100}), 100)
	if fv.Slope != 0 || fv.Delta != 0 {
		t.Errorf("single-observation history should have zero Delta/Slope, got %+v", fv)
	}
}

// TestFeatureExtractionWindowIsBoundedToFeatureWindow checks that a much
// longer history than featureWindow only ever looks at the trailing
// featureWindow entries — old data outside the window must not leak in.
func TestFeatureExtractionWindowIsBoundedToFeatureWindow(t *testing.T) {
	values := make([]float64, 0, featureWindow+40)
	// A long, wildly noisy prefix far outside the window...
	for i := 0; i < 40; i++ {
		v := 100.0
		if i%2 == 0 {
			v = 1000.0
		}
		values = append(values, v)
	}
	// ...followed by featureWindow ticks of a clean, flat plateau.
	for i := 0; i < featureWindow; i++ {
		values = append(values, 145.0)
	}

	fv := ExtractFeatures(histFrom(values), 100)
	if fv.ZeroCrossings != 0 {
		t.Errorf("ZeroCrossings = %d, want 0 — the noisy prefix outside featureWindow should not be visible", fv.ZeroCrossings)
	}
	if !fv.IsStable {
		t.Error("expected IsStable — only the flat trailing window should matter")
	}
}

// ── Pattern templates (Stage B) ─────────────────────────────────────

// TestPatternSustainedSpike: a steep, sustained rise well past
// alertThreshold must match SustainedSpike.
func TestPatternSustainedSpike(t *testing.T) {
	values := []float64{100, 100, 100, 100, 130, 160, 190, 220, 250, 280}
	fv := ExtractFeatures(histFrom(values), 100)

	typ, conf, ok := DefaultPatternLibrary().Match(fv)
	if !ok || typ != EventSustainedSpike {
		t.Fatalf("expected SustainedSpike match, got type=%s ok=%v (fv=%+v)", typ, ok, fv)
	}
	if !floatsClose(conf, 0.7, 1e-9) {
		t.Errorf("expected MinConfidence 0.7, got %v", conf)
	}
}

// TestPatternOscillation: a signal repeatedly crossing baseline must
// match Oscillation, not SustainedSpike (its Slope is far too shallow to
// pass spikeSteepThreshold) or GradualDrift (too few ZeroCrossings would
// be required, and it fails on ZeroCrossings anyway for those to matter).
func TestPatternOscillation(t *testing.T) {
	values := []float64{100, 140, 60, 140, 60, 140, 60, 140, 60, 140}
	fv := ExtractFeatures(histFrom(values), 100)

	typ, conf, ok := DefaultPatternLibrary().Match(fv)
	if !ok || typ != EventOscillation {
		t.Fatalf("expected Oscillation match, got type=%s ok=%v (fv=%+v)", typ, ok, fv)
	}
	if !floatsClose(conf, 0.6, 1e-9) {
		t.Errorf("expected MinConfidence 0.6, got %v", conf)
	}
}

// TestPatternGradualDrift: a slow, steady climb — steeper than noise,
// shallower than a spike, sustained long enough to rule out a transient
// move — must match GradualDrift.
func TestPatternGradualDrift(t *testing.T) {
	values := []float64{100, 100, 100, 132, 136, 140, 144, 148, 152, 156}
	fv := ExtractFeatures(histFrom(values), 100)

	typ, conf, ok := DefaultPatternLibrary().Match(fv)
	if !ok || typ != EventGradualDrift {
		t.Fatalf("expected GradualDrift match, got type=%s ok=%v (fv=%+v)", typ, ok, fv)
	}
	if !floatsClose(conf, 0.5, 1e-9) {
		t.Errorf("expected MinConfidence 0.5, got %v", conf)
	}
}

// TestPatternPlateau: a value that jumps once, then sits perfectly flat
// (well away from any slope at all) must match Plateau, not GradualDrift
// — a genuinely zero slope is what separates the two.
func TestPatternPlateau(t *testing.T) {
	values := []float64{145, 145, 145, 145, 145, 145, 145, 145, 145, 145}
	fv := ExtractFeatures(histFrom(values), 100)

	typ, conf, ok := DefaultPatternLibrary().Match(fv)
	if !ok || typ != EventPlateau {
		t.Fatalf("expected Plateau match, got type=%s ok=%v (fv=%+v)", typ, ok, fv)
	}
	if !floatsClose(conf, 0.6, 1e-9) {
		t.Errorf("expected MinConfidence 0.6, got %v", conf)
	}
}

// TestPatternSuddenDrop: a sharp single-tick fall must match SuddenDrop.
func TestPatternSuddenDrop(t *testing.T) {
	values := []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 40}
	fv := ExtractFeatures(histFrom(values), 100)

	typ, conf, ok := DefaultPatternLibrary().Match(fv)
	if !ok || typ != EventSuddenDrop {
		t.Fatalf("expected SuddenDrop match, got type=%s ok=%v (fv=%+v)", typ, ok, fv)
	}
	if !floatsClose(conf, 0.7, 1e-9) {
		t.Errorf("expected MinConfidence 0.7, got %v", conf)
	}
}

// TestPatternNovel: an abnormal reading whose shape matches NONE of the
// five templates must fall through to NovelPattern (Unseen), with
// maximum confidence — the detector is certain it doesn't recognize this.
// The history is long enough to exceed novelMinDuration so that
// duration gating (Option C) doesn't suppress it.
func TestPatternNovel(t *testing.T) {
	// A gently rising signal: too slow for SustainedSpike (slope <
	// spikeSteepThreshold=0.15, MaxDeviation < alertThreshold=0.70),
	// not oscillating (zero crossings = 0), not stable (IsRising =
	// true, so not Plateau), not a drop, and too short a Duration for
	// GradualDrift (which needs longPatternDuration=6). Duration is
	// >= novelMinDuration so the duration gate doesn't suppress it.
	values := []float64{100, 100, 100, 100, 100, 140, 145, 150, 155}
	fv := ExtractFeatures(histFrom(values), 100)

	_, _, ok := DefaultPatternLibrary().Match(fv)
	if ok {
		t.Fatalf("expected no template to recognize this shape (fv=%+v)", fv)
	}

	d := NewEventDetector()
	signal := relativeSignal(155, 100)
	typ, conf := d.Classify(signal, fv, false)
	if typ != EventNovelPattern {
		t.Errorf("expected NovelPattern classification, got %s (fv=%+v)", typ, fv)
	}
	if !floatsClose(conf, 1.0, 1e-9) {
		t.Errorf("expected confidence 1.0 for a NovelPattern call, got %v", conf)
	}
}

// TestClassifyStageANormalBelowWatchThreshold checks Stage A in
// isolation: a small signal never even reaches pattern matching.
func TestClassifyStageANormalBelowWatchThreshold(t *testing.T) {
	d := NewEventDetector()
	typ, conf := d.Classify(0.1, FeatureVector{}, false) // well below watchThreshold=0.30
	if typ != EventNormal {
		t.Errorf("expected Normal classification for a small signal, got %s", typ)
	}
	if !floatsClose(conf, 1.0, 1e-9) {
		t.Errorf("expected confidence 1.0 for a confident Normal call, got %v", conf)
	}
}

// ── Lifecycle (EventDetector.Update) ─────────────────────────────────

// TestEventLifecycle drives a full Detected -> Confirmed -> Active ->
// Decaying -> Active (anomaly resumes) -> Decaying -> Resolved traversal,
// checking every PLAN_2-specified transition along the way.
func TestEventLifecycle(t *testing.T) {
	d := NewEventDetector()
	now := time.Unix(0, 0)
	tick := func() time.Time { now = now.Add(time.Second); return now }

	var ev *Event

	ev = d.Update(ev, "node-x", EventSustainedSpike, 0.7, FeatureVector{}, tick())
	if ev == nil || ev.Status != EventDetected {
		t.Fatalf("expected Detected after the first abnormal tick, got %+v", ev)
	}
	if ev.StartTime != now {
		t.Errorf("expected StartTime set to the first abnormal tick, got %v want %v", ev.StartTime, now)
	}

	ev = d.Update(ev, "node-x", EventSustainedSpike, 0.7, FeatureVector{}, tick())
	if ev.Status != EventConfirmed {
		t.Fatalf("expected Confirmed at confirmTicks=%d, got %s", confirmTicks, ev.Status)
	}

	for i := 0; i < activeTicks-confirmTicks; i++ {
		ev = d.Update(ev, "node-x", EventSustainedSpike, 0.7, FeatureVector{}, tick())
	}
	if ev.Status != EventActive {
		t.Fatalf("expected Active at activeTicks=%d, got %s", activeTicks, ev.Status)
	}

	ev = d.Update(ev, "node-x", EventNormal, 1.0, FeatureVector{}, tick())
	if ev.Status != EventDecaying {
		t.Fatalf("expected Decaying once a reading returns to Normal, got %s", ev.Status)
	}

	ev = d.Update(ev, "node-x", EventSustainedSpike, 0.7, FeatureVector{}, tick())
	if ev.Status != EventActive {
		t.Fatalf("expected Active again once the anomaly resumes before cooldown completed, got %s", ev.Status)
	}

	ev = d.Update(ev, "node-x", EventNormal, 1.0, FeatureVector{}, tick())
	for i := 0; i < cooldownTicks-1; i++ {
		ev = d.Update(ev, "node-x", EventNormal, 1.0, FeatureVector{}, tick())
	}
	if ev.Status != EventResolved {
		t.Fatalf("expected Resolved after %d consecutive Normal ticks, got %s", cooldownTicks, ev.Status)
	}
}

// TestEventDetectorUpdateReturnsNilWhenNothingToTrack checks the "no
// event" representation: nil in, Normal tick, nil out — never a
// synthetic EventNormal Event.
func TestEventDetectorUpdateReturnsNilWhenNothingToTrack(t *testing.T) {
	d := NewEventDetector()
	got := d.Update(nil, "node-x", EventNormal, 1.0, FeatureVector{}, time.Now())
	if got != nil {
		t.Fatalf("expected nil when there is no existing event and this tick is Normal, got %+v", got)
	}
}

// TestEventDetectorUpdateAssignsAgentScopedID checks nextEventID's shape:
// human-inspectable, and scoped to the agent that produced it.
func TestEventDetectorUpdateAssignsAgentScopedID(t *testing.T) {
	d := NewEventDetector()
	ev := d.Update(nil, "node-x", EventPlateau, 0.6, FeatureVector{}, time.Unix(42, 0))
	if ev == nil {
		t.Fatal("expected a new event to be created")
	}
	if got, want := ev.ID[:len("node-x-evt-")], "node-x-evt-"; got != want {
		t.Errorf("event ID = %q, expected prefix %q", ev.ID, want)
	}
}

// ── Learning phase ───────────────────────────────────────────────────

// TestLearningPhaseCapturesBaseline checks that during the learning
// phase, an unrecognized abnormal shape is classified as
// EventBaselinePattern (low danger) rather than EventNovelPattern
// (maximum danger), AND the shape is registered in the library so
// future occurrences outside the learning phase are also recognized.
func TestLearningPhaseCapturesBaseline(t *testing.T) {
	d := NewEventDetector()
	// A shape that doesn't match any built-in template.
	fv := FeatureVector{
		Delta: 0.05, Slope: 0.03, Variance: 0.15,
		Duration: 4, ZeroCrossings: 1, MaxDeviation: 0.40,
		IsRising: true, IsFalling: false, IsStable: false,
	}
	signal := 0.45 // above watchThreshold

	// During learning: should capture as baseline, not novel.
	typ, conf := d.Classify(signal, fv, true)
	if typ != EventBaselinePattern {
		t.Fatalf("during learning, expected BaselinePattern, got %s", typ)
	}
	if !floatsClose(conf, 0.3, 1e-9) {
		t.Errorf("expected confidence 0.3, got %v", conf)
	}
	if len(d.Library.LearnedBaselines) != 1 {
		t.Fatalf("expected 1 learned baseline, got %d", len(d.Library.LearnedBaselines))
	}

	// After learning: the same shape should still match as baseline,
	// not fall through to novel.
	typ2, _ := d.Classify(signal, fv, false)
	if typ2 != EventBaselinePattern {
		t.Errorf("after learning, same shape should still match as BaselinePattern, got %s", typ2)
	}
}

// TestLearningPhaseDoesNotCaptureDuplicates checks that registering
// very similar shapes during the learning phase doesn't create
// duplicate entries — the tolerance check deduplicates them.
func TestLearningPhaseDoesNotCaptureDuplicates(t *testing.T) {
	d := NewEventDetector()
	fv1 := FeatureVector{
		Delta: 0.05, Slope: 0.03, Variance: 0.15,
		Duration: 4, ZeroCrossings: 1, MaxDeviation: 0.40,
		IsRising: true, IsFalling: false, IsStable: false,
	}
	// fv2 is within baselineTolerance of fv1.
	fv2 := FeatureVector{
		Delta: 0.06, Slope: 0.035, Variance: 0.155,
		Duration: 5, ZeroCrossings: 2, MaxDeviation: 0.41,
		IsRising: true, IsFalling: false, IsStable: false,
	}
	signal := 0.45

	d.Classify(signal, fv1, true)
	d.Classify(signal, fv2, true)

	if len(d.Library.LearnedBaselines) != 1 {
		t.Errorf("expected similar shapes to deduplicate to 1 entry, got %d", len(d.Library.LearnedBaselines))
	}
}

// TestLearningPhaseDistinctShapes checks that genuinely different
// shapes during the learning phase create separate baseline entries.
func TestLearningPhaseDistinctShapes(t *testing.T) {
	d := NewEventDetector()
	fvRising := FeatureVector{
		Delta: 0.10, Slope: 0.08, Variance: 0.20,
		Duration: 4, MaxDeviation: 0.50,
		IsRising: true, IsFalling: false, IsStable: false,
	}
	fvFalling := FeatureVector{
		Delta: -0.10, Slope: -0.08, Variance: 0.20,
		Duration: 4, MaxDeviation: 0.50,
		IsRising: false, IsFalling: true, IsStable: false,
	}
	signal := 0.45

	d.Classify(signal, fvRising, true)
	d.Classify(signal, fvFalling, true)

	if len(d.Library.LearnedBaselines) != 2 {
		t.Errorf("expected 2 distinct baselines, got %d", len(d.Library.LearnedBaselines))
	}
}

// ── Duration gating (Option C) ───────────────────────────────────────

// TestDurationGatingSuppressesTransientNovel checks Option C: an
// abnormal tick with Duration < novelMinDuration that doesn't match
// any template is downgraded to Normal, not NovelPattern.
func TestDurationGatingSuppressesTransientNovel(t *testing.T) {
	d := NewEventDetector()
	// Short duration, unrecognized shape, NOT in learning mode.
	fv := FeatureVector{
		Delta: 0.35, Slope: 0.02, Variance: 0.12,
		Duration: 1, MaxDeviation: 0.35,
		IsRising: true, IsFalling: false, IsStable: false,
	}
	signal := 0.45

	typ, _ := d.Classify(signal, fv, false)
	if typ != EventNormal {
		t.Errorf("expected transient unrecognized blip to be gated to Normal, got %s", typ)
	}
}

// TestDurationGatingAllowsSustainedNovel checks that a sustained
// unrecognized shape (Duration >= novelMinDuration) IS classified
// as NovelPattern — gating only suppresses transient blips.
func TestDurationGatingAllowsSustainedNovel(t *testing.T) {
	d := NewEventDetector()
	fv := FeatureVector{
		Delta: 0.35, Slope: 0.02, Variance: 0.12,
		Duration: novelMinDuration, MaxDeviation: 0.35,
		IsRising: true, IsFalling: false, IsStable: false,
	}
	signal := 0.45

	typ, conf := d.Classify(signal, fv, false)
	if typ != EventNovelPattern {
		t.Errorf("expected sustained unrecognized shape to classify as NovelPattern, got %s", typ)
	}
	if !floatsClose(conf, 1.0, 1e-9) {
		t.Errorf("expected confidence 1.0, got %v", conf)
	}
}

// TestBaselinePatternDangerWeight confirms that EventBaselinePattern
// has a much lower danger weight than EventNovelPattern — the whole
// point of the learning phase is that learned shapes are NOT scary.
func TestBaselinePatternDangerWeight(t *testing.T) {
	baselineWeight := eventDangerWeights[EventBaselinePattern]
	novelWeight := eventDangerWeights[EventNovelPattern]
	if baselineWeight >= novelWeight {
		t.Fatalf("baseline weight (%v) should be much lower than novel weight (%v)",
			baselineWeight, novelWeight)
	}
	if !floatsClose(baselineWeight, 0.1, 1e-9) {
		t.Errorf("expected baseline weight 0.1, got %v", baselineWeight)
	}
}
