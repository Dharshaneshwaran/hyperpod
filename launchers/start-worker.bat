@echo off
REM ==========================================================================
REM  HyperPot - Windows worker launcher (double-click me on the TUF)
REM
REM  Starts BOTH:
REM    1) ggml-rpc-server.exe  (lends this laptop's compute to the pool)
REM    2) hyperpot-worker.exe  (broadcasts presence -> auto-appears in dashboard)
REM
REM  Edit the CONFIG block below to match this laptop, then just double-click.
REM ==========================================================================

REM ---------------- CONFIG (edit these once) ----------------
set "CONTROLLER_IP=192.168.1.20"
set "RPC_PORT=50052"
set "WORKER_ID=worker-TUF"
set "GPU_NAME=RTX 4050"
set "LLAMA_DIR=C:\llama"
set "MODEL=C:\llama\models\Llama-3.2-1B-Instruct-Q4_K_M.gguf"
set "WORKER_EXE=D:\meta\hyperpod\worker\target\release\hyperpot-worker.exe"
REM ----------------------------------------------------------

echo(
echo ==== HyperPot worker launcher (Windows) ====

REM Auto-detect this laptop's IPv4 on the Wi-Fi (first non-loopback IPv4).
for /f "tokens=2 delims=:" %%a in ('ipconfig ^| findstr /r /c:"IPv4.*192\.168\." /c:"IPv4.*10\." /c:"IPv4.*172\."') do (
    if not defined MY_IP set "MY_IP=%%a"
)
REM strip leading space
for /f "tokens=* delims= " %%b in ("%MY_IP%") do set "MY_IP=%%b"
if not defined MY_IP set "MY_IP=127.0.0.1"
echo Detected this laptop IP: %MY_IP%
echo Controller:              http://%CONTROLLER_IP%:8080
echo(

REM 1) Start the RPC server in its own window.
echo Starting ggml-rpc-server on 0.0.0.0:%RPC_PORT% ...
start "HyperPot RPC server" cmd /k "%LLAMA_DIR%\ggml-rpc-server.exe -H 0.0.0.0 -p %RPC_PORT%"

REM give it a moment to bind
timeout /t 2 /nobreak >nul

REM 2) Start the worker (broadcasts presence + serves /run) in its own window.
echo Starting hyperpot-worker ...
set "HYPERPOT_WORKER_ID=%WORKER_ID%"
set "HYPERPOT_CONTROLLER=http://%CONTROLLER_IP%:8080"
set "HYPERPOT_WORKER_ADVERTISE=http://%MY_IP%:9100"
set "HYPERPOT_RPC_ADVERTISE=%MY_IP%:%RPC_PORT%"
set "HYPERPOT_LLAMA_CLI=%LLAMA_DIR%\llama-cli.exe"
set "HYPERPOT_MODEL=%MODEL%"
set "HYPERPOT_GPU=%GPU_NAME%"
start "HyperPot worker" cmd /k "%WORKER_EXE%"

echo(
echo Both started. This laptop should now appear in the HyperPot dashboard.
echo Close the two opened windows to stop.
echo(
pause
