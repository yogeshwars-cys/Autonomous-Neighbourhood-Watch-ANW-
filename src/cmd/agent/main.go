// Command agent runs a single autonomous agent, satisfying Milestone 1:
// internal state, a decision loop, and local reasoning. It has no
// awareness of any other agent — that arrives in Milestone 2.
package main

import (
	"context"
	"flag"
	"os/signal"
	"syscall"
	"time"

	"anw/internal/agent"
)

func main() {
	id := flag.String("id", "node-1", "agent identifier")
	tick := flag.Duration("tick", 500*time.Millisecond, "observation interval")
	spikeProb := flag.Float64("spike-prob", 0.05, "chance per tick of an injected anomaly")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sensor := agent.NewSyntheticSensor(10.0, 1.0, *spikeProb, 8.0)
	a := agent.New(*id, sensor, *tick)

	a.Run(ctx)
}
