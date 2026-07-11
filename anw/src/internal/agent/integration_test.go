package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// scriptedSensor is a fully deterministic Sensor: it plays back a fixed
// sequence of values, then holds the final one forever. Unlike
// SyntheticSensor (sensor.go), it has no randomness at all — needed here
// so tests can drive an agent to an EXACT classification (SustainedSpike,
// specifically) without any risk of flakiness from a probabilistic spike
// generator.
type scriptedSensor struct {
	values []float64
	i      int
}

func (s *scriptedSensor) Read() float64 {
	if s.i >= len(s.values) {
		return s.values[len(s.values)-1]
	}
	v := s.values[s.i]
	s.i++
	return v
}

// sustainedSpikeScript is a flat baseline (6 ticks at 100) followed by a
// steep, sustained climb. Verified by direct simulation against the real
// detector pipeline: the 11th observation (index 10, value 300) lands
// exactly inside a SustainedSpike/Active classification. Kept as a
// package-level fixture so every test below is reasoning about the exact
// same, hand-verified sequence.
var sustainedSpikeScript = []float64{100, 100, 100, 100, 100, 100, 140, 180, 220, 260, 300}

// driveToSustainedSpike replays sustainedSpikeScript through a*State
// directly (not through Agent.Run()'s wall-clock ticker) so reaching the
// SustainedSpike/Active classification is deterministic and instantaneous
// — no goroutines, no timers, no possibility of missing the window.
func driveToSustainedSpike(t *testing.T, s *State) {
	t.Helper()
	now := time.Unix(0, 0)
	for _, v := range sustainedSpikeScript {
		now = now.Add(100 * time.Millisecond)
		s.Observe(Observation{Timestamp: now, Value: v})
	}
	if s.ActiveEvent == nil || s.ActiveEvent.Type != EventSustainedSpike {
		t.Fatalf("setup failed: expected SustainedSpike after the scripted ramp, got %+v", s.ActiveEvent)
	}
}

// ── End-to-end: sensor -> feature extraction -> event detection ───────

// TestSensorToEventDetectionPipeline is the single-agent half of the
// pipeline MILESTONE6_PROMPT.md's integration_test.go asks for: a real
// Sensor feeds real Observe() calls, which extract real features and run
// them through the real two-stage classifier, ending in a real
// SustainedSpike/Active Event — no step of the pipeline is mocked out.
func TestSensorToEventDetectionPipeline(t *testing.T) {
	sensor := &scriptedSensor{values: sustainedSpikeScript}
	a := New("node-x", sensor, 100*time.Millisecond).WithEvents()

	now := time.Unix(0, 0)
	for range sustainedSpikeScript {
		now = now.Add(100 * time.Millisecond)
		a.State.Observe(Observation{Timestamp: now, Value: a.Sensor.Read()})
	}

	if a.State.ActiveEvent == nil {
		t.Fatal("expected an active event after a sustained climb")
	}
	if a.State.ActiveEvent.Type != EventSustainedSpike {
		t.Errorf("expected SustainedSpike, got %s", a.State.ActiveEvent.Type)
	}
	if a.State.ActiveEvent.Status != EventActive {
		t.Errorf("expected Active status, got %s", a.State.ActiveEvent.Status)
	}
	if len(a.State.EventLog) == 0 {
		t.Error("expected the event to also appear in EventLog")
	}
	// DangerScore should have been raised by the event's contribution
	// (0.8 for an Active SustainedSpike) — proving the classification
	// actually feeds back into the agent's own danger reading, not just
	// a side channel nobody reads.
	if a.State.DangerScore < 0.8-1e-9 {
		t.Errorf("expected DangerScore to reflect the active event's weight (>= 0.8), got %v", a.State.DangerScore)
	}
}

// TestEventsDisabledByDefault checks the additive/opt-in constraint from
// MILESTONE6_PROMPT.md section 3: without WithEvents(), an agent must
// behave exactly like Milestones 1-5 — no ActiveEvent, ever, regardless
// of how extreme the input is.
func TestEventsDisabledByDefault(t *testing.T) {
	sensor := &scriptedSensor{values: sustainedSpikeScript}
	a := New("node-x", sensor, 100*time.Millisecond) // no WithEvents()

	now := time.Unix(0, 0)
	for range sustainedSpikeScript {
		now = now.Add(100 * time.Millisecond)
		a.State.Observe(Observation{Timestamp: now, Value: a.Sensor.Read()})
	}

	if a.State.ActiveEvent != nil {
		t.Errorf("expected no event classification without WithEvents(), got %+v", a.State.ActiveEvent)
	}
	if a.State.Detector != nil {
		t.Error("expected Detector to stay nil without WithEvents()")
	}
}

