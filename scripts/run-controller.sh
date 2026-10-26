#!/usr/bin/env bash
# Launch the HyperPot controller fully detached from the calling shell.
set -e
export PATH="/home/linuxbrew/.linuxbrew/bin:$PATH"
BASE=/mnt/newvolume/meta/HyperPot
cd "$BASE/controller"
pkill -f hyperpot-controller 2>/dev/null || true
sleep 1
# Double-detach: setsid + nohup + full FD redirection, then background.
setsid nohup ./hyperpot-controller >/tmp/hp-controller.log 2>&1 </dev/null &
CPID=$!
# Disown and detach from this script entirely.
disown "$CPID" 2>/dev/null || true
echo "controller started pid=$CPID"
