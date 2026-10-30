#!/usr/bin/env bash
# Launch the HyperPod worker fully detached from the calling shell.
set -e
export PATH="/home/linuxbrew/.linuxbrew/bin:$HOME/.cargo/bin:$PATH"
BASE=/mnt/newvolume/meta/hyperpod
export HYPERPOD_LLAMA_CLI="$BASE/test/llama.cpp/build/bin/llama-cli"
export HYPERPOD_MODEL="$BASE/test/models/Llama-3.2-1B-Instruct-Q4_K_M.gguf"
export HYPERPOD_GPU="${HYPERPOD_GPU:-RTX 3050}"
export HYPERPOD_NGL="${HYPERPOD_NGL:-99}"
export HYPERPOD_WORKER_ID="${HYPERPOD_WORKER_ID:-worker-A}"
cd "$BASE/worker"
pkill -f hyperpod-worker 2>/dev/null || true
sleep 1
setsid nohup ./target/release/hyperpod-worker >/tmp/hp-worker.log 2>&1 </dev/null &
WPID=$!
disown "$WPID" 2>/dev/null || true
echo "worker started pid=$WPID"
