# Autonomous Neighbourhood Watch (ANW)

> A research project exploring how decentralized autonomous agents can collaboratively detect abnormal behavior using only local observations and peer-to-peer communication.

---

## Vision

Traditional monitoring systems rely on centralized controllers that collect information, analyze it, and make decisions for the entire network.

Biological systems work differently.

There is no single immune cell responsible for defending the body. Every immune cell observes its local environment, reacts independently, communicates with neighboring cells, and collectively produces an intelligent global response.

Autonomous Neighbourhood Watch explores whether similar principles can be applied to distributed computer systems.

The project is intentionally **not** an Intrusion Detection System.

Instead, it investigates the more fundamental question:

> **Can autonomous agents, using only local information and decentralized communication, develop reliable global situational awareness?**

---

# Research Questions

1. Can autonomous agents detect abnormal local behavior without global knowledge?
2. How should neighboring agents communicate uncertainty?
3. How does information propagate through a decentralized network?
4. How do local decisions become reliable global behavior?
5. How resilient is the system to node failures and network partitions?
6. What simple local rules produce the most stable emergent behavior?

---

# Design Principles

* No central controller
* Local observations only
* Peer-to-peer communication
* Explainable behavior
* Fault tolerance
* Emergent intelligence
* Modular architecture

---

# Non-Goals

This project deliberately avoids:

* Machine Learning
* Large Language Models
* Centralized databases
* Cloud orchestration
* Global monitoring servers
* Signature-based IDS

These may be explored in future projects, but they are **not** required to answer the research questions above.

---

# Initial Architecture

Each autonomous node maintains only local knowledge.

```
Node
 ├── Local State
 ├── Neighbor List
 ├── Danger Score
 ├── Trust Scores
 ├── Event History
 └── Communication Module
```

Each node continuously performs the following loop:

1. Observe local environment.
2. Update internal state.
3. Share relevant information with neighbors.
4. Receive neighbor updates.
5. Adjust confidence.
6. Decide whether action is necessary.
7. Return to observation.

No node has access to complete network state.

---

# Success Metrics

The project is evaluated using system behavior rather than classification accuracy.

Metrics include:

* Detection latency
* Communication overhead
* Message complexity
* Convergence time
* Stability
* False propagation rate
* Partition resilience
* Scalability

---

# Planned Experiments

## Experiment 1

Single anomaly propagation.

## Experiment 2

Multiple simultaneous anomalies.

## Experiment 3

Random node failures.

## Experiment 4

Network partition.

## Experiment 5

Malicious node spreading false alerts.

## Experiment 6

Large-scale topology simulation.

---

# Long-Term Vision

Autonomous Neighbourhood Watch is intended to become the foundational building block for future research into decentralized adaptive systems.

Rather than solving cybersecurity directly, the project focuses on understanding the principles that allow autonomous agents to cooperate without centralized control.

The insights gained here may later be applied to distributed monitoring, cyber defense, autonomous infrastructure management, and other resilient distributed systems.

---

# Repository Status

🚧 Research in progress.
