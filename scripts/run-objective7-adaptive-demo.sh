#!/usr/bin/env bash
# run-adaptive-demo.sh — Phase 1 — Objective 7 visual demo
#
# Launches a small open-chain network where every node has a naturally
# noisy sensor. Half the nodes run with FIXED thresholds (Milestone
# 1-5 behavior), half run with -adaptive (Phase 1 — Objective 7). Watch the
# adaptive nodes settle into fewer false ALERTs than their fixed-
# threshold twins once their own volatility history builds up, and
# watch reputation traces show trust relaxing for quiet peers.
#
# Usage: ./run-adaptive-demo.sh [node-count] [duration-seconds]
#   ./run-adaptive-demo.sh          # 6 nodes (3 fixed / 3 adaptive), 20s
#   ./run-adaptive-demo.sh 8 30     # 8 nodes, 30s

set -euo pipefail

N="${1:-6}"
DURATION="${2:-20}"
BASE_PORT=9100
BIN=/tmp/anw-agent
LOG_DIR=$(mktemp -d)

cd "$(dirname "$0")/../src"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  ANW Phase 1 — Objective 7 — Adaptive Behaviour Demo"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo
echo "  Nodes:            $N (open chain, alternating fixed/adaptive)"
echo "  Duration:         ${DURATION}s"
echo "  Sensor:           moderate, sustained noise with large spikes on every node"
echo "                    (spike-prob=0.12, so fixed-threshold nodes"
echo "                     alarm noticeably more than adaptive ones)"
echo "  Logs:             $LOG_DIR"
echo
echo "Building agent binary..."
go build -o "$BIN" ./cmd/agent
echo

echo "┌─────────────────────────────────────────────────────┐"
echo "│  Launching $N agents                                   │"
echo "└─────────────────────────────────────────────────────┘"

PIDS=()
for i in $(seq 1 "$N"); do
  PORT=$((BASE_PORT + i))
  ID="node-$i"

  if [ $((i % 2)) -eq 0 ]; then
    MODE="-adaptive"
    LABEL="adaptive"
  else
    MODE=""
    LABEL="fixed   "
  fi

  ARGS=(-id="$ID" -tick=200ms -heartbeat=400ms -spike-prob=0.12 -spike-mag=15 \
        -listen="127.0.0.1:${PORT}" -picture-interval=6s)
  if [ -n "$MODE" ]; then
    ARGS+=("$MODE")
  fi
  if [ "$i" -lt "$N" ]; then
    NEXT_PORT=$((BASE_PORT + i + 1))
    ARGS+=(-peers="127.0.0.1:${NEXT_PORT}")
  fi

  "$BIN" "${ARGS[@]}" > "$LOG_DIR/${ID}.log" 2>&1 &
  PIDS+=($!)
  echo "  ✦ Started $ID ($LABEL) on :${PORT} (PID ${PIDS[-1]})"
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
echo "=== ALERT frequency: fixed vs adaptive ==="
for i in $(seq 1 "$N"); do
  ID="node-$i"
  if [ $((i % 2)) -eq 0 ]; then LABEL="adaptive"; else LABEL="fixed   "; fi
  COUNT=$(grep -c "ALERT" "$LOG_DIR/${ID}.log" 2>/dev/null | tr -d '\r' || true)
  : "${COUNT:=0}"
  echo "  $ID ($LABEL): $COUNT tick(s) logged at ALERT"
done

echo
echo "=== Sample adaptive threshold readout (even-numbered nodes) ==="
for i in $(seq 2 2 "$N"); do
  ID="node-$i"
  LINE=$(grep "NETWORK PICTURE" "$LOG_DIR/${ID}.log" 2>/dev/null | tail -1 | tr -d '\r' || echo "")
  if [ -n "$LINE" ]; then
    echo "  $ID: $LINE"
  fi
done

echo
echo "=== Reputation traces observed ==="
grep -h "trust=" "$LOG_DIR"/*.log 2>/dev/null | tail -10 | tr -d '\r' | sed 's/^/  /' || echo "  (none logged yet — try a longer duration)"

echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Full logs: $LOG_DIR"
echo "  Compare fixed vs adaptive ALERT counts: grep -c ALERT $LOG_DIR/*.log"
echo "  Watch memory/reputation over time:      grep 'NETWORK PICTURE' -A5 $LOG_DIR/node-2.log"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
