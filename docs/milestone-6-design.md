# Milestone 6 — Semantic Events (Objective 8: Semantic Perception)

## What this satisfies

PLAN_2.md's Objective 8 asks three questions: "How should observations
be represented? What is an event? How can raw sensor values become
meaningful knowledge?" — and names three deliverables: an Event model,
semantic observations, and event classification. Milestone 6 delivers
all three: `event.go`'s taxonomy, `feature.go`'s shape extraction, and
`detector.go`'s two-stage classifier, tied into the live agent loop in
`agent.go` and `decision.go`.

Through Milestone 5, an agent's entire internal picture of "how worried
should I be" was one float: `DangerScore`. That number can't distinguish
a sensor ramping steadily upward from one flapping wildly between high
and low — two operationally different situations that can produce an
identical score. Milestone 6 asks the obvious next question: worried
about **what**?

## The core design decision: a hierarchy, not a flat enum

```
Event
├── Normal
└── Abnormal
    ├── Seen    (SustainedSpike, Oscillation, GradualDrift, Plateau, SuddenDrop)
    └── Unseen  (NovelPattern)
```

The Seen/Unseen split is the actual cognitive leap. Every anomaly
pipeline before this one implicitly treated "abnormal" as a single
bucket. Splitting it forces the agent to be honest about the difference
between "this is a shape of trouble I have a name for" and "this is
trouble, and I genuinely don't know what kind." `NovelPattern` is scored
as the **most** dangerous classification, not the least — unknown is
scarier than known, because a rule-based system has no basis to bound
the risk of a pattern it has no template for.

Deliberately still true after this milestone: no ML anywhere. Every
classification is a deterministic function of a handful of arithmetic
features (`feature.go`) run through hand-written pattern templates
(`pattern.go`). Nothing is learned; everything is explainable by
construction.

## Two-stage classification

**Stage A (Normal vs. Abnormal)** reuses `decision.go`'s existing
`watchThreshold` rather than inventing a second, differently-tuned
cutoff — it's asking exactly the question `Status` already answers.

**Stage B (Seen vs. Unseen)** runs a `FeatureVector` — Delta, Slope,
Variance, Duration, ZeroCrossings, MaxDeviation, IsRising/IsFalling/
IsStable — through five ordered pattern templates (`pattern.go`). First
match wins; the order itself is a design decision (fast-moving shapes
checked before slow ones, so a still-unfolding spike isn't mistaken for
a plateau evaluated a tick too late). No match at all means
`NovelPattern`, confidence 1.0 — the detector is certain it doesn't
recognize the shape, which is itself a confident conclusion.

A deliberate departure from a literal reading of "extract features from
`Observation.Value`": every feature is computed on the
**baseline-relative** signal — the same quantity `decision.go` already
uses for `DangerScore` — not raw sensor units. This keeps a deviation of
5 meaning the same thing whether baseline is 10 or 1000, and lets
`pattern.go`'s thresholds reuse `watchThreshold`/`alertThreshold`
directly instead of inventing a second, differently-scaled set of
magnitude cutoffs.

## Event lifecycle

```
nil       -> Detected   (first abnormal tick)
Detected  -> Confirmed  (matches >= confirmTicks)
Confirmed -> Active     (matches >= activeTicks)
{Detected,Confirmed,Active} -> Decaying  (tick returns to Normal)
Decaying  -> Active     (anomaly resumes before cooldown completes)
Decaying  -> Resolved   (normalStreak >= cooldownTicks)
```

A status-weight table (`statusWeight`, `event.go`) scales each event
type's base severity by how far along its lifecycle it is — a
`Detected` `SustainedSpike` (one noisy tick) does not carry the same
weight as an `Active` one (confirmed, ongoing). `Resolved` contributes
zero: a resolved event is history, not a current danger.

## Renaming the old `Event` to `Episode`

Objective 7's groundwork (originally in `pending_memory.go`/
`pending_gossip.go`) had already introduced a small, three-kind
`Event`/`EventKind`/`EventDigest` set (Spike/Sustained/Recovery) as an
early, un-featured stand-in for "semantic perception," before this
milestone had a real taxonomy to build. Milestone 6 needed the name
`Event` for the real thing, so the original type — and everything that
used it — was renamed to `Episode`/`EpisodeKind`/`EpisodeDigest`
(files renamed to `episodic_memory.go`/`episode_gossip.go` to match).
This is not just a find-and-replace for its own sake: the two concepts
are genuinely different memories, and the rename makes that visible in
the code, not just in a comment.

- **Event** (`event.go`) — what's happening **right now**: a live,
  evolving classification with its own lifecycle. At most one is
  "current" at a time (`State.ActiveEvent`).
- **Episode** (`episodic_memory.go`) — what's **worth remembering**
  after the fact: a bounded, importance-ranked log entry.

The two are connected, not merely adjacent: when an `Event` transitions
to `Resolved`, `Agent.recordMemory` (`agent.go`) folds it into a
permanent `Episode` — "a lived moment becomes a memory once it's over."
The `Episode`'s remembered severity uses the event **type's** base
weight (`eventDangerWeights[resolved.Type]`), not the resolved status
weight (which is deliberately zero) — a resolved `NovelPattern` should
still be remembered as having mattered more than a resolved `Plateau`.

## Event-driven danger scoring, without breaking Milestones 1-5

PLAN_2's spec calls for "the final DangerScore is the maximum active
event score, or 0 if no active events." Implemented literally, that
would throw away `decision.go`'s existing EMA-based decay the moment
*any* event, however mild, became active — directly conflicting with
the requirement two lines later that "DangerScore must still decay back
to 0 when events resolve."

