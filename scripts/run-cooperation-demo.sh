#!/usr/bin/env bash
# run-cooperation-demo.sh — Milestone 5 visual demo
#
# Launches N agents. One "sick" node has a high spike probability,
# the others are calm. Watch cooperative danger scores propagate
# through the network as trust-weighted peer signals take effect.
#
# Usage: ./run-cooperation-demo.sh [node-count] [duration-seconds]
#   ./run-cooperation-demo.sh          # 5 nodes, 15s
#   ./run-cooperation-demo.sh 8 20     # 8 nodes, 20s

set -euo pipefail

N="${1:-5}"
DURATION="${2:-15}"
BASE_PORT=9000
BIN=/tmp/anw-agent
LOG_DIR=$(mktemp -d)
SICK_NODE=1  # node-1 will be the anomalous one

cd "$(dirname "$0")/../src"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  ANW Milestone 5 — Cooperation Demo"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo
echo "  Nodes:          $N (open chain)"
echo "  Duration:       ${DURATION}s"
echo "  Sick node:      node-${SICK_NODE} (spike-prob=0.8)"
echo "  Healthy nodes:  all others (spike-prob=0.0)"
echo "  Logs:           $LOG_DIR"
echo
echo "Building agent binary..."
go build -o "$BIN" ./cmd/agent
echo

echo "┌─────────────────────────────────────────────────────┐"
echo "│  Launching $N agents (node-${SICK_NODE} is sick)              │"
echo "└─────────────────────────────────────────────────────┘"

PIDS=()
for i in $(seq 1 "$N"); do
  PORT=$((BASE_PORT + i))
  ID="node-$i"

  if [ "$i" -eq "$SICK_NODE" ]; then
    SPIKE_PROB="0.8"
  else
    SPIKE_PROB="0.0"
  fi

  if [ "$i" -lt "$N" ]; then
    NEXT_PORT=$((BASE_PORT + i + 1))
    SEED="127.0.0.1:${NEXT_PORT}"
    "$BIN" -id="$ID" -tick=300ms -heartbeat=500ms -spike-prob="$SPIKE_PROB" \
      -listen="127.0.0.1:${PORT}" -peers="$SEED" \
      > "$LOG_DIR/${ID}.log" 2>&1 &
  else
    "$BIN" -id="$ID" -tick=300ms -heartbeat=500ms -spike-prob="$SPIKE_PROB" \
      -listen="127.0.0.1:${PORT}" \
      > "$LOG_DIR/${ID}.log" 2>&1 &
  fi
  PIDS+=($!)
  if [ "$i" -eq "$SICK_NODE" ]; then
    echo "  ⚠ Started $ID on :${PORT} (PID ${PIDS[-1]}) — SICK (spike-prob=$SPIKE_PROB)"
  else
    echo "  ✦ Started $ID on :${PORT} (PID ${PIDS[-1]})"
  fi
done

echo
echo "  Running for ${DURATION}s..."
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
echo "=== Cooperative Danger Propagation ==="
echo "(Showing last cooperative danger score logged by each node)"
for i in $(seq 1 "$N"); do
  ID="node-$i"
  LAST_COOP=$(grep "coop=" "$LOG_DIR/${ID}.log" 2>/dev/null | tail -1 | tr -d '\r' || echo "")
  if [ -n "$LAST_COOP" ]; then
    if [ "$i" -eq "$SICK_NODE" ]; then
      echo "  ⚠ $ID (SICK): $LAST_COOP"
    else
      echo "  ✦ $ID:         $LAST_COOP"
    fi
  else
    echo "  · $ID: no cooperative scores logged (may not have had peers yet)"
  fi
done

echo
echo "=== Discovery ==="
for i in $(seq 1 "$N"); do
  ID="node-$i"
  COUNT=$(grep -c "discovered new peer" "$LOG_DIR/${ID}.log" 2>/dev/null | tr -d '\r' || echo 0)
  echo "  $ID discovered $COUNT peer(s)"
done

echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Full logs: $LOG_DIR"
echo "  Cooperation: grep 'coop=' $LOG_DIR/*.log | tail -20"
echo "  Trust:       grep 'trust' $LOG_DIR/*.log"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
