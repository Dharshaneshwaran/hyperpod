#!/usr/bin/env bash
# Launch the HyperPot worker fully detached from the calling shell.
set -e
export PATH="/home/linuxbrew/.linuxbrew/bin:$HOME/.cargo/bin:$PATH"
BASE=/mnt/newvolume/meta/HyperPot
export HYPERPOT_LLAMA_CLI="$BASE/test/llama.cpp/build/bin/llama-cli"
export HYPERPOT_MODEL="$BASE/test/models/Llama-3.2-1B-Instruct-Q4_K_M.gguf"
export HYPERPOT_GPU="${HYPERPOT_GPU:-RTX 3050}"
export HYPERPOT_NGL="${HYPERPOT_NGL:-99}"
export HYPERPOT_WORKER_ID="${HYPERPOT_WORKER_ID:-worker-A}"
cd "$BASE/worker"
pkill -f hyperpot-worker 2>/dev/null || true
sleep 1
setsid nohup ./target/release/hyperpot-worker >/tmp/hp-worker.log 2>&1 </dev/null &
WPID=$!
disown "$WPID" 2>/dev/null || true
echo "worker started pid=$WPID"
