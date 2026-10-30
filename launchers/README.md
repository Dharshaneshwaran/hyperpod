# HyperPod Launchers (no-typing demo start)

Double-click / one-command launchers so laptops join the pool without typing
commands. Auto-discovery means workers appear in the dashboard automatically.

## On the ROG (Linux) — the demo / controller machine

1. **Start the controller:**
   ```
   bash launchers/start-controller.sh
   ```
   Prints the controller URL (e.g. http://192.168.1.20:8080) and the dashboard
   command. Discovery (UDP :50999) is ON; big tier splits over auto-discovered
   workers (no manual IPs).

2. **Start the dashboard** (another terminal; use the IP it printed):
   ```
   cd frontend
   VITE_CONTROLLER=http://192.168.1.20:8080 npm run dev
   ```
   Open the printed http://localhost:5173 in a browser.

3. **(Optional) make the ROG also a worker:**
   ```
   CONTROLLER_IP=192.168.1.20 bash launchers/start-worker.sh
   ```

## On the TUF (Windows) — a pool laptop

1. Edit `launchers/start-worker.bat` CONFIG block once (CONTROLLER_IP, paths).
2. **Double-click `start-worker.bat`.** It opens two windows:
   - `ggml-rpc-server` (lends compute)
   - `hyperpod-worker` (broadcasts presence -> auto-appears in dashboard)
3. First time only, allow UDP 50999 + TCP 50052/9100 through Windows Firewall:
   ```
   netsh advfirewall firewall add rule name="HyperPod Discovery 50999" dir=in action=allow protocol=UDP localport=50999
   netsh advfirewall firewall add rule name="HyperPod RPC 50052" dir=in action=allow protocol=TCP localport=50052
   netsh advfirewall firewall add rule name="HyperPod Worker 9100" dir=in action=allow protocol=TCP localport=9100
   ```

## The demo flow
1. Run controller + dashboard on the ROG.
2. Double-click the launcher on each pool laptop -> they appear as cards.
3. Ask a question (small tier = fast 1B; big tier = 3B split across the pool).
4. Kill a laptop (close its windows / Wi-Fi) -> dashboard shows it go red; the
   big-tier pipeline rebuilds over survivors and keeps serving.

## Stop
- Linux: `pkill -f ggml-rpc-server ; pkill -f hyperpod-worker ; pkill -f hyperpod-controller`
- Windows: close the opened windows.
