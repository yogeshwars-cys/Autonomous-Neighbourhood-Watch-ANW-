# Milestone 4 — Failure Testing

## What this satisfies

From `PLAN.md`, Milestone 4 requires demonstrating that the decentralized network can:
- **Survive node failures** (a node drops offline permanently)
- **Survive communication failures** (messages are dropped or network links fail)
- **Recover automatically** (a failed node comes back online and is seamlessly reintegrated)

This milestone implements continuous, decentralized liveness tracking across the sparse gossip-converged network. It is tested completely in-memory with parallel processes via standard Go channels and socket closures.

## The core design decision: staleness threshold

We define a **StaleThreshold** (by default set to $6 \times \text{HeartbeatInterval}$, i.e., $6\text{s}$ at default $1\text{s}$ heartbeat). 
If an agent does not receive a heartbeat from a known neighbor within this window:
- It is considered **unreachable** (`☠ peer node-x is UNREACHABLE`).
- Its ID is added to the agent's internal list of dead peers (`Neighbors.DeadPeers()`).

A threshold of $6\text{s}$ is a deliberate trade-off:
- **Too low** (e.g., $1\text{s}$ or $2\text{s}$): A single delayed UDP packet or momentary latency spike triggers a false failure alarm.
- **Too high** (e.g., $30\text{s}$): System remains unaware of partitions or node deaths for too long, delaying responses.

The failure detection loop runs at `StaleThreshold / 2` to satisfy the Nyquist sampling rate and guarantee that state transitions are captured in a timely and responsive manner.

## How recovery works for free

Because `NeighborList.Update()` is called immediately upon receiving any heartbeat from a peer, recovery is inherently automatic. When a node restarts or a network partition heals:
1. The peer resumes sending heartbeats.
2. The receiving agent calls `Update()`, which updates `Neighbor.LastSeen` to `time.Now()`.
3. The next tick of the liveness detector notices `LastSeen` is now fresh, and logs a recovery message (`✓ peer node-x has RECOVERED`), removing it from the dead list.

## Running it

Single test of the failure detection and recovery mechanics:
```bash
cd src
go test ./internal/agent/ -v -run Milestone4
```

To run the entire test suite:
```bash
cd src
go test ./... -v
```

## What this does NOT do — the gap that matters next

Failure detection updates our local view of who is reachable. However, it still does not change how we reason about danger, nor does it resolve network disagreements. If a neighbor goes down while reporting an alert, we simply log the unreachable node, but we do not dynamically adjust our global consensus.

This is the transition to **Objective 4 (Cooperation) / Milestone 5 (Emergent Behaviour)** — allowing active peer reports (and the lack thereof from failed peers) to actively influence our own local danger thresholds.
