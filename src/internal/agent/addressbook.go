package agent

// AddressBook is local knowledge of HOW TO REACH other agents — a third
// kind of local knowledge, distinct from both State (what I know about
// myself) and NeighborList (what I currently believe about a peer's
// reasoning). An ID can appear here before it ever appears in
// NeighborList: knowing where to find someone and having actually heard
// from them are different facts. That gap is exactly what makes gossip
// discovery possible — an agent can learn a peer's address secondhand,
// through a third party, before any direct contact ever happens.
type AddressBook struct {
	addrs map[string]string // ID -> "host:port"
}

func NewAddressBook() *AddressBook {
	return &AddressBook{addrs: make(map[string]string)}
}

// Add registers or refreshes how to reach a peer. The bool return is
// true only the first time a given ID is added — callers use this to
// log discovery events distinctly from routine re-confirmation of an
// address already known.
func (a *AddressBook) Add(id, addr string) bool {
	_, existed := a.addrs[id]
	a.addrs[id] = addr
	return !existed
}

// Get returns the known address for one peer, if any.
func (a *AddressBook) Get(id string) (string, bool) {
	addr, ok := a.addrs[id]
	return addr, ok
}

// All returns a snapshot copy of every known ID -> address mapping.
// A copy, not the live map, because this is handed straight to
// json.Marshal when gossiping — callers should never be able to mutate
// an agent's address book through a value it merely shared.
func (a *AddressBook) All() map[string]string {
	out := make(map[string]string, len(a.addrs))
	for k, v := range a.addrs {
		out[k] = v
	}
	return out
}

func (a *AddressBook) Count() int {
	return len(a.addrs)
}
