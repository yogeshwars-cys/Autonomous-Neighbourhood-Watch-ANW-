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

// Run is the agent's loop, matching the README's seven-step cycle —
// minus steps 3/4 (share / receive neighbor updates), which don't exist
// until there's a Communicator in Milestone 2:
//
//  1. Observe local environment      -> Sensor.Read()
//  2. Update internal state          -> State.Observe()
//  5. Adjust confidence              -> done inside State.Observe()
//  6. Decide whether action needed   -> act()
//  7. Return to observation          -> loop
func (a *Agent) Run(ctx context.Context) {
	ticker := time.NewTicker(a.Tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			a.State.Observe(Observation{
				Timestamp: t,
				Value:     a.Sensor.Read(),
			})
			a.act()
		}
	}
}

// act is deliberately the only place that does anything visible to the
// outside world right now. "Action" for Milestone 1 just means logging
// its own reasoning — there's nowhere else to send it yet, and that's
// fine: Objective 1 only asks for local decisions, not consequences.
func (a *Agent) act() {
	last := a.State.History[len(a.State.History)-1]
	log.Printf("[%s] obs=%.3f %s", a.State.ID, last.Value, a.State.Explain())
}
