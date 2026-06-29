# Milestone 2 — Two Communicating Agents

## What this satisfies

PLAN.md's Milestone 2 requires: message exchange, heartbeats, and
neighbor awareness. All three now exist, over a real UDP socket, between
two separate OS processes — confirmed by running `node-1` and `node-2` as
actual separate binaries on `127.0.0.1:9001`/`9002` and watching each
log `heard from <peer>: status=... danger=...` lines drawn from the
other's real, independently-computed local reasoning.

## Objective 3's four questions, answered concretely

PLAN.md poses these without answering them. Here's what Milestone 2
commits to, and why:

**"What should agents communicate?"** — Only `{ID, Status, DangerScore,
Timestamp}` (`communicator.go`, the `Heartbeat` struct). Explicitly NOT
included: `Observation`, `History`, `Baseline`. Objective 2 already
decided raw sensor data is private; a heartbeat carries the agent's
*conclusion*, never its evidence. This also keeps message size constant
regardless of how much history an agent has accumulated — a peer with 50
stored observations sends the same few bytes as one with 2.

**"How often?"** — Heartbeats run on their own ticker
(`HeartbeatInterval`), deliberately decoupled from the sensing tick
(`Tick`). These answer different questions — "how often do I look at the
world" vs. "how often do I tell others what I concluded" — and conflating
them would hide that they're independent knobs with independent
tradeoffs (sensing rate affects reasoning latency; heartbeat rate affects
bandwidth and how stale a peer's picture of you can get).

**"To whom?"** — A static, hand-specified peer list (`-peers` flag).
Every agent knows exactly who its neighbors are because a human told it.
This is the most naive possible answer on purpose — Milestone 3 is where
"to whom" becomes an actual research question (discovery, dynamic
membership) instead of a CLI argument.

**"At what cost?"** — JSON over UDP. JSON costs more bytes than a packed
binary format would, but it's human-readable, which matters more right
now: you can point `nc -u -l 9001` at a port and watch real heartbeats
go by in plain text while debugging. UDP itself costs reliability — no
delivery guarantee, no ordering guarantee — which is a deliberate,
named tradeoff, not an oversight (see below).

## Why UDP, deliberately

TCP would have been the "safer" choice — guaranteed delivery, guaranteed
order. That safety is exactly what Objective 5 (Resilience) needs to
study honestly. If the transport silently fixed every dropped packet,
"what happens when a message is lost" would never actually happen during
development; it'd have to be artificially reintroduced later. Starting
with UDP means message loss is already a real, observable possibility in
the system as it exists today, not a future feature.

## The asymmetry between `State` and `Neighbor`

Compare the two structs directly:

- `State` (mine): ID, Status, DangerScore, Baseline, a 50-entry History,
  LastUpdated.
- `Neighbor` (theirs): ID, Status, DangerScore, LastSeen. No baseline, no
  history.

This isn't a missing feature — it's Objective 2's "what should never be
global?" made structural. An agent's knowledge of itself is rich and
continuously updated by direct observation. Its knowledge of a peer is
thin, lagging (only as fresh as the last heartbeat), and entirely
secondhand. Any future cooperative behavior (Objective 4) has to be built
on top of that asymmetry, not around it.

## What receiving a heartbeat does NOT do (yet)

`receiveHeartbeat` updates `Neighbors`, full stop. It never touches
`a.State`. Hearing that a neighbor is in `ALERT` does not currently make
this agent more suspicious, more careful, or change its own danger score
in any way. That's intentional — letting peer information influence local
reasoning is *cooperation*, which is Objective 4, and arrives in
Milestone 3 as a deliberate, separate design decision (how much should a
neighbor's alarm affect mine? immediately, or with its own skepticism?)
rather than something that slipped in early by accident.

## Networking is additive, not required

`Agent.Communicator` defaults to `nil`. Every Milestone 1 test still
passes unmodified, and `go run ./cmd/agent` with no networking flags
still behaves exactly as it did before this milestone. Calling
`WithNetwork(...)` is what turns a standalone agent into a networked one
— "being an agent" and "talking to peers" remain genuinely separable
concerns in the code, not just in the documentation.

## Running it (two real OS processes)

Terminal 1:
```
go run ./cmd/agent -id=node-1 -tick=300ms -heartbeat=1s \
  -listen=127.0.0.1:9001 -peers=127.0.0.1:9002
```

Terminal 2:
```
go run ./cmd/agent -id=node-2 -tick=300ms -heartbeat=1s \
  -listen=127.0.0.1:9002 -peers=127.0.0.1:9001
```

Each process reasons about itself independently and logs a
`heard from <peer>` line every time the other's heartbeat arrives.

## Suggested next step: Milestone 3

A small decentralized network — more than two agents, still
peer-to-peer, still no coordinator. The interesting new question:
`-peers` as a hardcoded CLI list doesn't scale past a handful of nodes
typed by hand. What's the simplest mechanism by which an agent could
learn about a peer it was never explicitly told about?
