package agent

import "testing"

func TestAdaptiveThresholdsStartAtBaseWithNoSamples(t *testing.T) {
	a := NewAdaptiveThresholds()
	watch, alert := a.Thresholds()
	if !floatsClose(watch, watchThreshold, 1e-9) || !floatsClose(alert, alertThreshold, 1e-9) {
		t.Fatalf("expected thresholds to start at fixed defaults with no data: got watch=%v alert=%v", watch, alert)
	}
}

func TestAdaptiveThresholdsWidenWithVolatility(t *testing.T) {
	stable := NewAdaptiveThresholds()
	for i := 0; i < 30; i++ {
		stable.Update(0.2) // perfectly constant — zero volatility
	}

	volatile := NewAdaptiveThresholds()
	for i := 0; i < 30; i++ {
		if i%2 == 0 {
			volatile.Update(0.05)
		} else {
			volatile.Update(0.5)
		}
	}

	stableWatch, stableAlert := stable.Thresholds()
	volatileWatch, volatileAlert := volatile.Thresholds()

	if volatileWatch <= stableWatch {
		t.Errorf("expected volatile history to widen watch threshold: stable=%v volatile=%v", stableWatch, volatileWatch)
	}
	if volatileAlert <= stableAlert {
		t.Errorf("expected volatile history to widen alert threshold: stable=%v volatile=%v", stableAlert, volatileAlert)
	}
}

func TestAdaptiveThresholdsAreClampedToBounds(t *testing.T) {
	a := NewAdaptiveThresholds()
	// Hammer extreme volatility.
	for i := 0; i < 1000; i++ {
		if i%2 == 0 {
			a.Update(0.0)
		} else {
			a.Update(1.0)
		}
	}
	watch, alert := a.Thresholds()
	if watch > adaptiveMaxWatch || watch < adaptiveMinWatch {
		t.Errorf("watch threshold out of bounds: %v", watch)
	}
	if alert > adaptiveMaxAlert || alert < adaptiveMinAlert {
		t.Errorf("alert threshold out of bounds: %v", alert)
	}
}

func TestAdaptiveThresholdsNeverCollapseTogether(t *testing.T) {
	a := NewAdaptiveThresholds()
	for i := 0; i < 500; i++ {
		a.Update(float64(i%10) / 10.0)
	}
	watch, alert := a.Thresholds()
	if watch >= alert {
		t.Fatalf("expected watch (%v) to always stay below alert (%v)", watch, alert)
	}
}

func TestEnableAdaptiveThresholdsIsOptInAndAdditive(t *testing.T) {
	s := &State{ID: "test"}
	if s.Adaptive != nil {
		t.Fatalf("expected Adaptive to be nil by default")
	}
	watch, alert := s.effectiveThresholds()
	if !floatsClose(watch, watchThreshold, 1e-9) || !floatsClose(alert, alertThreshold, 1e-9) {
		t.Fatalf("expected default state to use fixed thresholds, got watch=%v alert=%v", watch, alert)
	}

	s.EnableAdaptiveThresholds()
	if s.Adaptive == nil {
		t.Fatalf("expected Adaptive to be set after EnableAdaptiveThresholds")
	}
}

// TestAdaptiveThresholdsDoNotChangeMilestone1Behavior is a regression
// guard: an agent that never calls EnableAdaptiveThresholds must behave
// identically, tick for tick, to the original fixed-threshold State.
func TestAdaptiveThresholdsDoNotChangeMilestone1Behavior(t *testing.T) {
	s := &State{ID: "test"}
	for i := 0; i < 10; i++ {
		s.Observe(obsAt(10.0, i))
	}
	s.Observe(obsAt(200.0, 10))
	if s.Adaptive != nil {
		t.Fatalf("Observe() must never implicitly enable adaptive thresholds")
	}
}
