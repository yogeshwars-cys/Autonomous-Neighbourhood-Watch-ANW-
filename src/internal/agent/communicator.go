package agent

import (
	"context"
	"encoding/json"
	"net"
	"time"
)

// Heartbeat is the entire contents of what one agent ever tells another.
//
// Notice what's NOT here: no Observation, no History. Objective 2 already
// decided raw sensor data is private; a heartbeat carries only the
// agent's *conclusion* (Status, DangerScore), never its evidence. This
// is a real, deliberate answer to Objective 3's question "what should
// agents communicate?" — not an oversight.
//
// KnownPeers is the one addition Milestone 3 makes: alongside the
// agent's own conclusion, it piggybacks its current address book, so
// peer addresses can propagate through the network without ever being
// typed into a config file. This is a real tradeoff — Objective 3's
// "at what cost?" question — trading a slightly larger packet for the
// ability to discover peers nobody told you about directly. It's also
// exactly how production gossip protocols like SWIM piggyback
// membership updates on ordinary pings, rather than running a separate
// discovery channel.
type Heartbeat struct {
	ID          string            `json:"id"`
	Status      string            `json:"status"`
	DangerScore float64           `json:"danger_score"`
	Timestamp   time.Time         `json:"timestamp"`
	KnownPeers  map[string]string `json:"known_peers,omitempty"`

	// SourceAddr is the UDP address a packet actually arrived from. It
	// is set locally by Listen() after receipt and is never part of the
	// wire format (json:"-"). This is deliberate: an agent's reachable
	// address is determined by the network itself, not by anything a
	// peer claims about itself in the message body — a small, early
	// defense against a peer lying about where it lives. (A peer could
	// still lie about OTHER peers in KnownPeers — that's a real gap,
	// left for Objective 5 / the README's "malicious node" experiment.)
	SourceAddr string `json:"-"`
}

// Communicator is an agent's only connection to its peers — the network
// equivalent of Sensor. Keeping it as an interface means Agent.Run never
// needs to know whether it's talking over real UDP, an in-memory fake for
// tests, or some other transport entirely.
type Communicator interface {
	// Listen starts receiving heartbeats from peers in the background and
	// returns a channel of decoded heartbeats. The channel closes when
	// ctx is cancelled.
	Listen(ctx context.Context) <-chan Heartbeat

	// Send delivers a heartbeat to one peer at the given address.
	// UDP is fire-and-forget: a returned nil error means "handed to the
	// OS," not "the peer received it." That gap is intentional — Objective
	// 5 (Resilience) is specifically about what happens when delivery
	// can't be guaranteed, and pretending otherwise now would just have
	// to be undone later.
	Send(peerAddr string, hb Heartbeat) error
}

// UDPCommunicator is a real UDP socket-backed Communicator.
type UDPCommunicator struct {
	conn *net.UDPConn
}

// NewUDPCommunicator binds a UDP socket at listenAddr (e.g. ":9001", or
// "127.0.0.1:0" to let the OS pick a free port — useful in tests).
func NewUDPCommunicator(listenAddr string) (*UDPCommunicator, error) {
	addr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	return &UDPCommunicator{conn: conn}, nil
}

// LocalAddr reports the address actually bound — mainly useful when
// listenAddr used port 0 and the OS picked one for you.
func (c *UDPCommunicator) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *UDPCommunicator) Listen(ctx context.Context) <-chan Heartbeat {
	out := make(chan Heartbeat, 16)
	go func() {
		defer close(out)
		defer c.conn.Close()
		buf := make([]byte, 4096)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// A short read deadline lets this loop notice ctx.Done()
			// promptly instead of blocking forever on a socket read.
			c.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			n, addr, err := c.conn.ReadFromUDP(buf)
			if err != nil {
				continue // timeout, or a transient error — just retry
			}

			var hb Heartbeat
			if err := json.Unmarshal(buf[:n], &hb); err != nil {
				// A malformed packet from a misbehaving or malicious peer
				// is dropped, not trusted. This is the agent's first (very
				// small) act of self-defense — Objective 5 will build on it.
				continue
			}
			// addr is ground truth from the OS/kernel, not a claim inside
			// the packet — this is what makes passive discovery (learning
			// a peer's address just from hearing from them) trustworthy.
			hb.SourceAddr = addr.String()

			select {
			case out <- hb:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (c *UDPCommunicator) Send(peerAddr string, hb Heartbeat) error {
	data, err := json.Marshal(hb)
	if err != nil {
		return err
	}
	return c.sendRaw(peerAddr, data)
}

// sendRaw is unexported and exists only so tests can fire deliberately
// malformed packets at a listener without going through JSON encoding.
func (c *UDPCommunicator) sendRaw(peerAddr string, data []byte) error {
	addr, err := net.ResolveUDPAddr("udp", peerAddr)
	if err != nil {
		return err
	}
	_, err = c.conn.WriteToUDP(data, addr)
	return err
}
