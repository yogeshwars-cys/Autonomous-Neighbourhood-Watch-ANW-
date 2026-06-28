# Milestone 1 — One Autonomous Agent

## What this satisfies

From PLAN.md, Milestone 1 requires: internal state, a decision loop, and
local reasoning. This is implemented in `src/internal/agent/` and run via
`src/cmd/agent/`.

There is exactly one agent. It has never heard of another agent. That's
intentional — Objective 3 (Communication) and Objective 4 (Cooperation)
don't exist yet, and bolting on "neighbor awareness" early would make it
impossible to later tell which behaviors come from local reasoning alone
versus from peer influence.

## Design decisions, and why

**Sensor is an interface, not a network socket.**
You chose real network communication (UDP, separate processes) for
Milestone 2 onward. But Objective 1's question is "how does an agent
*perceive* its environment" — not "how does it talk to peers." Keeping
`Sensor` as a one-method interface (`Read() float64`) means the perception
boundary and the *future* communication boundary are two different
seams in the code. When Milestone 2 adds a `Communicator`, it won't need
to touch `Sensor`, `State`, or the decision loop at all.

**Danger score is continuous; Status is discrete, with two thresholds.**
A single threshold (anomaly / not anomaly) would make the agent flip
status on every noisy reading near the boundary. Two thresholds
(`watchThreshold`, `alertThreshold`) create a middle "WATCHING" state —
the agent's own way of saying "I'm not sure yet." This is a primitive
form of the uncertainty Objective 3's research questions ask about
("how should neighboring agents communicate uncertainty?") — except here
there's no neighbor to tell, so it just sits in local state.

**Baseline adapts; danger score decays.**
Two separate research questions are bundled in "what counts as normal":
- *Drift*: the environment's true mean can legitimately move over time.
  `Baseline` follows it via an exponential moving average.
- *Forgiveness*: even a real anomaly shouldn't keep the agent alarmed
  forever once it passes. `DangerScore` decays back toward 0 independently
  of baseline. Without this, the agent could only ever escalate, never
  de-escalate — which would make "ALERT" meaningless as a status, since
  it would be a one-way door.

**The first observation sets the baseline instead of being judged against it.**
An agent with one data point has no basis for calling that point unusual.
Treating it as instantly normal avoids a false alarm on startup —
covered by `TestFirstObservationSetsBaselineWithoutAlarm`.

**History is bounded (`historyLimit = 50`).**
Objective 2 asks "what is local knowledge?" — part of the answer is that
it isn't infinite. An agent that remembers every observation forever is
modeling a database, not a resource-constrained autonomous node. Bounding
memory now also makes Objective 7 (forgetting, as a deliberate mechanism)
a real design problem later instead of something solved by accident.

**`Explain()` exists because "explainable behavior" is a stated design
principle in README.md, not an afterthought.** Every status the agent
reaches has a one-line, inspectable justification. This matters more once
there are multiple agents and a human (you) needs to debug *why* the
network ended up in some emergent state — Objective 6's whole premise
depends on being able to trace local decisions back to local reasoning.

## What's deliberately NOT here yet

- No neighbors, no messages, no trust — Milestone 2 / Objective 3.
- No machine learning, per the README's non-goals.
- No notion of "attack" or "intrusion" anywhere in the domain model —
  `Observation` is just a float. This keeps the system honest to the
  stated non-goal: this is not an IDS, it's a substrate for studying
  decentralized agent behavior that happens to be motivated by one.

## Running it

```
cd src
go test ./...                                    # verify local reasoning
go run ./cmd/agent -tick=300ms -spike-prob=0.1    # watch one agent reason live
```

## Suggested next step: Milestone 2

Two agents, real UDP sockets, heartbeats, and neighbor awareness — without
the `Sensor`/`State`/decision code above needing to change. A
`Communicator` interface (mirroring `Sensor`) is the natural shape for
this, with the open research question being *what specifically gets sent*
in a heartbeat: the raw `Observation`? The derived `DangerScore`? Just
`Status`? Each answer trades off bandwidth against how much reasoning the
receiving agent gets to redo locally — which is itself one of Objective 3's
questions ("what should agents communicate?").
