package agent

import (
	"context"
	"fmt"
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
	Communicator Communicator

	// Seeds is the bootstrap list, not the network. As of Milestone 3 an
	// agent is never told the full membership — it's told one or two
	// starting points and discovers the rest through AddressBook growing
	// via gossip. Renamed from "Peers" (Milestone 2) to make that honest:
	// after the first few heartbeats, AddressBook will know about peers
	// that never appear in this slice at all.
	Seeds             []string
	HeartbeatInterval time.Duration
	Neighbors         *NeighborList
	AddressBook       *AddressBook

	// StaleThreshold is how long we wait without a heartbeat from a neighbor
	// before treating them as dead/unreachable.
	StaleThreshold time.Duration
	lastKnownDead  map[string]bool
}

// WithNetwork attaches networking to an already-constructed agent and
// returns it for chaining. seeds only needs to contain ONE reachable
// peer for the agent to eventually discover an entire connected network
// — that's the point of Milestone 3.
func (a *Agent) WithNetwork(comm Communicator, seeds []string, heartbeatInterval time.Duration) *Agent {
	a.Communicator = comm
	a.Seeds = seeds
	a.HeartbeatInterval = heartbeatInterval
	a.Neighbors = NewNeighborList()
	a.AddressBook = NewAddressBook()

	interval := heartbeatInterval
	if interval == 0 {
		interval = a.Tick
	}
	a.StaleThreshold = 6 * interval
	a.lastKnownDead = make(map[string]bool)

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
// updates) are now real — and as of Milestone 3, "share" and "receive"
// also grow the agent's address book, not just its picture of who's
// healthy:
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
	var failureCheckC <-chan time.Time

	if a.Communicator != nil {
		incoming = a.Communicator.Listen(ctx)

		interval := a.HeartbeatInterval
		if interval == 0 {
			interval = a.Tick
		}
		heartbeatTicker := time.NewTicker(interval)
		defer heartbeatTicker.Stop()
		heartbeatC = heartbeatTicker.C

		// Check liveness twice as fast as the threshold for responsiveness
		failureCheckTicker := time.NewTicker(a.StaleThreshold / 2)
		defer failureCheckTicker.Stop()
		failureCheckC = failureCheckTicker.C
	}
	// If a.Communicator is nil, incoming, heartbeatC and failureCheckC stay nil.
	// A nil channel in a select simply never fires.

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
		case <-failureCheckC:
			a.checkNeighborLiveness()
		case hb, ok := <-incoming:
			if !ok {
				incoming = nil // communicator shut down; stop selecting on it
				continue
			}
			a.receiveHeartbeat(hb)
		}
	}
}

func (a *Agent) checkNeighborLiveness() {
	dead := a.Neighbors.DeadPeers(a.StaleThreshold)
	deadMap := make(map[string]bool)
	for _, id := range dead {
		deadMap[id] = true
		if !a.lastKnownDead[id] {
			lastSeenStr := "never"
			if nb, ok := a.Neighbors.Get(id); ok {
				lastSeenStr = fmt.Sprintf("%.1fs ago", time.Since(nb.LastSeen).Seconds())
			}
			log.Printf("[%s] ☠ peer %s is UNREACHABLE (last seen %s)", a.State.ID, id, lastSeenStr)
			a.lastKnownDead[id] = true
		}
	}

	// Check for recovery: if it was in lastKnownDead but isn't in deadMap
	for id := range a.lastKnownDead {
		if !deadMap[id] {
			log.Printf("[%s] ✓ peer %s has RECOVERED", a.State.ID, id)
			delete(a.lastKnownDead, id)
		}
	}
}

// broadcastHeartbeat tells every reachable peer the agent's current
// conclusion about itself, plus its current address book. The reasoning
// payload (Status, DangerScore) is exactly as narrow as Milestone 2 —
// raw state still never crosses the network. KnownPeers is the new,
// separate concern: not "what I believe," but "who I know how to find."
//
// Targets are the union of Seeds (the original bootstrap list — kept
// alive indefinitely in case a seed hasn't replied yet) and everyone
// currently in AddressBook (everyone discovered since). That union is
// what lets the target list grow over time without anyone editing it.
func (a *Agent) broadcastHeartbeat() {
	hb := Heartbeat{
		ID:          a.State.ID,
		Status:      a.State.Status.String(),
		DangerScore: a.State.DangerScore,
		Timestamp:   time.Now(),
		KnownPeers:  a.AddressBook.All(),
	}

	targets := make(map[string]bool)
	for _, addr := range a.Seeds {
		targets[addr] = true
	}
	for _, addr := range a.AddressBook.All() {
		targets[addr] = true
	}

	for addr := range targets {
		if err := a.Communicator.Send(addr, hb); err != nil {
			log.Printf("[%s] could not reach %s: %v", a.State.ID, addr, err)
		}
	}
}

// receiveHeartbeat folds an incoming peer heartbeat into local
// knowledge — and now does so in two genuinely different ways:
//
//  1. Neighbors.Update — what this agent believes about the SENDER's
//     reasoning state. Unchanged from Milestone 2.
//  2. AddressBook.Add — how to reach peers, learned two ways: directly
//     (the sender's own real network address, ground truth from the OS)
//     and transitively (every address the sender claims to know, via
//     KnownPeers). Only the first kind is independently verified; the
//     second is taken on trust, which is precisely the kind of trust
//     Objective 5's "malicious node" experiment will eventually test.
//
// Note what still doesn't happen here: a.State is never touched. A
// neighbor's danger score still cannot influence this agent's own —
// that remains Objective 4 (Cooperation), not yet built.
func (a *Agent) receiveHeartbeat(hb Heartbeat) {
	a.Neighbors.Update(hb)

	if hb.SourceAddr != "" && hb.ID != a.State.ID {
		if isNew := a.AddressBook.Add(hb.ID, hb.SourceAddr); isNew {
			log.Printf("[%s] discovered new peer %s at %s (direct contact)",
				a.State.ID, hb.ID, hb.SourceAddr)
		}
	}

	for peerID, peerAddr := range hb.KnownPeers {
		if peerID == a.State.ID {
			continue // don't add ourselves to our own address book
		}
		if isNew := a.AddressBook.Add(peerID, peerAddr); isNew {
			log.Printf("[%s] discovered new peer %s at %s (via gossip from %s)",
				a.State.ID, peerID, peerAddr, hb.ID)
		}
	}

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
