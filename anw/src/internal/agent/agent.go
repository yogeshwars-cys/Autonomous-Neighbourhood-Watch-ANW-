package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
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

	// Trust tracks how much this agent trusts each peer, learned from
	// whether peers' reports agree with cooperative consensus. Added
	// in Milestone 5 — cooperation requires networking.
	Trust *TrustTable

	// Memory is this agent's episodic event log (Objective 7 /
	// Objective 9) — always present, unlike Trust/Communicator, because
	// remembering your OWN significant history doesn't require a
	// network. See memory.go.
	Memory *EpisodicMemory

	// LongTerm (Milestone 7) is the agent's aggregated statistical
	// memory — running Welford stats per EventType and a periodic
	// pattern detector. Always present (initialized in New()), but
	// only CONSULTED when State.MemorySuppressEnabled is true.
	LongTerm *LongTermSummary

	// PeerEvents is this agent's independently-assembled slice of the
	// network's situational picture (Milestone 5 Extended): significant events
	// heard about each peer, keyed by origin ID, via selective gossip
	// (gossip.go). Nil until WithNetwork attaches it — collective
	// awareness is meaningless without peers to be aware of.
	PeerEvents map[string][]EpisodeDigest

	// relayQueue holds event digests heard from peers that are still
	// eligible for further relay (Hops < maxRelayHops). Drained into
	// the next outgoing heartbeat, then refilled as new digests arrive.
	relayQueue []EpisodeDigest

	// PictureInterval, if non-zero, makes Run() periodically log
	// NetworkPicture() — useful for simulation harnesses observing
	// long-run emergent behavior (Milestone 5 Extended) without instrumenting
	// every call site by hand. Zero (the default) disables it.
	PictureInterval time.Duration

	// prevStatus / alertStreak / sustainedRecorded track status
	// transitions tick-to-tick so recordMemory (below) can classify
	// Episodes without re-deriving history from State.History each time.
	prevStatus        Status
	alertStreak       int
	sustainedRecorded bool

	// VerboseEvents (Milestone 6), if true, makes act() log an extra,
	// more detailed line — full FeatureVector included — every tick an
	// event is active, on top of the concise Explain()-embedded summary
	// that's already always shown. Off by default so a busy network
	// with many agents doesn't flood logs; opt in with cmd/agent's
	// -verbose-events flag.
	VerboseEvents bool

	// EventLogPath (Milestone 6), if non-empty, is where cmd/agent's
	// main() dumps State.EventLog as JSON after Run() returns (see the
	// -event-log flag). Deliberately NOT read anywhere inside agent.go
	// itself — persisting to disk is a cmd/agent concern, not something
	// the reusable Agent type should know how to do on its own.
	EventLogPath string
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
	a.Trust = NewTrustTable()
	a.PeerEvents = make(map[string][]EpisodeDigest)

	return a
}

// WithEvents (Milestone 6 / Objective 8) opts this agent into semantic
// event detection and returns it for chaining, matching the exact same
// additive pattern as WithNetwork/EnableAdaptiveThresholds: a bare
// New()-constructed agent (State.Detector == nil) behaves exactly like
// Milestones 1-5 until this is called. Unlike WithNetwork, this needs
// no arguments and no networking — classifying your OWN sensor's shape
// is local reasoning, same as Objective 1's original decision loop, and
// doesn't require a peer to exist.
func (a *Agent) WithEvents() *Agent {
	a.State.Detector = NewEventDetector()
	return a
}

// WithLearningPhase configures an initial calibration period of ticks
// ticks during which unrecognized abnormal shapes are captured as
// baseline patterns rather than flagged as NovelPattern. Requires
// events to be enabled first (WithEvents) — calling this without
// WithEvents is harmless but has no effect, since the learning counter
// is only consulted inside updateEvents (detector.go), which itself
// only runs when State.Detector != nil.
//
// The default (0) means no learning phase: the agent classifies
// unrecognized shapes as NovelPattern from its very first tick,
// exactly as Milestone 6 always did. Typical values: 50-200 ticks
// depending on how noisy the environment is.
func (a *Agent) WithLearningPhase(ticks int) *Agent {
	a.State.LearningTicksRemaining = ticks
	return a
}

// WithMemorySuppress (Milestone 7 / Objective 9) opts this agent into
// memory-influenced DangerScore suppression: when a new anomaly
// closely matches something the agent has seen many times before, the
// final DangerScore gets a bounded discount (up to 40%). Requires
// events to be enabled (WithEvents) for meaningful classification —
// without events, all episodes key to EventNormal, and the familiarity
// engine has nothing to differentiate. Like every other capability in
// this codebase, this is additive and opt-in.
func (a *Agent) WithMemorySuppress() *Agent {
	a.State.MemorySuppressEnabled = true
	a.State.LongTerm = a.LongTerm
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
		Sensor:     sensor,
		Tick:       tick,
		Memory:     NewEpisodicMemory(),
		LongTerm:   NewLongTermSummary(),
		prevStatus: StatusCalm,
	}
}

