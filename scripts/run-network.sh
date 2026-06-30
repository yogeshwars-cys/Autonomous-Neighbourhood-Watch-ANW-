#!/usr/bin/env bash
# run-network.sh — launches N real agent processes in a deliberately
# SPARSE, OPEN chain topology (node-1 -> node-2 -> ... -> node-N, with
# NO wraparound back to node-1) so you can watch gossip-based discovery
# converge a sparse bootstrap into a connected network on its own.
#
# Why a chain and not a ring or full mesh: if every node were seeded
# with every other node, "discovery" would be meaningless — everyone
# would already know everyone from the command line. A chain is the
# minimum topology where most pairs of nodes have NEVER been told about
# each other directly, so any address they learn proves gossip did
# real work.
#
# Usage: ./run-network.sh <node-count> [duration-seconds]
#   ./run-network.sh 5        # 5 nodes, 15s (default)
#   ./run-network.sh 8 30     # 8 nodes, 30s

set -euo pipefail

N="${1:-5}"
DURATION="${2:-15}"
BASE_PORT=9000
BIN=/tmp/anw-agent
LOG_DIR=$(mktemp -d)

cd "$(dirname "$0")/../src"

echo "Building agent binary..."
go build -o "$BIN" ./cmd/agent

echo "Launching $N agents in an OPEN chain (node-1 -> node-2 -> ... -> node-$N) for ${DURATION}s"
echo "Logs: $LOG_DIR"
echo

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
    # The last node in the chain starts with NO seed at all. It only
    # becomes reachable once node-(N-1) contacts it — proving an agent
    # doesn't need to initiate contact to eventually become known
    # network-wide.
    "$BIN" -id="$ID" -tick=300ms -heartbeat=500ms \
      -listen="127.0.0.1:${PORT}" \
      > "$LOG_DIR/${ID}.log" 2>&1 &
  fi
  PIDS+=($!)
done

sleep "$DURATION"

echo "Stopping all $N agents..."
kill "${PIDS[@]}" 2>/dev/null || true
sleep 1

echo
echo "=== Discovery summary (out of $((N - 1)) possible peers per node) ==="
for i in $(seq 1 "$N"); do
  ID="node-$i"
  COUNT=$(grep -c "discovered new peer" "$LOG_DIR/${ID}.log" 2>/dev/null || echo 0)
  echo "  $ID discovered $COUNT peer(s)"
done

echo
echo "=== Sample of HOW discovery happened (direct vs gossip) ==="
grep -h "discovered new peer" "$LOG_DIR"/*.log | sort | head -20

echo
echo "Full logs kept at: $LOG_DIR"
echo "(inspect any node's full log with: cat $LOG_DIR/node-1.log)"
