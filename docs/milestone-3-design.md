# Milestone 3 — Small Decentralized Network

## What this satisfies

PLAN.md's Milestone 3 requires: multiple autonomous agents, peer-to-peer
communication, no central coordinator. Milestone 2 already proved the
second and third for exactly two nodes; the open problem was scale — and
specifically, that a hand-typed `-peers` list doesn't scale past a
handful of agents. This milestone solves that, and proves it with a real
multi-process run: `scripts/run-network.sh 10 10` produces 10 separate
OS processes, each seeded with only its immediate neighbor in an open
chain, all converging to full mutual awareness — every node ends up
knowing all 9 others — within 10 seconds.

## The core design decision: address book vs neighbor list vs state

This milestone adds a THIRD kind of local knowledge, and the distinction
matters:

- **State** (Milestone 1): what I know about *myself*. Rich, continuous,
  built from direct observation.
- **NeighborList** (Milestone 2): what I *believe* about a peer's
  reasoning. Thin, only as fresh as their last heartbeat.
- **AddressBook** (this milestone): how to *reach* a peer. Thinner still
  — just an ID and an address, nothing about their state at all.

The reason these are three separate types instead of one bigger struct:
an ID can appear in AddressBook long before it ever appears in
NeighborList. Knowing where to find someone and having actually heard
from them are different facts, and conflating them would make it
impossible to represent "I know how to reach node-7 but haven't gotten
a heartbeat from it yet" — which is exactly the state every newly
discovered peer is in for at least one heartbeat interval.

## How discovery actually works — two mechanisms, not one

**1. Passive discovery from direct contact.** Go's `net.UDPConn`, once
bound via `ListenUDP`, uses that same bound port for outgoing `Send`
calls too. That means when agent A sends a packet to agent B, the
source address B's socket observes is A's real, reachable listen
address — not something A had to declare in the message body. So the
instant B receives anything from A, B can record "node-a lives here," at
zero extra protocol cost. This is also a small, deliberate security
property: `Heartbeat.SourceAddr` is tagged `json:"-"` specifically so it
can never be set by the sender's claimed payload — only by what the OS
itself observed. A peer cannot lie about its own reachable address.

**2. Gossip propagation.** Passive discovery alone only ever produces
direct edges — it can't explain how node-1 learns about node-9 in a
10-node chain it was never seeded with. For that, every heartbeat now
also piggybacks the sender's current `AddressBook.All()` as
`KnownPeers`. When B tells A "here's my conclusion about myself," it
also says "and here's everyone I currently know how to reach." A merges
in any ID it didn't already have. This is precisely how production
gossip protocols like SWIM work — piggybacking membership updates on
routine pings rather than running a separate discovery channel — and
it's why the `Heartbeat` struct grew a field instead of a second message
type being introduced.

## Why an open chain, not a ring, for the proof

`scripts/run-network.sh` seeds node-i with node-(i+1) only, all the way
down to the last node, which gets **no seed at all**. A closed ring
(wrapping the last node back to the first) was deliberately rejected for
the test and the demo script: it would let every node reach its two
ring-neighbors through direct seeding alone, making "did gossip do
anything" unobservable. With an open chain, the only way node-1 can ever
learn node-10's address is if every node in between relayed it — which
is exactly what `TestGossipDiscoversTransitivePeer` checks for directly,
asserting that node-a (seeded only with node-b) ends up with node-c's
real address, and that the log explicitly says `(via gossip from
node-b)`, not `(direct contact)`.

## What this does NOT do — the gap that matters next

Discovery only grows `AddressBook`. It still never touches `a.State` or
even feeds into how `NeighborList` entries are *used*. An agent now
knows the entire network exists, and can reach all of it, but has no
opinion yet about what that means. PLAN.md's Objective 4 question — "can
agents solve problems together?" — is still completely open: every
agent in a 10-node converged network is still reasoning in total
isolation, just with a much better address book.

There's also an honest trust gap worth naming rather than hiding:
`KnownPeers` entries are taken on faith. A malicious node could gossip
fake ID-to-address mappings, and nothing here would catch it — that's
not an oversight, it's precisely the README's Experiment 5 ("malicious
node spreading false alerts"), deliberately left for later rather than
solved prematurely.

## Running it

Single test of the core mechanism:
```
cd src
go test ./... -run Gossip -v
```

A real multi-process network, with node count and duration as actual
parameters:
```
./scripts/run-network.sh <node-count> [duration-seconds]
./scripts/run-network.sh 5         # 5 nodes, 15s
./scripts/run-network.sh 10 10     # 10 nodes, 10s
./scripts/run-network.sh 20 20     # try a bigger one
```

The script prints a discovery summary (how many of the N-1 possible
peers each node found) and a sample of *how* each discovery happened —
direct contact vs. gossip — so the propagation pattern is visible, not
just the end state.

## Suggested next step: Milestone 4 (Failure Testing)

The network can now find itself. The natural next question, straight
from PLAN.md's Objective 5: what happens when a node disappears mid-run?
Right now nothing notices — a peer that stops sending heartbeats just
silently stops appearing in logs; nothing marks it as gone, suspect, or
even stale. `Neighbor.LastSeen` already exists and is unused for
anything. The next real design decision is a staleness threshold: how
long without a heartbeat before a peer should be treated as failed
rather than just quiet?
