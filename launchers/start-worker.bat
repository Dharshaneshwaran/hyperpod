@echo off
REM ==========================================================================
REM  HyperPod - Windows worker launcher (double-click me on the TUF)
REM
REM  Starts BOTH:
REM    1) ggml-rpc-server.exe  (lends this laptop's compute to the pool)
REM    2) hyperpod-worker.exe  (broadcasts presence -> auto-appears in dashboard)
REM
REM  Edit the CONFIG block below to match this laptop, then just double-click.
REM ==========================================================================

REM ---------------- CONFIG (edit these once) ----------------
REM Only a starting point. The controller answers this worker's broadcast with
REM its own address, so a wrong value here corrects itself within a few seconds
REM and does not need editing when the controller changes network.
set "CONTROLLER_IP=192.168.1.20"
set "RPC_PORT=50052"
set "WORKER_ID=worker-%COMPUTERNAME%"
set "LLAMA_DIR=C:\llama-cuda"
set "MODEL=C:\llama\models\Llama-3.2-1B-Instruct-Q4_K_M.gguf"
set "WORKER_EXE=%~dp0..\worker\target\release\hyperpod-worker.exe"
REM ----------------------------------------------------------

echo(
echo ==== HyperPod worker launcher (Windows) ====

REM The worker decides which of this machine's addresses the controller can
REM reach, by asking the OS for the route. Guessing the first IPv4 from
REM ipconfig picked Wi-Fi while Ethernet was the reachable path.
echo Controller:              http://%CONTROLLER_IP%:8080
echo(

REM 1) Start the RPC server in its own window.
echo Starting ggml-rpc-server on 0.0.0.0:%RPC_PORT% ...
start "HyperPod RPC server" cmd /k "%LLAMA_DIR%\ggml-rpc-server.exe -H 0.0.0.0 -p %RPC_PORT% -c"

REM give it a moment to bind
timeout /t 2 /nobreak >nul

REM 2) Start the worker (broadcasts presence + serves /run) in its own window.
echo Starting hyperpod-worker ...
set "HYPERPOD_WORKER_ID=%WORKER_ID%"
set "HYPERPOD_CONTROLLER=http://%CONTROLLER_IP%:8080"
set "HYPERPOD_RPC_PORT=%RPC_PORT%"
set "HYPERPOD_LLAMA_CLI=%LLAMA_DIR%\llama-cli.exe"
set "HYPERPOD_MODEL=%MODEL%"
start "HyperPod worker" cmd /k "%WORKER_EXE%"

echo(
echo Both started. This laptop should now appear in the HyperPod dashboard.
echo Close the two opened windows to stop.
echo(
pause
