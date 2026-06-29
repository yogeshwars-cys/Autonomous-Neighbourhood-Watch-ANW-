package agent

import (
	"context"
	"log"
	"time"
)

// Sensor is anything that can produce one local observation.
//
// This interface is the entire boundary between "the agent" and "the
// world it watches." Milestone 1 uses a synthetic generator (see
// sensor.go). Later milestones can swap in something that reads real
// metrics without changing a single line of decision logic — which is
// the point: perception and reasoning are different concerns.
type Sensor interface {
	Read() float64
}

// Agent ties together state, sensing, and the decision loop.
//
// Deliberately absent: any notion of other agents. Objective 1 is about
// a single agent reasoning locally; neighbors, messages, and trust are
// Objective 3 and arrive in Milestone 2 as a separate Communicator,
// not as fields bolted onto this struct. Keeping "do I have neighbors"
// out of the Agent type is what makes "no node has access to complete
// network state" (a README design principle) structurally true rather
// than just a convention someone has to remember.
type Agent struct {
	State  State
	Sensor Sensor
	Tick   time.Duration

	// Everything below is optional. A zero-value Agent (Communicator ==
	// nil) behaves identically to the Milestone 1 agent — networking is
	// additive, never a hard requirement of being an agent at all.
	Communicator      Communicator
	Peers             []string
	HeartbeatInterval time.Duration
	Neighbors         *NeighborList
}

// WithNetwork attaches networking to an already-constructed agent and
// returns it for chaining. Calling this is what turns a Milestone 1
// agent into a Milestone 2 agent — nothing about State or the decision
// loop changes; this only adds a second, independent activity (talking
// to peers) alongside the first (reasoning about itself).
func (a *Agent) WithNetwork(comm Communicator, peers []string, heartbeatInterval time.Duration) *Agent {
	a.Communicator = comm
	a.Peers = peers
	a.HeartbeatInterval = heartbeatInterval
	a.Neighbors = NewNeighborList()
	return a
}

// New creates an agent with empty initial state. Status starts at Calm
// because an agent with zero observations has no evidence of anything
// else — defaulting to alarm would mean reacting to ignorance, not to
// the environment.
func New(id string, sensor Sensor, tick time.Duration) *Agent {
	return &Agent{
		State: State{
			ID:     id,
			Status: StatusCalm,
		},
		Sensor: sensor,
		Tick:   tick,
	}
}

// Run is the agent's loop, matching the README's seven-step cycle.
// With networking attached, steps 3 and 4 (share / receive neighbor
// updates) are now real:
//
//  1. Observe local environment      -> Sensor.Read()
//  2. Update internal state          -> State.Observe()
//  3. Share relevant info w/ peers   -> broadcastHeartbeat() (if networked)
//  4. Receive neighbor updates       -> receiveHeartbeat() (if networked)
//  5. Adjust confidence              -> done inside State.Observe()
//  6. Decide whether action needed   -> act()
//  7. Return to observation          -> loop
//
// Sensing and heartbeating run on independent timers, not the same one.
// "How often do I look at the world" and "how often do I tell others
// what I think" are different questions with different right answers —
// collapsing them into one tick rate would hide that they're separate
// design knobs.
func (a *Agent) Run(ctx context.Context) {
	senseTicker := time.NewTicker(a.Tick)
	defer senseTicker.Stop()

	var incoming <-chan Heartbeat
	var heartbeatC <-chan time.Time

	if a.Communicator != nil {
		incoming = a.Communicator.Listen(ctx)

		interval := a.HeartbeatInterval
		if interval == 0 {
			interval = a.Tick
		}
		heartbeatTicker := time.NewTicker(interval)
		defer heartbeatTicker.Stop()
		heartbeatC = heartbeatTicker.C
	}
	// If a.Communicator is nil, incoming and heartbeatC stay nil. A nil
	// channel in a select simply never fires — Go's idiomatic way of
	// saying "this case doesn't exist for this agent."

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-senseTicker.C:
			a.State.Observe(Observation{
				Timestamp: t,
				Value:     a.Sensor.Read(),
			})
			a.act()
		case <-heartbeatC:
			a.broadcastHeartbeat()
		case hb, ok := <-incoming:
			if !ok {
				incoming = nil // communicator shut down; stop selecting on it
				continue
			}
			a.receiveHeartbeat(hb)
		}
	}
}

// broadcastHeartbeat tells every known peer the agent's current
// conclusion about itself — and nothing else. This is the only place in
// the codebase where local state crosses the network boundary, and it
// crosses deliberately narrow: three fields, not the whole State struct.
func (a *Agent) broadcastHeartbeat() {
	hb := Heartbeat{
		ID:          a.State.ID,
		Status:      a.State.Status.String(),
		DangerScore: a.State.DangerScore,
		Timestamp:   time.Now(),
	}
	for _, peer := range a.Peers {
		if err := a.Communicator.Send(peer, hb); err != nil {
			log.Printf("[%s] could not reach peer %s: %v", a.State.ID, peer, err)
		}
	}
}

// receiveHeartbeat folds an incoming peer heartbeat into local neighbor
// knowledge. Note what this does NOT do: it never touches a.State. A
// peer's danger score updating this agent's own danger score is
// cooperation (Objective 4), which doesn't exist yet — for now, hearing
// from a neighbor only updates what's known about that neighbor.
func (a *Agent) receiveHeartbeat(hb Heartbeat) {
	a.Neighbors.Update(hb)
	log.Printf("[%s] heard from %s: status=%s danger=%.3f",
		a.State.ID, hb.ID, hb.Status, hb.DangerScore)
}

// act is deliberately the only place that does anything visible to the
// outside world right now. "Action" for Milestone 1 just means logging
// its own reasoning — there's nowhere else to send it yet, and that's
// fine: Objective 1 only asks for local decisions, not consequences.
func (a *Agent) act() {
	last := a.State.History[len(a.State.History)-1]
	log.Printf("[%s] obs=%.3f %s", a.State.ID, last.Value, a.State.Explain())
}