// Run is the agent's loop, matching the README's seven-step cycle.
// With networking attached, steps 3 and 4 (share / receive neighbor
// updates) are now real — and as of Milestone 3, "share" and "receive"
// also grow the agent's address book, not just its picture of who's
// healthy. As of Objective 7/7, they also grow episodic memory and the
// network's collective picture (see recordMemory, ingestEvents):
//
//  1. Observe local environment      -> Sensor.Read()
//  2. Update internal state          -> State.Observe() (adaptive thresholds, if enabled)
//  3. Share relevant info w/ peers   -> broadcastHeartbeat() (if networked; includes event digests)
//  4. Receive neighbor updates       -> receiveHeartbeat() (if networked; ingests peer event digests)
//  5. Adjust confidence              -> done inside State.Observe() / cooperate()
//  6. Decide whether action needed   -> act() + recordMemory()
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
	var pictureC <-chan time.Time

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
	if a.PictureInterval > 0 {
		pictureTicker := time.NewTicker(a.PictureInterval)
		defer pictureTicker.Stop()
		pictureC = pictureTicker.C
	}
	// If a.Communicator is nil, incoming, heartbeatC and failureCheckC stay nil.
	// A nil channel in a select simply never fires. Same for pictureC when
	// PictureInterval is 0.

	for {
		select {
		case <-ctx.Done():
			return
		case t := <-senseTicker.C:
			a.State.Observe(Observation{
				Timestamp: t,
				Value:     a.Sensor.Read(),
			})
			a.cooperate()
			a.act()
			a.recordMemory()
		case <-heartbeatC:
			a.broadcastHeartbeat()
		case <-failureCheckC:
			a.checkNeighborLiveness()
		case <-pictureC:
			log.Print(a.NetworkPicture())
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
		Events:      a.gossipPayload(),
		ActiveEvent: a.activeEventSummary(),
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

	if len(hb.Events) > 0 {
		a.ingestEvents(hb.Events)
	}

	eventNote := ""
	if hb.ActiveEvent != nil {
		eventNote = fmt.Sprintf(" event=%s(conf=%.2f)", hb.ActiveEvent.Type, hb.ActiveEvent.Confidence)
	}
	log.Printf("[%s] heard from %s: status=%s danger=%.3f%s",
		a.State.ID, hb.ID, hb.Status, hb.DangerScore, eventNote)
}

// activeEventSummary (Milestone 6) is the wire-safe form of this
// agent's current ActiveEvent, if any — nil whenever events aren't
// enabled or nothing is currently abnormal, so broadcastHeartbeat never
// fabricates a summary just to have something to send.
func (a *Agent) activeEventSummary() *EventSummary {
	if a.State.ActiveEvent == nil {
		return nil
	}
	return a.State.ActiveEvent.Summary(time.Now())
}

// act is deliberately the only place that does anything visible to the
// outside world right now. "Action" for Milestone 1 just means logging
// its own reasoning — there's nowhere else to send it yet, and that's
// fine: Objective 1 only asks for local decisions, not consequences.
// Milestone 5 adds the cooperative score alongside the local one.
func (a *Agent) act() {
	last := a.State.History[len(a.State.History)-1]
	if a.Trust != nil && a.State.CooperativeDanger > 0 {
		log.Printf("[%s] obs=%.3f %s %s", a.State.ID, last.Value,
			a.State.Explain(),
			fmt.Sprintf("coop=%.3f", a.State.CooperativeDanger))
	} else {
		log.Printf("[%s] obs=%.3f %s", a.State.ID, last.Value, a.State.Explain())
	}

	// Milestone 6: an optional, more verbose second line — the raw
	// FeatureVector that produced the current classification. Kept
	// entirely separate from the line above (rather than folding
	// Features into Explain() itself) so a busy network's default logs
	// stay exactly as concise as Milestone 1-5's always were; this is
	// strictly opt-in extra detail for someone actively debugging why a
	// pattern matched.
	if a.VerboseEvents && a.State.ActiveEvent != nil {
		fv := a.State.ActiveEvent.Features
		log.Printf("[%s]   features: delta=%.3f slope=%.3f variance=%.3f duration=%d zero_crossings=%d max_dev=%.3f rising=%v falling=%v stable=%v",
			a.State.ID, fv.Delta, fv.Slope, fv.Variance, fv.Duration, fv.ZeroCrossings, fv.MaxDeviation,
			fv.IsRising, fv.IsFalling, fv.IsStable)
	}
}

// cooperate builds peer signals from the current neighbor list,
// computes the cooperative danger score, and updates status based
// on the blended result. This is the integration point between the
// self-contained cooperation module and the live agent loop.
func (a *Agent) cooperate() {
	if a.Trust == nil || a.Neighbors == nil {
		return // no networking — cooperation is a no-op
	}

	// Objective 7: let reputation for peers we haven't heard agreement/
	// disagreement from in a while relax back toward neutral, before
	// this tick's fresh signals (if any) reinforce it further below.
	a.Trust.DecayStale(time.Now())

	all := a.Neighbors.All()
	if len(all) == 0 {
		return // no neighbors yet — nothing to cooperate with
	}

	peers := make([]PeerSignal, len(all))
	for i, nb := range all {
		peers[i] = PeerSignal{
			ID:          nb.ID,
			DangerScore: nb.DangerScore,
			Live:        a.Neighbors.IsAlive(nb.ID, a.StaleThreshold),
			EventType:   nb.ActiveEventType,
		}
	}

	// Milestone 6: fold in this agent's own current event type so
	// CooperateWithEvent can weight peer event-TYPE agreement more
	// heavily than raw danger-score agreement (PLAN_2's requirement).
	// selfEventType stays "" whenever events aren't enabled or nothing
	// is currently abnormal, which ReinforceWithEvent treats identically
	// to Milestone 5's plain Cooperate — no behavior change for agents
	// that never call WithEvents().
	selfEventType := ""
	if a.State.ActiveEvent != nil {
		selfEventType = a.State.ActiveEvent.Type.String()
	}
	a.State.CooperativeDanger = CooperateWithEvent(a.State.DangerScore, peers, a.Trust, selfEventType)

	// Re-evaluate status against the cooperative score instead of just
	// the local score — this is the moment where peer influence actually
	// changes this agent's behavior, closing the gap that existed since
	// Milestone 2.
	a.State.updateStatusFromCooperative()
}

// ── Objective 7: episodic memory integration ────────────────────────

// sustainedTicks is how many consecutive ticks an agent must remain in
// StatusAlert before that episode is upgraded from a one-off EpisodeSpike
// to an EpisodeSustained. Chosen so a single noisy tick that briefly
// crosses into ALERT and immediately drops back out is never mistaken
// for a standing situation.
const sustainedTicks = 4

// recordMemory turns this tick's status (whichever score decided it —
// local-only for an unnetworked agent, cooperative once Milestone 5's
// blending is active) into a semantic Episode when something meaningful
// changed, and prunes memory afterward. This is the answer to
// Objective 8's "how can raw sensor values become meaningful
// knowledge?": a status TRANSITION is the smallest fact worth
// remembering — not every tick, just the ones that meant something.
func (a *Agent) recordMemory() {
	if a.Memory == nil {
		return
	}

	danger := a.State.DangerScore
	if a.Trust != nil && a.State.CooperativeDanger > 0 {
		danger = a.State.CooperativeDanger
	}
	now := a.State.LastUpdated
	if now.IsZero() {
		now = time.Now()
	}

	curr := a.State.Status
	switch {
	case curr == StatusAlert:
		a.alertStreak++
		switch {
		case a.prevStatus != StatusAlert:
			a.Memory.Record(Episode{Timestamp: now, Kind: EpisodeSpike, DangerScore: danger, OriginType: a.currentEventType(), Note: "entered ALERT"})
		case a.alertStreak == sustainedTicks && !a.sustainedRecorded:
			a.Memory.Record(Episode{Timestamp: now, Kind: EpisodeSustained, DangerScore: danger, OriginType: a.currentEventType(), Note: "ALERT sustained"})
			a.sustainedRecorded = true
		}
	case a.prevStatus == StatusAlert:
		// Left ALERT this tick, for whatever calmer status.
		a.Memory.Record(Episode{Timestamp: now, Kind: EpisodeRecovery, DangerScore: danger, OriginType: a.currentEventType(), Note: "recovered from ALERT"})
		a.alertStreak = 0
		a.sustainedRecorded = false
	default:
		a.alertStreak = 0
		a.sustainedRecorded = false
	}

	// Milestone 6: a just-resolved semantic Event becomes a permanent
	// Episode — "a lived moment becomes a memory once it's over" (see
	// state.go's LastResolvedEvent / episodic_memory.go's header
	// comment). LastResolvedEvent is a one-tick pulse (State.Observe
	// clears it at the start of every tick), so this only ever fires on
	// the exact tick an event finishes, never retroactively and never
	// more than once for the same event.
	if resolved := a.State.LastResolvedEvent; resolved != nil {
		// Remember the episode by the event's TYPE severity
		// (eventDangerWeights), not resolved.DangerWeight() — the
		// latter is deliberately 0 for EventResolved (see
		// EventStatus.statusWeight), which would make every resolved
		// event look equally forgettable regardless of how serious it
		// was while active. A resolved NovelPattern should still be
		// remembered as having mattered more than a resolved Plateau.
		resolvedDanger := eventDangerWeights[resolved.Type]
		a.Memory.Record(Episode{
			Timestamp:   resolved.LastSeen,
			Kind:        EpisodeRecovery,
			DangerScore: resolvedDanger,
			OriginType:  resolved.Type,
			Note: fmt.Sprintf("%s event resolved: %s (confidence %.2f, lasted %s)",
				resolved.Type.Category(), resolved.Type, resolved.Confidence,
				resolved.Duration(resolved.LastSeen).Round(time.Second)),
		})

		// Milestone 7: feed resolved event into long-term statistics.
		if a.LongTerm != nil {
			a.LongTerm.Record(resolved.Type, resolvedDanger, resolved.LastSeen)
		}
	}

	a.Memory.Prune(now)
	a.prevStatus = curr
}
// currentEventType returns the currently active EventType if semantic
// event detection is enabled and an event is live, or EventNormal as
// the explicit "untyped" bucket for pre-M6 agents and ticks where no
// event is active.
func (a *Agent) currentEventType() EventType {
	if a.State.ActiveEvent != nil {
		return a.State.ActiveEvent.Type
	}
	return EventNormal
}

// ── Milestone 5 Extended: selective gossip integration ───────────────────────

// gossipPayload assembles this tick's outgoing event digests: this
// agent's own top-important local events (fresh, Hops=0) plus whatever
// relay-eligible digests are queued from peers. The relay queue is
// drained here — anything not sent this tick was already capped by
// relayQueueLimit, so nothing is silently lost, just delayed to the
// next heartbeat if the combined payload had to be trimmed.
func (a *Agent) gossipPayload() []EpisodeDigest {
	own := SelectForGossip(a.State.ID, a.Memory, time.Now())

	relay := a.relayQueue
	if len(relay) > maxRelayEvents {
		relay = relay[:maxRelayEvents]
	}
	a.relayQueue = nil

	if len(own) == 0 && len(relay) == 0 {
		return nil
	}
	return append(own, relay...)
}

// ingestEvents folds received event digests into two places:
//  1. PeerEvents — this agent's own slice of the network's collective
//     picture (Milestone 5 Extended), bounded per-origin by peerEventHistoryLimit.
//  2. relayQueue — candidates for further re-gossip, hop-incremented
//     and bounded by relayQueueLimit, enabling information to travel
//     more than one hop without flooding the network (see gossip.go).
func (a *Agent) ingestEvents(digests []EpisodeDigest) {
	if a.PeerEvents == nil {
		a.PeerEvents = make(map[string][]EpisodeDigest)
	}
	for _, d := range digests {
		if d.OriginID == a.State.ID {
			continue // don't file our own news under "peers"
		}
		a.PeerEvents[d.OriginID] = appendBounded(a.PeerEvents[d.OriginID], d, peerEventHistoryLimit)
	}

	relay := RelayCandidates(a.State.ID, digests)
	a.relayQueue = append(a.relayQueue, relay...)
	if len(a.relayQueue) > relayQueueLimit {
		a.relayQueue = a.relayQueue[len(a.relayQueue)-relayQueueLimit:]
	}
}

// NetworkPicture summarizes what THIS agent currently believes is
// happening across the network — its own, independently-assembled
// view, built entirely from local memory plus whatever event digests
// selective gossip has carried to it over time. No two agents'
// NetworkPicture() calls are guaranteed to agree, and that's not a
// bug: nobody here has global knowledge (README's core design
// principle). Useful group-level awareness emerging anyway from these
// partial, independently-built pictures is precisely PLAN_3's
// Objective 16/20 question — "what principles create collective
// intelligence?" — made observable.
func (a *Agent) NetworkPicture() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] NETWORK PICTURE\n", a.State.ID)
	fmt.Fprintf(&b, "  own memory: %s\n", a.Memory.Summary(time.Now()))

	if len(a.PeerEvents) == 0 {
		b.WriteString("  no peer events heard yet")
		return b.String()
	}

	for peerID, events := range a.PeerEvents {
		last := events[len(events)-1]
		trace := ""
		if a.Trust != nil {
			trace = " | " + a.Trust.ReputationTrace(peerID)
		}
		fmt.Fprintf(&b, "  %s: %d event(s) heard, most recent=%s (danger=%.3f, hops=%d)%s\n",
			peerID, len(events), last.Kind, last.DangerScore, last.Hops, trace)
	}
	return strings.TrimRight(b.String(), "\n")
}
