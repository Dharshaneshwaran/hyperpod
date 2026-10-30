#!/usr/bin/env bash
# Launch a local ggml-rpc-server (for single-machine split testing) fully detached.
set -e
BASE=/mnt/newvolume/meta/hyperpod
BIN="$BASE/test/llama.cpp/build/bin/ggml-rpc-server"
HOST="${1:-127.0.0.1}"
PORT="${2:-50052}"
pkill -f ggml-rpc-server 2>/dev/null || true
sleep 1
setsid nohup "$BIN" -H "$HOST" -p "$PORT" >/tmp/hp-rpc.log 2>&1 </dev/null &
disown "$!" 2>/dev/null || true
echo "rpc-server started pid=$! on $HOST:$PORT"
