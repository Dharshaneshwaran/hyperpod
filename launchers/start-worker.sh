#!/usr/bin/env bash
# ==========================================================================
#  HyperPod - Linux worker launcher (run me on the ROG / any Linux laptop)
#
#  Starts BOTH:
#    1) ggml-rpc-server  (lends this laptop's compute to the pool)
#    2) hyperpod-worker  (broadcasts presence -> auto-appears in dashboard)
#
#  Usage:  ./start-worker.sh            (uses defaults below)
#          CONTROLLER_IP=192.168.1.20 ./start-worker.sh
# ==========================================================================
set -e

# ---------------- CONFIG (edit if needed) ----------------
BASE="${HYPERPOD_BASE:-/mnt/newvolume/meta/hyperpod}"
CONTROLLER_IP="${CONTROLLER_IP:-127.0.0.1}"
RPC_PORT="${RPC_PORT:-50052}"
WORKER_ID="${WORKER_ID:-worker-ROG}"
GPU_NAME="${GPU_NAME:-RTX 3050}"
MODEL="${MODEL:-$BASE/test/models/Llama-3.2-1B-Instruct-Q4_K_M.gguf}"
# ---------------------------------------------------------

BIN="$BASE/test/llama.cpp/build/bin"
WORKER="$BASE/worker/target/release/hyperpod-worker"

# auto-detect this laptop's LAN IP
MY_IP="$(ip route get 1.1.1.1 2>/dev/null | grep -oP '(?<=src\s)\d+(\.\d+){3}' | head -1)"
MY_IP="${MY_IP:-127.0.0.1}"

echo "==== HyperPod worker launcher (Linux) ===="
echo "this laptop IP : $MY_IP"
echo "controller     : http://$CONTROLLER_IP:8080"
echo

# 1) rpc-server (detached)
pkill -f ggml-rpc-server 2>/dev/null || true
sleep 1
setsid nohup "$BIN/ggml-rpc-server" -H 0.0.0.0 -p "$RPC_PORT" -c >/tmp/hp-rpc.log 2>&1 </dev/null &
disown "$!" 2>/dev/null || true
echo "started ggml-rpc-server on 0.0.0.0:$RPC_PORT (log: /tmp/hp-rpc.log)"
sleep 2

# 2) worker (detached) - broadcasts presence
pkill -f hyperpod-worker 2>/dev/null || true
sleep 1
setsid nohup env \
  HYPERPOD_WORKER_ID="$WORKER_ID" \
  HYPERPOD_CONTROLLER="http://$CONTROLLER_IP:8080" \
  HYPERPOD_WORKER_ADVERTISE="http://$MY_IP:9100" \
  HYPERPOD_RPC_ADVERTISE="$MY_IP:$RPC_PORT" \
  HYPERPOD_LLAMA_CLI="$BIN/llama-cli" \
  HYPERPOD_MODEL="$MODEL" \
  HYPERPOD_GPU="$GPU_NAME" \
  "$WORKER" >/tmp/hp-worker.log 2>&1 </dev/null &
disown "$!" 2>/dev/null || true
echo "started hyperpod-worker (log: /tmp/hp-worker.log)"
echo
echo "This laptop should now appear in the HyperPod dashboard."
echo "Stop with: pkill -f ggml-rpc-server ; pkill -f hyperpod-worker"
