// Command agent runs a single autonomous agent. With no networking
// flags, this is exactly the Milestone 1 agent. Pass -listen and -peers
// to also broadcast and receive heartbeats over real UDP — Milestone 2.
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"anw/internal/agent"
)

func main() {
	id := flag.String("id", "node-1", "agent identifier")
	tick := flag.Duration("tick", 500*time.Millisecond, "observation interval")
	spikeProb := flag.Float64("spike-prob", 0.05, "chance per tick of an injected anomaly")

	listen := flag.String("listen", "", "UDP address to listen on for peer heartbeats, e.g. :9001 (empty = networking disabled)")
	peers := flag.String("peers", "", "comma-separated SEED peer UDP addresses (just a bootstrap — more peers are discovered automatically via gossip), e.g. 127.0.0.1:9002")
	heartbeat := flag.Duration("heartbeat", time.Second, "how often to broadcast a heartbeat to known peers")

	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sensor := agent.NewSyntheticSensor(10.0, 1.0, *spikeProb, 8.0)
	a := agent.New(*id, sensor, *tick)

	if *listen != "" {
		comm, err := agent.NewUDPCommunicator(*listen)
		if err != nil {
			log.Fatalf("failed to start networking on %s: %v", *listen, err)
		}

		var peerList []string
		if *peers != "" {
			peerList = strings.Split(*peers, ",")
		}

		a.WithNetwork(comm, peerList, *heartbeat)
		log.Printf("[%s] networking enabled: listening on %s, peers=%v", *id, *listen, peerList)
	}

	a.Run(ctx)
}