// ── Event propagation over a real heartbeat ────────────────────────────

// TestEventPropagation is test #9 from MILESTONE6_PROMPT.md section 4:
// agent A detects a SustainedSpike, sends a heartbeat, and agent B
// receives it and sees the event summary. Event detection and lifecycle
// are driven deterministically (driveToSustainedSpike) so the only
// non-deterministic part of this test is the real UDP round trip itself
// — the same trusted mechanism every other networked test in this
// package already relies on.
func TestEventPropagation(t *testing.T) {
	commA, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commA: %v", err)
	}
	commB, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commB: %v", err)
	}
	addrB := commB.LocalAddr().String()

	sensor := &scriptedSensor{values: sustainedSpikeScript}
	agentA := New("node-a", sensor, 100*time.Millisecond).
		WithEvents().
		WithNetwork(commA, []string{addrB}, time.Hour) // heartbeat ticker disabled; we send manually below

	driveToSustainedSpike(t, &agentA.State)
	agentA.broadcastHeartbeat()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	incoming := commB.Listen(ctx)

	select {
	case hb := <-incoming:
		if hb.ID != "node-a" {
			t.Fatalf("expected heartbeat from node-a, got %q", hb.ID)
		}
		if hb.ActiveEvent == nil {
			t.Fatal("expected the heartbeat to carry an ActiveEvent summary")
		}
		if hb.ActiveEvent.Type != EventSustainedSpike.String() {
			t.Errorf("expected event summary type %q, got %q", EventSustainedSpike.String(), hb.ActiveEvent.Type)
		}
		if hb.ActiveEvent.Confidence <= 0 {
			t.Errorf("expected a positive confidence, got %v", hb.ActiveEvent.Confidence)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for node-b to receive node-a's heartbeat")
	}
}

// TestEventPropagationUpdatesNeighborActiveEventType extends propagation
// one step further: once agent B has received the heartbeat (folded in
// via the real receiveHeartbeat path, not a hand-rolled substitute), its
// own NeighborList must reflect node-a's reported event type — this is
// the exact piece of information CooperateWithEvent later reads.
func TestEventPropagationUpdatesNeighborActiveEventType(t *testing.T) {
	commA, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commA: %v", err)
	}
	commB, err := NewUDPCommunicator("127.0.0.1:0")
	if err != nil {
		t.Fatalf("commB: %v", err)
	}
	addrA := commA.LocalAddr().String()

	sensor := &scriptedSensor{values: sustainedSpikeScript}
	agentA := New("node-a", sensor, 100*time.Millisecond).WithEvents().
		WithNetwork(commA, nil, time.Hour)
	agentB := New("node-b", NewSyntheticSensor(10, 0.05, 0, 0), 100*time.Millisecond).
		WithNetwork(commB, []string{addrA}, time.Hour)

	driveToSustainedSpike(t, &agentA.State)

	hb := Heartbeat{
		ID:          agentA.State.ID,
		Status:      agentA.State.Status.String(),
		DangerScore: agentA.State.DangerScore,
		Timestamp:   time.Now(),
		ActiveEvent: agentA.State.ActiveEvent.Summary(time.Now()),
	}
	agentB.receiveHeartbeat(hb)

	nb, ok := agentB.Neighbors.Get("node-a")
	if !ok {
		t.Fatal("expected node-b to know about node-a after receiveHeartbeat")
	}
	if nb.ActiveEventType != EventSustainedSpike.String() {
		t.Errorf("expected neighbor ActiveEventType %q, got %q", EventSustainedSpike.String(), nb.ActiveEventType)
	}
}

// ── Event-aware cooperation ────────────────────────────────────────────

