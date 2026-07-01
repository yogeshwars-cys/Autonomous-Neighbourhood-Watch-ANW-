#!/usr/bin/env bash
# run-failure-test.sh — Milestone 4 visual demo
#
# Launches N agents in an open chain, waits for gossip convergence,
# then KILLS the middle node and watches the survivors detect the
# failure and (optionally) recover when a replacement starts.
#
# Usage: ./run-failure-test.sh [node-count] [duration-seconds]
#   ./run-failure-test.sh          # 5 nodes, 20s
#   ./run-failure-test.sh 7 30     # 7 nodes, 30s

set -euo pipefail

N="${1:-5}"
DURATION="${2:-20}"
BASE_PORT=9000
BIN=/tmp/anw-agent
LOG_DIR=$(mktemp -d)
KILL_NODE=$(( (N + 1) / 2 ))   # middle node

cd "$(dirname "$0")/../src"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  ANW Milestone 4 — Failure Testing Demo"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo
echo "  Nodes:          $N (open chain)"
echo "  Duration:       ${DURATION}s"
echo "  Kill target:    node-${KILL_NODE} (middle of chain)"
echo "  Kill at:        ~$(( DURATION / 3 ))s"
echo "  Restart at:     ~$(( DURATION * 2 / 3 ))s"
echo "  Logs:           $LOG_DIR"
echo
echo "Building agent binary..."
go build -o "$BIN" ./cmd/agent
echo

# ── Phase 1: Launch all nodes ────────────────────────────
echo "┌─────────────────────────────────────────────────────┐"
echo "│  Phase 1: LAUNCHING $N agents                       │"
echo "└─────────────────────────────────────────────────────┘"

PIDS=()
for i in $(seq 1 "$N"); do
  PORT=$((BASE_PORT + i))
  ID="node-$i"

  if [ "$i" -lt "$N" ]; then
    NEXT_PORT=$((BASE_PORT + i + 1))
    SEED="127.0.0.1:${NEXT_PORT}"
    "$BIN" -id="$ID" -tick=300ms -heartbeat=500ms \
      -listen="127.0.0.1:${PORT}" -peers="$SEED" \
      > "$LOG_DIR/${ID}.log" 2>&1 &
  else
    "$BIN" -id="$ID" -tick=300ms -heartbeat=500ms \
      -listen="127.0.0.1:${PORT}" \
      > "$LOG_DIR/${ID}.log" 2>&1 &
  fi
  PIDS+=($!)
  echo "  ✦ Started $ID on :${PORT} (PID ${PIDS[-1]})"
done

KILL_TIME=$(( DURATION / 3 ))
RESTART_TIME=$(( DURATION * 2 / 3 ))
echo
echo "  Waiting ${KILL_TIME}s for gossip convergence..."
sleep "$KILL_TIME"

# ── Phase 2: Kill the middle node ────────────────────────
echo
echo "┌─────────────────────────────────────────────────────┐"
echo "│  Phase 2: KILLING node-${KILL_NODE}                          │"
echo "└─────────────────────────────────────────────────────┘"

KILL_IDX=$(( KILL_NODE - 1 ))
KILLED_PID=${PIDS[$KILL_IDX]}
kill "$KILLED_PID" 2>/dev/null || true
echo "  ☠ Killed node-${KILL_NODE} (PID $KILLED_PID)"
echo
echo "  Waiting $(( RESTART_TIME - KILL_TIME ))s for failure detection..."
sleep $(( RESTART_TIME - KILL_TIME ))

# ── Phase 3: Restart a replacement node ──────────────────
echo
echo "┌─────────────────────────────────────────────────────┐"
echo "│  Phase 3: RESTARTING node-${KILL_NODE} (recovery test)       │"
echo "└─────────────────────────────────────────────────────┘"

RESTART_PORT=$((BASE_PORT + 100 + KILL_NODE))
# Seed the replacement to an immediate neighbor so it can re-enter
if [ "$KILL_NODE" -gt 1 ]; then
  NEIGHBOR_PORT=$((BASE_PORT + KILL_NODE - 1))
else
  NEIGHBOR_PORT=$((BASE_PORT + KILL_NODE + 1))
fi

"$BIN" -id="node-${KILL_NODE}" -tick=300ms -heartbeat=500ms \
  -listen="127.0.0.1:${RESTART_PORT}" -peers="127.0.0.1:${NEIGHBOR_PORT}" \
  > "$LOG_DIR/node-${KILL_NODE}-restarted.log" 2>&1 &
RESTART_PID=$!
PIDS[$KILL_IDX]=$RESTART_PID
echo "  ✓ Restarted node-${KILL_NODE} on :${RESTART_PORT} (PID $RESTART_PID)"
echo
echo "  Waiting $(( DURATION - RESTART_TIME ))s for recovery propagation..."
sleep $(( DURATION - RESTART_TIME ))

# ── Cleanup ──────────────────────────────────────────────
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

echo "=== Discovery (gossip convergence before kill) ==="
for i in $(seq 1 "$N"); do
  ID="node-$i"
  COUNT=$(grep -c "discovered new peer" "$LOG_DIR/${ID}.log" 2>/dev/null || echo 0)
  echo "  $ID discovered $COUNT peer(s)"
done

echo
echo "=== ☠ Failure Detection (who noticed node-${KILL_NODE} went down?) ==="
for i in $(seq 1 "$N"); do
  if [ "$i" -eq "$KILL_NODE" ]; then continue; fi
  ID="node-$i"
  DETECTED=$(grep -c "UNREACHABLE" "$LOG_DIR/${ID}.log" 2>/dev/null || echo 0)
  if [ "$DETECTED" -gt 0 ]; then
    FIRST=$(grep "UNREACHABLE" "$LOG_DIR/${ID}.log" | head -1)
    echo "  ☠ $ID detected failure ($DETECTED events)"
    echo "    └─ $FIRST"
  else
    echo "  · $ID did NOT detect the failure (may not have known node-${KILL_NODE})"
  fi
done

echo
echo "=== ✓ Recovery Detection (who noticed node-${KILL_NODE} came back?) ==="
for i in $(seq 1 "$N"); do
  if [ "$i" -eq "$KILL_NODE" ]; then continue; fi
  ID="node-$i"
  RECOVERED=$(grep -c "RECOVERED" "$LOG_DIR/${ID}.log" 2>/dev/null || echo 0)
  if [ "$RECOVERED" -gt 0 ]; then
    FIRST=$(grep "RECOVERED" "$LOG_DIR/${ID}.log" | head -1)
    echo "  ✓ $ID detected recovery"
    echo "    └─ $FIRST"
  else
    echo "  · $ID did NOT detect recovery (may not have received heartbeats yet)"
  fi
done

echo
echo "=== Restarted node-${KILL_NODE} re-discovery ==="
RESTART_LOG="$LOG_DIR/node-${KILL_NODE}-restarted.log"
if [ -f "$RESTART_LOG" ]; then
  COUNT=$(grep -c "discovered new peer" "$RESTART_LOG" 2>/dev/null || echo 0)
  echo "  node-${KILL_NODE} (restarted) re-discovered $COUNT peer(s)"
  grep "discovered new peer" "$RESTART_LOG" 2>/dev/null | while read -r line; do
    echo "    └─ $line"
  done
else
  echo "  (no restart log found)"
fi

echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Full logs: $LOG_DIR"
echo "  Inspect:   cat $LOG_DIR/node-1.log"
echo "  Failure:   grep '☠\\|UNREACHABLE' $LOG_DIR/*.log"
echo "  Recovery:  grep '✓\\|RECOVERED' $LOG_DIR/*.log"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
