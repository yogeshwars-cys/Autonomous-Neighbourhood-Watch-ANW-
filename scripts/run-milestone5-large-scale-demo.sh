#!/usr/bin/env bash
# run-collective-demo.sh — Phase 1 — Milestone 5 Extended visual demo
#
# Launches a larger open-chain network (default 12 nodes) with two
# "sick" nodes at opposite ends and everyone else calm and adaptive.
# Each node periodically logs its own NetworkPicture — watch news of
# an incident at one end of the chain reach nodes several hops away
# that never talked to the origin directly, bounded by maxRelayHops so
# it doesn't flood forever. This is the long-running-stability /
# larger-network scenario the Milestone 6/7 advancement asked for.
#
# Usage: ./run-collective-demo.sh [node-count] [duration-seconds]
#   ./run-collective-demo.sh          # 12 nodes, 60s
#   ./run-collective-demo.sh 20 120   # 20 nodes, 2 minutes

set -euo pipefail

N="${1:-12}"
DURATION="${2:-60}"
BASE_PORT=9200
BIN=/tmp/anw-agent
LOG_DIR=$(mktemp -d)
SICK_A=1          # one end of the chain
SICK_B="$N"       # the other end — several hops away from SICK_A

cd "$(dirname "$0")/../src"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  ANW Phase 1 — Milestone 5 Extended — Collective Intelligence Demo"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo
echo "  Nodes:            $N (open chain — deliberately sparse, not"
echo "                    fully connected, so multi-hop relay matters)"
echo "  Duration:         ${DURATION}s"
echo "  Sick nodes:       node-${SICK_A} and node-${SICK_B} (opposite ends)"
echo "  Everyone else:    calm, adaptive thresholds enabled"
echo "  Picture interval: every 8s per node"
echo "  Logs:             $LOG_DIR"
echo
echo "Building agent binary..."
go build -o "$BIN" ./cmd/agent
echo

echo "┌─────────────────────────────────────────────────────┐"
echo "│  Launching $N agents in an open chain                 │"
echo "└─────────────────────────────────────────────────────┘"

PIDS=()
for i in $(seq 1 "$N"); do
  PORT=$((BASE_PORT + i))
  ID="node-$i"

  if [ "$i" -eq "$SICK_A" ] || [ "$i" -eq "$SICK_B" ]; then
    SPIKE_PROB="0.15"
    LABEL="SICK"
  else
    SPIKE_PROB="0.0"
    LABEL="calm"
  fi

  ARGS=(-id="$ID" -tick=150ms -heartbeat=300ms -spike-prob="$SPIKE_PROB" \
        -adaptive -listen="127.0.0.1:${PORT}" -picture-interval=8s)
  if [ "$i" -lt "$N" ]; then
    NEXT_PORT=$((BASE_PORT + i + 1))
    ARGS+=(-peers="127.0.0.1:${NEXT_PORT}")
  fi

  "$BIN" "${ARGS[@]}" > "$LOG_DIR/${ID}.log" 2>&1 &
  PIDS+=($!)
  echo "  · Started $ID ($LABEL) on :${PORT} (PID ${PIDS[-1]})"
done

echo
echo "  Running for ${DURATION}s — long enough for several"
echo "  picture-interval snapshots and multi-hop relay to occur..."
sleep "$DURATION"

echo
echo "Stopping all agents..."
for pid in "${PIDS[@]}"; do
  kill "$pid" 2>/dev/null || true
done
sleep 1

# ── Summary ──────────────────────────────────────────────
echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  RESULTS"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

echo
echo "=== Did news of node-${SICK_A}'s incident reach the far end (node-${SICK_B})? ==="
FAR_LOG="$LOG_DIR/node-${SICK_B}.log"
if grep -q "node-${SICK_A}" "$FAR_LOG" 2>/dev/null; then
  echo "  ✦ YES — node-${SICK_B}'s picture mentions node-${SICK_A}:"
  grep "node-${SICK_A}" "$FAR_LOG" | tail -3 | tr -d '\r' | sed 's/^/    /'
else
  echo "  · Not yet — try a longer duration or fewer nodes-in-between"
  echo "    hops for relay to cross (bounded by maxRelayHops)."
fi

echo
echo "=== Sample NetworkPicture from a middle node ==="
MID=$(( (N + 1) / 2 ))
MID_LOG="$LOG_DIR/node-${MID}.log"
if [ -f "$MID_LOG" ]; then
  grep "NETWORK PICTURE" -A 6 "$MID_LOG" 2>/dev/null | tail -8 | tr -d '\r' | sed 's/^/  /' || echo "  (no picture logged yet in this window)"
fi

echo
echo "=== Discovery activity across the network ==="
for i in $(seq 1 "$N"); do
  ID="node-$i"
  DISCOVERED=$(grep -c "discovered new peer" "$LOG_DIR/${ID}.log" 2>/dev/null | tr -d '\r' || true)
  : "${DISCOVERED:=0}"
  echo "  $ID discovered $DISCOVERED peer(s)"
done

echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Full logs: $LOG_DIR"
echo "  Full pictures over time: grep 'NETWORK PICTURE' -A6 $LOG_DIR/node-${MID}.log"
echo "  Trace one incident's spread: grep 'node-${SICK_A}' $LOG_DIR/*.log"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
