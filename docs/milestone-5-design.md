# Milestone 5 — Emergent Behaviour (Cooperation)

## What this satisfies

PLAN.md's Milestone 5 requires: cooperative decision making, stable
information propagation, and observable system-level intelligence.

Through Milestones 1–4, a comment in `agent.go` has been honest
about what's missing:

> Note what still doesn't happen here: a.State is never touched. A
> neighbor's danger score still cannot influence this agent's own —
> that remains Objective 4 (Cooperation), not yet built.

This milestone closes that gap. For the first time, a peer's alarm
can change what this agent *does* — not just what it knows about the
peer.

## The core design decision: trust-weighted cooperative scoring

Every sense tick, after computing its local `DangerScore`, a networked
agent now calls `cooperate()`, which:

1. Builds a `[]PeerSignal` from its `NeighborList`, using
   `IsAlive(id, StaleThreshold)` to mark each peer live or dead.
2. Calls `Cooperate(localDanger, peers, trustTable)`, which:
   - Computes a trust-weighted average of all live peers' danger scores.
   - Blends it with the local score: `LocalWeight×local + (1-LocalWeight)×peerSignal`.
   - Reinforces trust for each live peer based on agreement/disagreement.
3. Stores the result in `State.CooperativeDanger`.
4. Re-evaluates `Status` against the cooperative score, not the raw local one.

### Why trust is asymmetric

Trust is easier to lose than to earn (`trustLoss=0.05` vs
`trustGain=0.02`). This is a deliberate design choice: a peer that
lies once should cost more credibility than one honest report earns.
A minimum trust floor (`minTrust=0.05`) prevents complete deafness —
even the least-trusted peer still contributes a tiny signal.

### Why LocalWeight is 0.6

An agent's own sensor is its most reliable source of information.
Setting `LocalWeight=0.6` means peer signals can influence but never
override local reasoning. A single alarmed peer can raise cooperative
danger from 0 to at most 0.4 — enough to push the agent into WATCHING
but not into ALERT on its own.

## What emerges

The headline result: **an agent whose own local sensor is perfectly
calm will still see its CooperativeDanger rise — and its Status
potentially change — when a trusted neighbor is alarmed.** This is
intelligence emerging from cooperation, not from any single agent's
local sensing. `TestCooperativeAlertPropagation` proves this in an
integration test with real UDP agents.

## What this does NOT do

- **No consensus protocol.** Agents don't vote or agree on a shared
  state. Each agent independently blends its local view with whatever
  peer signals it has. Disagreement is natural and expected.
- **No reputation gossip.** Trust scores are never shared. How much
  I trust you is private — Objective 5's "malicious node" experiment
  depends on trust being locally computed, not globally imposed.
- **No adaptive thresholds.** The watch/alert thresholds are still
  fixed constants. Making them responsive to network conditions is
  Objective 7 (Adaptive Behaviour).

## Running it

Unit tests for the cooperation module:
```
cd src
go test ./internal/agent/ -v -run "TestTrust|TestCooperative|TestCooperate|TestEmergent|TestSnapshot|TestExplain|TestAverage"
```

Integration tests proving emergent behavior:
```
cd src
go test ./internal/agent/ -v -run "TestCooperativeAlert|TestTrustErosion"
```

A real multi-process visual demo:
```
./scripts/run-cooperation-demo.sh 5 15
```

## Suggested next step

The network now cooperates. The natural next research question from
PLAN.md's Objective 5: what happens when a node deliberately lies?
Can the trust system detect and isolate a malicious peer that spreads
false alarms? `minTrust=0.05` was set with this experiment in mind —
a liar's influence should shrink but never fully disappear, creating
an observable signal that something is wrong.
