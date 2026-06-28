package agent

import (
	"testing"
	"time"
)

func obsAt(v float64, secOffset int) Observation {
	return Observation{
		Timestamp: time.Unix(int64(secOffset), 0),
		Value:     v,
	}
}

func TestStaysCalmOnStableInput(t *testing.T) {
	s := &State{ID: "test"}
	for i := 0; i < 20; i++ {
		s.Observe(obsAt(10.0, i))
	}
	if s.Status != StatusCalm {
		t.Errorf("expected CALM on stable input, got %s (danger=%.3f)", s.Status, s.DangerScore)
	}
}

func TestSpikeRaisesDangerScore(t *testing.T) {
	s := &State{ID: "test"}
	for i := 0; i < 10; i++ {
		s.Observe(obsAt(10.0, i))
	}
	before := s.DangerScore

	s.Observe(obsAt(100.0, 10)) // sharp spike

	if s.DangerScore <= before {
		t.Errorf("expected danger score to rise after spike: before=%.3f after=%.3f", before, s.DangerScore)
	}
}

func TestSpikeEventuallyTriggersAlert(t *testing.T) {
	s := &State{ID: "test"}
	for i := 0; i < 10; i++ {
		s.Observe(obsAt(10.0, i))
	}

	// Sustained large deviation, not just one tick, should be enough to
	// cross into ALERT — a single noisy reading should not.
	reachedAlert := false
	for i := 10; i < 20; i++ {
		s.Observe(obsAt(200.0, i))
		if s.Status == StatusAlert {
			reachedAlert = true
			break
		}
	}
	if !reachedAlert {
		t.Errorf("expected sustained deviation to eventually reach ALERT, final danger=%.3f", s.DangerScore)
	}
}

func TestDangerScoreDecaysWhenCalmAgain(t *testing.T) {
	s := &State{ID: "test"}
	for i := 0; i < 10; i++ {
		s.Observe(obsAt(10.0, i))
	}
	s.Observe(obsAt(100.0, 10))
	peak := s.DangerScore

	for i := 11; i < 30; i++ {
		s.Observe(obsAt(10.0, i))
	}

	if s.DangerScore >= peak {
		t.Errorf("expected danger score to decay after returning to normal: peak=%.3f final=%.3f", peak, s.DangerScore)
	}
}

func TestFirstObservationSetsBaselineWithoutAlarm(t *testing.T) {
	s := &State{ID: "test"}
	s.Observe(obsAt(500.0, 0)) // arbitrary first value, should not look like a spike

	if s.Status != StatusCalm {
		t.Errorf("expected first observation to be calm regardless of magnitude, got %s", s.Status)
	}
}

func TestHistoryIsBounded(t *testing.T) {
	s := &State{ID: "test"}
	for i := 0; i < historyLimit+25; i++ {
		s.Observe(obsAt(10.0, i))
	}
	if len(s.History) != historyLimit {
		t.Errorf("expected history capped at %d, got %d", historyLimit, len(s.History))
	}
}
