#!/usr/bin/env bash
# Launch the controller in SUPERVISOR mode (Path A) fully detached.
# Splits the model across the RPC workers listed in HYPERPOD_RPC_WORKERS.
set -e
export PATH="/home/linuxbrew/.linuxbrew/bin:$PATH"
BASE=/mnt/newvolume/meta/hyperpod
export HYPERPOD_LLAMA_SERVER="$BASE/test/llama.cpp/build/bin/llama-server"
export HYPERPOD_MODEL="$BASE/test/models/Llama-3.2-1B-Instruct-Q4_K_M.gguf"
export HYPERPOD_SERVER_HOST="127.0.0.1"
export HYPERPOD_SERVER_PORT="9931"
export HYPERPOD_NGL="99"
# Comma list of ggml-rpc-server endpoints to split across. Override as needed.
export HYPERPOD_RPC_WORKERS="${HYPERPOD_RPC_WORKERS:-127.0.0.1:50052}"
cd "$BASE/controller"
pkill -f hyperpod-controller 2>/dev/null || true
sleep 1
setsid nohup ./hyperpod-controller >/tmp/hp-controller.log 2>&1 </dev/null &
disown "$!" 2>/dev/null || true
echo "supervisor controller started pid=$! workers=$HYPERPOD_RPC_WORKERS"