// TestCooperativeEventAgreement is test #10 from MILESTONE6_PROMPT.md
// section 4: two agents reporting the SAME event type should end up
// trusting each other MORE than two otherwise-identical agents reporting
// DIFFERENT event types — event-type agreement is stronger evidence than
// numeric danger-score agreement alone (section 2.6's requirement).
func TestCooperativeEventAgreement(t *testing.T) {
	ttAgree := NewTrustTable()
	ttDisagree := NewTrustTable()

	peerAgree := []PeerSignal{{ID: "node-b", DangerScore: 0.8, Live: true, EventType: EventSustainedSpike.String()}}
	peerDisagree := []PeerSignal{{ID: "node-b", DangerScore: 0.8, Live: true, EventType: EventOscillation.String()}}

	CooperateWithEvent(0.8, peerAgree, ttAgree, EventSustainedSpike.String())
	CooperateWithEvent(0.8, peerDisagree, ttDisagree, EventSustainedSpike.String())

	agreeTrust := ttAgree.TrustOf("node-b")
	disagreeTrust := ttDisagree.TrustOf("node-b")
	if agreeTrust <= disagreeTrust {
		t.Fatalf("expected matching event types to earn MORE trust than conflicting ones: agree=%v disagree=%v",
			agreeTrust, disagreeTrust)
	}
}

// TestCooperativeEventAgreementExceedsScoreAlone proves event-type
// evidence is ADDITIVE on top of Milestone 5's score-based trust, not a
// replacement for it: a peer matching on BOTH score and event type ends
// up more trusted than an otherwise-identical peer that only matched on
// score (plain Cooperate, no event information at all).
func TestCooperativeEventAgreementExceedsScoreAlone(t *testing.T) {
	ttPlain := NewTrustTable()
	ttEvent := NewTrustTable()

	peerPlain := []PeerSignal{{ID: "node-b", DangerScore: 0.8, Live: true}}
	peerEvent := []PeerSignal{{ID: "node-b", DangerScore: 0.8, Live: true, EventType: EventSustainedSpike.String()}}

	Cooperate(0.8, peerPlain, ttPlain)
	CooperateWithEvent(0.8, peerEvent, ttEvent, EventSustainedSpike.String())

	if ttEvent.TrustOf("node-b") <= ttPlain.TrustOf("node-b") {
		t.Fatalf("expected event-type agreement to add trust BEYOND score agreement alone: plain=%v event=%v",
			ttPlain.TrustOf("node-b"), ttEvent.TrustOf("node-b"))
	}
}

// TestCooperativeEventSilenceIsNotDisagreement checks the deliberate
// exception documented on ReinforceWithEvent: if EITHER side has no
// active event ("" / Normal), that's absence of an opinion, not a
// conflicting one, and must not be penalized as though it were.
func TestCooperativeEventSilenceIsNotDisagreement(t *testing.T) {
	ttSilent := NewTrustTable()
	ttConflict := NewTrustTable()

	peerSilent := []PeerSignal{{ID: "node-b", DangerScore: 0.8, Live: true, EventType: ""}}
	peerConflict := []PeerSignal{{ID: "node-b", DangerScore: 0.8, Live: true, EventType: EventOscillation.String()}}

	CooperateWithEvent(0.8, peerSilent, ttSilent, EventSustainedSpike.String())
	CooperateWithEvent(0.8, peerConflict, ttConflict, EventSustainedSpike.String())

	if ttSilent.TrustOf("node-b") <= ttConflict.TrustOf("node-b") {
		t.Fatalf("expected silence (no reported event) to be trusted at least as much as an active conflict: silent=%v conflict=%v",
			ttSilent.TrustOf("node-b"), ttConflict.TrustOf("node-b"))
	}
}

// ── Explainability ─────────────────────────────────────────────────────

// TestExplainWithEvent is test #11 from MILESTONE6_PROMPT.md section 4:
// State.Explain() must mention the event's type, status, and confidence
// once an event is active — proving the event-aware Explain() (decision.go)
// is actually wired into the live agent, not just Event.Explain() in
// isolation (already covered by event_test.go).
func TestExplainWithEvent(t *testing.T) {
	s := &State{ID: "node-x"}
	s.Detector = NewEventDetector()
	driveToSustainedSpike(t, s)

	got := s.Explain()
	for _, want := range []string{"sustained_spike", "Active", "confidence"} {
		if !strings.Contains(got, want) {
			t.Errorf("Explain() = %q, missing %q", got, want)
		}
	}
}

// TestExplainWithoutEventUnchanged checks the additive guarantee one more
// time, at the Explain() level specifically: an agent with no active
// event (events disabled, or currently Normal) gets exactly the
// Milestone 1-5 output, with no trailing " | ..." suffix at all.
func TestExplainWithoutEventUnchanged(t *testing.T) {
	s := &State{ID: "node-x"}
	s.Observe(Observation{Timestamp: time.Unix(0, 0), Value: 10})
	got := s.Explain()
	if strings.Contains(got, "|") {
		t.Errorf("expected no event suffix when no event is active, got %q", got)
	}
}