The actual implementation (`updateEvents`, `detector.go`) folds the
event's `DangerWeight()` into `DangerScore` via **max**, not
replacement: `if w := ActiveEvent.DangerWeight(); w > DangerScore {
DangerScore = w }`. A low-weight `Detected` event never *lowers* the
agent's existing reading; a high-weight `Active`/`NovelPattern` event
can still raise it above what the raw EMA alone would say. Once an
event resolves, `statusWeight(Resolved) == 0` stops contributing
anything, and the EMA's own decay (unmodified since Milestone 1) takes
back over. This satisfies the spec's actual intent — events can escalate
danger — without regressing the decay guarantee Milestones 1-5 already
depend on.

## Cooperative events: event-type agreement outweighs score agreement

`Heartbeat` gained one field: `ActiveEvent *EventSummary` — type,
confidence, duration, nothing else (no `FeatureVector`, no raw
`Observation`), the same conclusion-only boundary every other field on
`Heartbeat` already drew. `Neighbor` gained two mirrored fields
(`ActiveEventType`, `ActiveEventConfidence`) so a peer's last-announced
event survives past the heartbeat that carried it.

`cooperate()` (`agent.go`) now calls `CooperateWithEvent` instead of
`Cooperate`, passing this agent's own current event type alongside the
usual peer signals. `TrustTable.ReinforceWithEvent` layers an
**additional, larger** trust adjustment on top of the existing
score-gap reinforcement: `eventAgreementBonus`/`eventDisagreementPenalty`
are set to `2×trustGain`/`2×trustLoss` specifically so that two agents
independently classifying the exact same named pattern counts as
stronger evidence than their raw `DangerScore`s merely landing close
together — a categorical match is much less likely to happen by chance
than two numbers being nearby. Silence (either side reporting no active
event) is explicitly **not** treated as disagreement — an agent that
hasn't classified anything abnormal has no opinion to conflict with,
not a dissenting one.

`Cooperate`'s signature and behavior are byte-for-byte unchanged
(it's now `CooperateWithEvent(..., selfEventType="")` under the hood) —
every Milestone 1-5 call site, and `cooperation_test.go`'s exact-delta
assertions, keep working without modification.

## Explainability

`Event.Explain()` produces the two formats PLAN_2's spec calls for —
a "Seen" event names its recognized pattern and the features that
mattered for that pattern; an "Unseen" event says plainly that nothing
matched, and shows the raw shape descriptors instead, since there's no
template name to point to. `State.Explain()` (`decision.go`) appends
this after the existing status/danger/baseline line rather than
replacing it, so an agent without events enabled — or one that's
currently Normal — produces exactly the Milestone 1-5 output, untouched.

## What's deliberately NOT here yet

- **No machine learning.** Every threshold in `pattern.go` is a named
  constant with a one-line justification, not a fitted parameter.
- **No cross-event correlation.** Each agent tracks at most one
  `ActiveEvent` at a time. Two simultaneous, independent anomalies on
  the same sensor are not distinguished from each other — a real gap,
  left for whichever future objective asks "can an agent be worried
  about two different things at once?"
- **No event-type gossip beyond the current one.** `EventLog` is local;
  only the live `ActiveEvent` summary crosses the network. Sharing a
  history of past classifications with peers would be a natural
  extension of the existing `EpisodeDigest` gossip mechanism, not a new
  one — deliberately not built until a research question asks for it.
- **The trust gap from Milestone 3 remains.** A malicious peer can
  still claim any event type it likes; `ReinforceWithEvent` rewards
  agreement and penalizes conflict, but nothing here verifies a peer's
  claim against independent evidence. Still Objective 5 / the README's
  "malicious node" experiment.

## Running it

Unit tests for the taxonomy, feature extraction, pattern templates, and
lifecycle:
```
cd src
go test ./internal/agent/ -v -run "TestEvent|TestDangerScoreFromEvent|TestDangerWeight|TestFeatureExtraction|TestPattern|TestClassifyStageA"
```

Integration tests — the full sensor -> feature -> event -> heartbeat ->
cooperation pipeline, over real UDP:
```
cd src
go test ./internal/agent/ -v -run "TestSensorToEvent|TestEventsDisabled|TestEventPropagation|TestCooperativeEvent|TestExplainWith"
```

Everything, including Milestones 1-5's existing suite (unmodified and
still green):
```
cd src
go test ./...
```

A single agent, watching its own classifications live:
```
go run ./cmd/agent -id solo -events -verbose-events -tick 300ms -spike-prob 0.3
```

A real multi-process network, one erratic node and several calm ones,
with a results summary at the end:
```
./scripts/run-events-demo.sh 5 15
```

## Suggested next step: Milestone 7 (Memory)

PLAN_2.md's Objective 9 is next: "What should an agent remember? What
should it forget?" `EpisodicMemory` already answers a first-pass version
of this for status transitions and — as of this milestone — resolved
semantic events. The open research question Milestone 7 inherits: now
that memories are typed and richer (a `Plateau` is a categorically
different memory than a `SustainedSpike`, not just a different
`DangerScore`), should importance decay (`importance()`,
`episodic_memory.go`) depend on event *type*, not just severity and
age — should a resolved `NovelPattern` be forgotten more slowly than an
equally-severe but well-understood `SuddenDrop`, on the theory that the
unfamiliar is worth remembering longer?
