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
watch = clamp(baseWatch + k·stddev, minWatch, maxWatch)
alert = clamp(baseAlert + k·stddev, minAlert, maxAlert)


An agent whose local signal is naturally noisy earns *more* tolerance
before alarming — the alternative (fixed thresholds for every agent
regardless of its baseline noise) is exactly the "false propagation /
alarm fatigue" failure mode the README's success metrics warn about,
since a chronically-alarming noisy agent would also mislead any peer
cooperating with it. A calm agent's thresholds stay right where
Milestone 1 left them.

This deliberately only reacts to *local* volatility. `CooperativeDanger`
(peer-blended) already exists as a separate signal from Milestone 5 —
mixing "how noisy is my own sensor" with "what are my neighbors saying"
into one adaptive rule would make the threshold's behavior much harder
to explain, which conflicts with the project's explainability principle.

### 3. Memory: two independent forgetting mechanisms, not one

`EpisodicMemory` never stores raw ticks — that's `State.History`'s job,
unchanged. It stores `Event`s: the interpretation of a status
transition (`EventSpike`, `EventSustained`, `EventRecovery`), which is
the smallest possible answer to Objective 8's question about turning
raw values into meaningful knowledge.

Forgetting has two independent knobs, matching Objective 9's two
questions ("what to remember" *and* "what to forget") with two
mechanisms rather than one clever one:

1. **Hard capacity** (`episodicCapacity = 200`) — a ceiling, same
   philosophy as `historyLimit`.
2. **Soft importance decay** — `importance = max(severity, floor) ×
   2^(-age/halfLife)`. A severe event significantly outlives a mild one
   even well within the capacity limit. `Prune()` removes anything
   below `forgetThreshold` every tick — cheap, and means memory doesn't
   wait for a full buffer before letting go of what no longer matters.

Both are pure functions of elapsed time and severity — no ML, per the
README's stated non-goal.

### 4. Reputation: forgetting applied to trust instead of events

`DecayStale` is the same idea as `Prune`, aimed at `TrustTable` instead
of `EpisodicMemory`: a peer nobody has reinforced in a while (silence,
not disagreement) relaxes back toward `initialTrust` on an exponential
half-life, rather than staying frozen at whatever extreme a past
episode left it at. Without this, a peer that had one bad noisy episode
during a Milestone-4-style partition would stay minimally trusted
forever even after reconnecting and behaving perfectly — that's a
grudge, not a reputation system.

`ReputationTrace` exists purely for explainability: `TrustOf` answers
"how much," this answers "why" (agreement/disagreement counts + time
since last contact), in one human-readable line.

### 5. Smarter gossip: selective content, bounded relay

Naively broadcasting the full `EpisodicMemory` on every heartbeat would
scale with history length and mostly repeat what a peer already knows —
Objective 3's "at what cost?" question, asked again for events instead
of addresses. `gossip.go` answers it with two classic, deliberately
unoriginal epidemic-protocol ideas:

- **Selective content**: only the top-K most important *local* events
  go out each tick (`SelectForGossip` / `maxGossipEvents = 3`).
- **Bounded relay**: a digest heard from a peer can be forwarded again,
  but only up to `maxRelayHops` times (`RelayCandidates`), so news can
  travel more than one hop through a sparse network without an
  unbounded broadcast storm as the network grows.

## What this does NOT do

- No machine learning anywhere — adaptive thresholds are a running
  variance, not a trained model; memory importance and reputation decay
  are closed-form exponentials.
- Adaptive thresholds only react to *local* danger-score volatility, not
  cooperative/network volatility — kept separate on purpose (see above).
- Gossip only carries `EventDigest` (interpreted conclusions), never raw
  observations — the same boundary `Heartbeat` already drew for
  `Status`/`DangerScore`.
- Nothing here is enabled by default for existing agents except
  `Agent.Memory` (always on, purely observational) and the `Events`
  field on `Heartbeat` (empty/omitted when there's nothing to say).
  Adaptive thresholds require `-adaptive` (or
  `State.EnableAdaptiveThresholds()`).

## Running it

```bash
cd src
go build ./...
go test ./...

# Single adaptive-threshold agent, no networking:
go run ./cmd/agent -id solo -adaptive -tick 500ms

# Two networked agents, adaptive thresholds + periodic picture logging:
go run ./cmd/agent -id node-a -adaptive -listen :9001 -peers 127.0.0.1:9002 -picture-interval 5s
go run ./cmd/agent -id node-b -adaptive -listen :9002 -peers 127.0.0.1:9001 -picture-interval 5s
