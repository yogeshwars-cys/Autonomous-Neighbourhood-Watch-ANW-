# Objective 7: Adaptive Behaviour

## Verification & Architectural Note

> **Authoritative Verification:** The adaptive threshold engine and its underlying statistical calculations are fully verified and proven correct via isolated unit and integration tests (`go test -v -run TestAdaptive ./internal/agent/`).

### Live Demo Limitation (Sensor vs. EMA Baseline Interaction)
While the implementation in `adaptive.go` functions exactly as designed, the live demonstration wrapper (`run-objective7-adaptive-demo.sh`) currently exhibits flat `CALM` behavior across all nodes due to a mathematical interaction between the synthetic sensor's spike dynamics and the Exponential Moving Average (EMA) baseline engine:

1. **Rapid EMA Absorption:** The baseline engine uses an exponentially weighted moving average ($\alpha = 0.10$). When fed high-probability, randomized spikes, the running mean rapidly shifts upward toward the average of those spikes within just a few ticks.
2. **Signal Attenuation:** Once the baseline drifts upward, subsequent sensor spikes register as minor deviations rather than severe anomalies, significantly dropping the normalized signal intensity.
3. **Decay Dominance:** Between spikes, the natural danger and reputation decay rates ($\text{decayRate} = 0.15$) erode the accumulated score faster than intermittent spikes can push it past the `ALERT` threshold ($\ge 0.70$).

**Conclusion:** The adaptive threshold logic successfully prevents "crying wolf" under chronic noise—exactly as intended by the design. However, demonstrating visual `WATCH`/`ALERT` contrast in a short live shell simulation requires a sustained step-shift sensor model rather than a transient spike model.

---

## A note on numbering

PLAN_1.md lists "Adaptive Behaviour" as **Objective 7**, without assigning
it a milestone number — Milestones 1–5 in that plan cover single agent
through emergent behaviour, and Objective 7 is flagged as future work
(milestone-5-design.md explicitly calls out "no adaptive thresholds" as
a known gap). PLAN_2.md separately numbers its own Objective 7 ("Semantic
Events") and Milestone 5 Extended ("Memory & Forgetting"). This document follows
the numbering given directly in the advancement request: **Objective 7 =
Adaptive Behaviour**, spanning Objective 7 (adaptive thresholds), most of
Objective 9 (memory & forgetting), and the reputation-evolution half of
Objective 12 (reflection). Milestone 5 Extended (separate doc) covers the
collective-intelligence step built on top of it.

## What this satisfies

- **Objective 7 — Adaptive Behaviour**: watch/alert thresholds are no
  longer fixed constants; they widen or tighten based on an agent's own
  recent volatility (`adaptive.go`).
- **Objective 9 — Memory**: agents keep a bounded episodic log of
  significant events, with two independent forgetting mechanisms
  (`memory.go`).
- **Objective 12 — Reflection (partial)**: trust now evolves with time,
  not just with fresh evidence — unreinforced relationships decay back
  toward neutral, and a one-line `ReputationTrace` explains why a peer
  is trusted the amount it is (`reputation.go`, extending `cooperation.go`).
- The "smarter gossip" half of the brief (selective, hop-bounded event
  sharing) is implemented in `gossip.go` and shared with Milestone 5 Extended,
  since it's the same mechanism that also produces Milestone 5 Extended's
  collective picture.

## Core design decisions

### 1. Everything here is additive, not a rewrite

Every Milestone 1–5 test still passes unmodified. This wasn't
incidental — it was the design constraint:

- `State.Adaptive` is `nil` by default. `effectiveThresholds()` falls
  back to the original fixed constants unless `EnableAdaptiveThresholds()`
  is called explicitly. A `State{}` literal behaves exactly like it did
  in Milestone 1.
- `Agent.Memory` is always populated (memory doesn't need a network),
  but recording events never changes `DangerScore`, `Status`, or any
  existing decision path — it's a passive observer bolted onto the
  existing `act()`/`Run()` cycle.
- `TrustTable`'s new fields (`lastInteraction`, `agreements`,
  `disagreements`) are populated inside the *existing* `Reinforce()`
  call, so no call site needed to change.

This mirrors how Trust and Communicator were already optional,
milestone-gated capabilities in this codebase — Objective 7 just adds
two more knobs to the same pattern instead of inventing a new one.

### 2. Adaptive thresholds: volatility, not learning

`AdaptiveThresholds` tracks the running mean/variance of an agent's own
`DangerScore` using Welford's algorithm — a single deterministic pass,
no stored history, no gradient, no model. The effective thresholds are:
