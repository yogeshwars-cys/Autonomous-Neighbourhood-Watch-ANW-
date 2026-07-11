// Command agent runs a single autonomous agent. With no networking
// flags, this is exactly the Milestone 1 agent. Pass -listen and -peers
// to also broadcast and receive heartbeats over real UDP — Milestone 2.
// Pass -events to additionally classify semantic events (Milestone 6).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
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
	maxTicks := flag.Int("max-ticks", 0, "run for exactly this many ticks and then shut down (0 = run forever)")

	listen := flag.String("listen", "", "UDP address to listen on for peer heartbeats, e.g. :9001 (empty = networking disabled)")
	peers := flag.String("peers", "", "comma-separated SEED peer UDP addresses (just a bootstrap — more peers are discovered automatically via gossip), e.g. 127.0.0.1:9002")
	heartbeat := flag.Duration("heartbeat", time.Second, "how often to broadcast a heartbeat to known peers")

	adaptive := flag.Bool("adaptive", false, "Objective 7: enable volatility-driven adaptive watch/alert thresholds instead of fixed ones")
	pictureInterval := flag.Duration("picture-interval", 0, "Milestone 5 Extended: how often to log this agent's NetworkPicture (0 = disabled)")

	events := flag.Bool("events", false, "Milestone 6 / Objective 8: enable semantic event detection (Normal/Abnormal, Seen/Unseen classification)")
	eventLogPath := flag.String("event-log", "", "Milestone 6: file path to dump this agent's EventLog to as JSON on shutdown (empty = don't dump)")
	verboseEvents := flag.Bool("verbose-events", false, "Milestone 6: log a detailed FeatureVector-included line every tick an event is active, in addition to the concise Explain() summary")
	learningTicks := flag.Int("learning-ticks", 0, "Milestone 6: number of initial ticks during which unrecognized shapes are captured as baseline patterns instead of flagged as novel_pattern (0 = no learning phase)")

	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *maxTicks > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*maxTicks)*(*tick))
		defer cancel()
	}

	sensor := agent.NewSyntheticSensor(10.0, 1.0, *spikeProb, 8.0)
	a := agent.New(*id, sensor, *tick)

	if *adaptive {
		a.State.EnableAdaptiveThresholds()
		log.Printf("[%s] adaptive thresholds enabled", *id)
	}
	if *pictureInterval > 0 {
		a.PictureInterval = *pictureInterval
	}

	if *events {
		a.WithEvents()
		a.VerboseEvents = *verboseEvents
		if *learningTicks > 0 {
			a.WithLearningPhase(*learningTicks)
			log.Printf("[%s] learning phase: %d ticks", *id, *learningTicks)
		}
		log.Printf("[%s] semantic event detection enabled (verbose=%v)", *id, *verboseEvents)
	}
	if *eventLogPath != "" {
		a.EventLogPath = *eventLogPath
	}

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

	if a.EventLogPath != "" {
		dumpEventLog(*id, a.EventLogPath, a.State.EventLog)
	}
}

// dumpEventLog writes an agent's EventLog to disk as JSON on shutdown —
// the -event-log flag's implementation. A best-effort convenience for
// post-run inspection (e.g. by scripts/run-events-demo.sh), not part of
// the agent's own reasoning: a failure here is logged, never fatal.
func dumpEventLog(id, path string, log_ []agent.Event) {
	data, err := json.MarshalIndent(log_, "", "  ")
	if err != nil {
		log.Printf("[%s] could not marshal event log: %v", id, err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Printf("[%s] could not write event log to %s: %v", id, path, err)
		return
	}
	log.Printf("[%s] wrote %d event(s) to %s", id, len(log_), path)
}
