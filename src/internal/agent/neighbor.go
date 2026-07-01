package agent

import "time"

// Neighbor is everything one agent knows about ANOTHER agent. Compare
// this to State: an agent's own State has a full observation history and
// a continuously-updated baseline. A Neighbor has none of that — just
// whatever the peer last chose to announce, and when. That asymmetry
// between "what I know about myself" and "what I know about others" is
// Objective 2's whole point, made visible in two different struct shapes.
type Neighbor struct {
	ID          string
	Status      string
	DangerScore float64
	LastSeen    time.Time
}

// NeighborList is local knowledge about peers — never a global view of
// the network. An agent only knows about peers that have actually sent
// it a heartbeat, and only as recently as their last one.
type NeighborList struct {
	peers map[string]*Neighbor
}

func NewNeighborList() *NeighborList {
	return &NeighborList{peers: make(map[string]*Neighbor)}
}

// Update folds in a heartbeat, overwriting whatever this agent previously
// believed about that peer. There is no merging or averaging — the most
// recent heartbeat simply replaces the old belief, because a stale belief
// about a peer is worse than no belief at all.
func (n *NeighborList) Update(hb Heartbeat) {
	n.peers[hb.ID] = &Neighbor{
		ID:          hb.ID,
		Status:      hb.Status,
		DangerScore: hb.DangerScore,
		LastSeen:    hb.Timestamp,
	}
}

// All returns every known neighbor. Order is not guaranteed.
func (n *NeighborList) All() []*Neighbor {
	out := make([]*Neighbor, 0, len(n.peers))
	for _, nb := range n.peers {
		out = append(out, nb)
	}
	return out
}

// Get returns what's known about one specific peer, if anything.
func (n *NeighborList) Get(id string) (*Neighbor, bool) {
	nb, ok := n.peers[id]
	return nb, ok
}

// DeadPeers returns the IDs of neighbors that haven't been seen within threshold.
func (n *NeighborList) DeadPeers(threshold time.Duration) []string {
	var dead []string
	now := time.Now()
	for id, nb := range n.peers {
		if now.Sub(nb.LastSeen) > threshold {
			dead = append(dead, id)
		}
	}
	return dead
}

// AlivePeers returns the IDs of neighbors that are still considered alive.
func (n *NeighborList) AlivePeers(threshold time.Duration) []string {
	var alive []string
	now := time.Now()
	for id, nb := range n.peers {
		if now.Sub(nb.LastSeen) <= threshold {
			alive = append(alive, id)
		}
	}
	return alive
}

// IsAlive checks if a specific peer is considered alive.
func (n *NeighborList) IsAlive(id string, threshold time.Duration) bool {
	nb, ok := n.peers[id]
	if !ok {
		return false
	}
	return time.Since(nb.LastSeen) <= threshold
}

// Count returns the number of tracked neighbors.
func (n *NeighborList) Count() int {
	return len(n.peers)
}

