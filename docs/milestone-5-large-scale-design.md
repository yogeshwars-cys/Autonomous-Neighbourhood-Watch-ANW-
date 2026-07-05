# Milestone 5 Extended: Deeper Collective Intelligence

## Scope and numbering

Following the advancement request's mapping, Milestone 5 Extended builds toward
PLAN_2's and PLAN_3's collective-intelligence objectives (particularly
Objective 16 — "what principles allow collective intelligence to
emerge?" and Objective 20's long-term emergent-properties framing),
using Objective 7's episodic memory and selective gossip
(`memory.go`, `gossip.go`) as its foundation. This is a **first step**,
not a full implementation of PLAN_2/PLAN_3 — see "What this does NOT
do" below for the honest boundary.

## What this satisfies

- **A shared/collective picture, assembled with no central controller.**
  `Agent.PeerEvents` + `Agent.NetworkPicture()` give each agent its own,
  independently-built view of "what's been happening across the
  network," built entirely from bounded, selective gossip exchanges
  (Objective 7's `EventDigest`) — never a query to a central store,
  because there isn't one.
- **Long-term emergent-property observability.** `Agent.PictureInterval`
  + the `-picture-interval` CLI flag let a running simulation log its
  self-assembled picture periodically, which is what makes "does useful
  group awareness emerge from these local exchanges" an observable,
  testable claim instead of an assertion.
- **Simulation harness improvements** for exactly the scenarios the
  advancement request named: larger networks, long-running stability,
  and adaptive behavior under changing conditions
  (`scripts/run-collective-demo.sh`).

## Core design decisions

### 1. Collective intelligence emerges from disagreement, not consensus

`NetworkPicture()`'s doc comment says it plainly: no two agents'
pictures are guaranteed to match, and that's not a bug. Every agent's
`PeerEvents` map reflects only what gossip has actually carried to
*it* — shaped by network topology, trust-driven cooperation, and the
random luck of which heartbeats arrived when. A central "ground truth"
view would defeat the entire research premise stated in the README:
no central controller, local state only. What this milestone adds is
the ability to *observe* that each agent's partial, honestly incomplete
picture is nonetheless useful — which is the actual research question
behind "collective intelligence," not an implementation detail to
paper over.

### 2. Reuse Objective 7's gossip mechanism rather than inventing a second one

An earlier design considered a dedicated "collective intelligence"
message type distinct from Objective 7's event gossip. It was dropped:
splitting "gossip for adaptive behaviour" from "gossip for collective
intelligence" would mean two wire formats, two selection policies, and
two relay-bounding rules doing conceptually the same job. Instead,
Milestone 5 Extended is *what Objective 7's gossip mechanism produces once you
look at it from the receiving side* — `PeerEvents` is just the
accumulated inbox of `EventDigest`s Objective 7 already defined.
This is a design decision worth stating explicitly: the "next
evolution" asked for in the brief was in this case an interpretation of
existing machinery, not new machinery.

### 3. Reputation is folded into the picture, not kept separate

`NetworkPicture()` includes each known peer's `ReputationTrace`
alongside its recent events. This is a small but deliberate choice:
collective intelligence that ignores *how much to trust the source* of
each piece of shared knowledge isn't very intelligent — a network of
agents that gossip credulously is exactly as vulnerable to a single bad
actor as one with no gossip at all. Surfacing trust next to the events
it colors is the cheapest possible step toward "collective intelligence
that accounts for source reliability," without building a full Bayesian
belief-fusion system that the "no ML, explainable" principle would rule
out anyway.

### 4. PictureInterval is observability, not behavior

Logging `NetworkPicture()` periodically doesn't change what an agent
*does* — no decision depends on it. It exists purely so a human (or a
simulation harness) can watch collective awareness assemble over time
without instrumenting every test by hand. This keeps it safely optional
(`PictureInterval == 0` disables it, matching every other opt-in
feature in this codebase) and free of side effects on the actual
decision-making system.

## What this does NOT do

Being direct about the boundary, since "collective intelligence" is an
easy phrase to over-claim:

- **No belief fusion or consensus algorithm.** Agents don't reconcile
  disagreeing pictures, vote, or converge on a single shared state.
  They each keep their own honestly partial view.
- **No emergent classification beyond `EventKind`.** PLAN_2's fuller
  "semantic events" vision (richer event types, causal linking between
  events) isn't attempted — `EventKind`'s three values are intentionally
  the minimum viable semantic layer.
- **No long-term/tiered memory beyond Objective 7's episodic log.**
  PLAN_2's "Memory" milestone envisions distinct working/episodic/
  long-term tiers; this uses one tier with importance-weighted
  forgetting, which is a smaller claim.
- **No "swarm-level" decision-making.** Nothing here lets the *network*
  decide anything as a unit — every decision remains a single agent's,
  informed by a richer local picture. That's consistent with the
  README's "no central controller" principle, but worth stating since
  "collective intelligence" can imply otherwise.

## Running it

```bash
cd src
go build ./...
go test ./...

# Larger network with periodic collective-picture logging:
scripts/run-collective-demo.sh
```

The demo script starts a handful of nodes in a loosely-connected
topology, seeds a couple with occasional large sensor spikes, and lets
`-picture-interval` print each node's independently-assembled view
periodically — a good starting point for eyeballing whether awareness
of a spike on one edge of the network is reaching nodes that never
talked to the origin directly (multi-hop relay, bounded by
`maxRelayHops`).

## Suggested next step

The most natural extension is closing the loop between `NetworkPicture`
and actual behavior: right now, hearing about a severe event three hops
away is purely informational. A defensible next step (staying within
the "no ML" principle) would be a simple, explainable rule like "widen
my own adaptive thresholds slightly if my collective picture shows
multiple concurrent ALERT episodes elsewhere" — network-wide volatility
informing local sensitivity, the network equivalent of what Milestone
6's `AdaptiveThresholds` already does for a single agent's own history.
