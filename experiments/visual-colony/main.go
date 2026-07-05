package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"anw/internal/agent"
)

// custom static sensor to cleanly simulate calm/alert states
type StaticSensor struct {
	Value float64
}

func (s *StaticSensor) Read() float64 {
	return s.Value
}

func main() {
	fmt.Println("\033[H\033[2J") // Clear screen
	fmt.Println("Booting Visual Colony...")
	fmt.Println("Waiting 3 seconds for gossip discovery and stabilization...")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const tick = 200 * time.Millisecond
	const heartbeat = 250 * time.Millisecond

	// Create Communicators
	commCenter, _ := agent.NewUDPCommunicator("127.0.0.1:9001")
	commNorth, _ := agent.NewUDPCommunicator("127.0.0.1:9002")
	commSouth, _ := agent.NewUDPCommunicator("127.0.0.1:9003")
	commEast, _ := agent.NewUDPCommunicator("127.0.0.1:9004")
	commWest, _ := agent.NewUDPCommunicator("127.0.0.1:9005")

	addrCenter := commCenter.LocalAddr().String()

	// Initial sensors: all calm (10.0)
	sensCenter := &StaticSensor{Value: 10.0}

	// Create Agents
	center := agent.New("Center", sensCenter, tick).WithNetwork(commCenter, nil, heartbeat)
	north := agent.New("North", &StaticSensor{Value: 10.0}, tick).WithNetwork(commNorth, []string{addrCenter}, heartbeat)
	south := agent.New("South", &StaticSensor{Value: 10.0}, tick).WithNetwork(commSouth, []string{addrCenter}, heartbeat)
	east := agent.New("East", &StaticSensor{Value: 10.0}, tick).WithNetwork(commEast, []string{addrCenter}, heartbeat)
	west := agent.New("West", &StaticSensor{Value: 10.0}, tick).WithNetwork(commWest, []string{addrCenter}, heartbeat)

	// Since we are running all agents in the same process and they all log heavily to stdout by default,
	// the logs will clash with our TUI. The agent logs inside act(), checkNeighborLiveness(), etc.
	// Since we can't easily turn off log.Printf without modifying the agent, we will just let it clear the screen
	// fast enough, or we can just accept some flicker, but standard log package can be muted.
	// We'll mute standard logger for the TUI to be perfectly clean.
	importLog := true
	_ = importLog // Just ensuring log is not required if we don't use it.
	
	// Actually, let's just mute the standard logger output.
	log.SetOutput(io.Discard)
	
	agents := []*agent.Agent{center, north, south, east, west}

	var wg sync.WaitGroup
	for _, a := range agents {
		wg.Add(1)
		go func(ag *agent.Agent) {
			defer wg.Done()
			ag.Run(ctx)
		}(a)
	}

	// Wait 3 seconds for network to converge and become completely calm
	time.Sleep(3 * time.Second)

	// Inject danger at the center
	sensCenter.Value = 100.0

	// UI Loop
	uiTicker := time.NewTicker(200 * time.Millisecond)
	defer uiTicker.Stop()

	for {
		select {
		case <-uiTicker.C:
			drawUI(center, north, south, east, west)
		}
	}
}

func getStatusEmoji(status string) string {
	switch status {
	case "CALM":
		return "🟢"
	case "WATCHING":
		return "🟡"
	case "ALERT":
		return "🔴"
	default:
		return "⚪"
	}
}

func getAvgTrust(a *agent.Agent) float64 {
	if a.Trust == nil {
		return 0.5
	}
	all := a.Trust.All()
	if len(all) == 0 {
		return 0.5 // initialTrust
	}
	sum := 0.0
	for _, t := range all {
		sum += t
	}
	return sum / float64(len(all))
}

func drawUI(c, n, s, e, w *agent.Agent) {
	var sb strings.Builder

	// Clear terminal and move to top-left
	sb.WriteString("\033[H\033[2J")
	
	sb.WriteString("=== Experiment 12: Visual Colony ===\n")
	sb.WriteString("Only the Center node senses danger.\n")
	sb.WriteString("Watch peer influence change the Status of the outer nodes.\n")
	sb.WriteString("(Press Ctrl+C to stop)\n\n")

	// Draw the visual star topology
	sb.WriteString(fmt.Sprintf("       %s \n", getStatusEmoji(n.State.Status.String())))
	sb.WriteString("       | \n")
	sb.WriteString(fmt.Sprintf(" %s —— %s —— %s \n", getStatusEmoji(w.State.Status.String()), getStatusEmoji(c.State.Status.String()), getStatusEmoji(e.State.Status.String())))
	sb.WriteString("       | \n")
	sb.WriteString(fmt.Sprintf("       %s \n\n", getStatusEmoji(s.State.Status.String())))

	// Draw the metrics table
	sb.WriteString(fmt.Sprintf("%-9s %-9s %-9s %-9s %s\n", "Node", "Danger", "Coop", "TrustAvg", "Status"))
	sb.WriteString("--------------------------------------------------\n")

	agents := []*agent.Agent{c, n, s, e, w}
	for _, a := range agents {
		danger := a.State.DangerScore
		coop := a.State.CooperativeDanger
		trust := getAvgTrust(a)
		status := a.State.Status.String()

		sb.WriteString(fmt.Sprintf("%-9s %-9.2f %-9.2f %-9.2f %s\n", a.State.ID, danger, coop, trust, status))
	}

	fmt.Print(sb.String())
}
