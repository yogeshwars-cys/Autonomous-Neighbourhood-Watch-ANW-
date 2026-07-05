package agent

import "time"

// ── Smarter gossip: selective event sharing ─────────────────────────
//
// Since Milestone 3, Heartbeat has piggybacked KnownPeers (addresses)
// on every send — cheap, because an address is a few bytes. Naively
// doing the same with EpisodicMemory (dumping the whole event log on
// every heartbeat) would NOT be cheap: it grows with history length,
// and most of it is redundant with what a peer already heard last
// tick. That's the "at what cost?" question from Objective 3, now
// asked again for events instead of addresses.
//
// EventDigest is the answer: a small, selective, TTL-bounded unit of
// gossip, built on two classic epidemic-protocol ideas rather than
// anything novel:
//
//  1. Selective content — only the TOP-K most important LOCAL events
//     go out each tick (see EpisodicMemory.TopImportant), not the full
//     log. An agent shares its headlines, not its diary.
//  2. Bounded relay — a digest heard from a peer can be re-gossiped to
//     OTHER peers (this is what lets news travel more than one hop
//     through a sparse network), but only up to maxRelayHops times.
//     Real gossip/rumor-mongering protocols (e.g. the epidemic
//     broadcast literature SWIM and its relatives draw from) stop
//     forwarding once a rumor has plausibly saturated the network —
//     doing the same here keeps communication overhead from growing
//     unboundedly as the network scales, which is one of the README's
//     named success metrics ("communication overhead," "message
//     complexity").
//
// This is also the seed of Milestone 5 Extended's collective intelligence: what
// starts as "an event local to node-B" becomes, a few hops later, part
// of node-A's PeerEvents picture — global-ish awareness assembled from
// nothing but bounded local exchanges, with no central relay ever
// involved.

// EventDigest is the network-safe, compact representation of an Event.
// Deliberately smaller than Event itself (no free-text Note) for the
// same reason Heartbeat never carries a raw Observation: keep shared
// state as small as the research question allows.
type EventDigest struct {
	OriginID    string    `json:"origin_id"`
	Kind        EventKind `json:"kind"`
	DangerScore float64   `json:"danger_score"`
	Timestamp   time.Time `json:"timestamp"`

	// Hops counts how many times this digest has already been relayed
	// by an agent other than its origin. RelayCandidates increments it;
	// once it reaches maxRelayHops, the digest is dropped rather than
	// forwarded again.
	Hops int `json:"hops"`
}

const (
	// maxGossipEvents caps how many of THIS agent's own events are
	// selected for a single heartbeat — selective sharing, not a full
	// memory dump.
	maxGossipEvents = 3

	// maxRelayEvents caps how many previously-heard digests get
	// re-forwarded per heartbeat, independent of how many are queued —
	// keeps a single noisy episode from monopolizing every future
	// heartbeat's payload.
	maxRelayEvents = 5

	// maxRelayHops bounds how many times a single event digest can be
	// re-broadcast by agents other than its origin.
	maxRelayHops = 3

	// relayQueueLimit bounds how many not-yet-relayed digests an agent
	// holds onto between heartbeats — this is a queue, not a second
	// memory; anything not sent stays capped rather than growing with
	// network size.
	relayQueueLimit = 20

	// peerEventHistoryLimit bounds how many digests are retained per
	// origin peer in an agent's collective picture (Agent.PeerEvents) —
	// the same bounded-memory philosophy as everywhere else in this
	// codebase, applied to knowledge ABOUT peers instead of self.
	peerEventHistoryLimit = 10
)

// SelectForGossip picks the most important LOCAL events to advertise
// this tick. Always fresh from the origin (Hops=0), since this is
// selecting FROM local memory, not relaying someone else's digest.
func SelectForGossip(selfID string, mem *EpisodicMemory, now time.Time) []EventDigest {
	if mem == nil {
		return nil
	}
	top := mem.TopImportant(maxGossipEvents, now)
	if len(top) == 0 {
		return nil
	}
	out := make([]EventDigest, len(top))
	for i, e := range top {
		out[i] = EventDigest{
			OriginID:    selfID,
			Kind:        e.Kind,
			DangerScore: e.DangerScore,
			Timestamp:   e.Timestamp,
			Hops:        0,
		}
	}
	return out
}

// RelayCandidates filters a batch of received digests down to the ones
// worth re-gossiping: not self-originated (no reason to echo your own
// news back to yourself) and still under the hop limit. Returned
// digests have Hops already incremented, ready to be queued for the
// NEXT outgoing heartbeat.
func RelayCandidates(selfID string, received []EventDigest) []EventDigest {
	out := make([]EventDigest, 0, len(received))
	for _, d := range received {
		if d.OriginID == selfID {
			continue
		}
		if d.Hops >= maxRelayHops {
			continue
		}
		d.Hops++
		out = append(out, d)
	}
	return out
}

// appendBounded appends v to a slice, trimming from the front so the
// result never exceeds limit — the same bounded-history pattern as
// State.recordHistory, reused for per-peer event tracking.
func appendBounded(s []EventDigest, v EventDigest, limit int) []EventDigest {
	s = append(s, v)
	if len(s) > limit {
		s = s[len(s)-limit:]
	}
	return s
}
