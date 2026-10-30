#!/usr/bin/env bash
# ==========================================================================
#  HyperPod - controller launcher (run me on the ROG / the demo machine)
#
#  Starts the Go controller in supervisor mode with:
#    - small tier (Llama 3.2 1B) + big tier (Llama 3.2 3B, split across pool)
#    - LAN auto-discovery ON (workers appear automatically, no manual IPs)
#  Then prints how to start the dashboard.
# ==========================================================================
set -e
export PATH="/home/linuxbrew/.linuxbrew/bin:$PATH"

BASE="${HYPERPOD_BASE:-/mnt/newvolume/meta/hyperpod}"
export HYPERPOD_LLAMA_SERVER="$BASE/test/llama.cpp/build/bin/llama-server"
export HYPERPOD_MODEL="$BASE/test/models/Llama-3.2-3B-Instruct-Q4_K_M.gguf"       # BIG tier
export HYPERPOD_SMALL_MODEL="$BASE/test/models/Llama-3.2-1B-Instruct-Q4_K_M.gguf" # SMALL tier
export HYPERPOD_SERVER_HOST="127.0.0.1"
export HYPERPOD_SERVER_PORT="9931"
export HYPERPOD_SMALL_PORT="9932"
export HYPERPOD_NGL="99"
export HYPERPOD_CTX="2048"
# NOTE: no HYPERPOD_RPC_WORKERS -> the big tier splits over AUTO-DISCOVERED workers.

MY_IP="$(ip route get 1.1.1.1 2>/dev/null | grep -oP '(?<=src\s)\d+(\.\d+){3}' | head -1)"
MY_IP="${MY_IP:-127.0.0.1}"

cd "$BASE/controller"
pkill -f hyperpod-controller 2>/dev/null || true
sleep 1
setsid nohup ./hyperpod-controller >/tmp/hp-controller.log 2>&1 </dev/null &
disown "$!" 2>/dev/null || true

echo "==== HyperPod controller started ===="
echo "controller API : http://$MY_IP:8080   (workers should point here)"
echo "log            : /tmp/hp-controller.log"
echo "discovery      : ON (UDP :50999) - workers auto-appear"
echo
echo "Next: start the dashboard in another terminal:"
echo "    cd $BASE/frontend"
echo "    VITE_CONTROLLER=http://$MY_IP:8080 npm run dev"
echo "Then open the printed http://localhost:5173 in a browser."
