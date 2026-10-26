# HyperPot — HANDOFF (resume here tomorrow)

_Last session: 26 Oct 2026, ~23:50. Everything below is committed + pushed to_
_`https://github.com/Dharshaneshwaran/hyperpod` (latest commit: Phase 2 recovery)._

---

## 1. One-line status
Core is DONE and VERIFIED: one Llama model splits across ROG + TUF over Wi-Fi,
and when a laptop is killed the controller detects it (~2s) and automatically
rebuilds over the survivor and keeps serving. The "kill a laptop, AI survives"
demo works on real hardware.

## 2. What is DONE (verified, not assumed)
- Phase 0: kill-test — proved plain llama.cpp RPC CRASHES on node loss.
  Evidence: `test/killtest2.log`, docs section 12.
- Phase 1: Go controller + Rust worker + llama.cpp; verified single-machine AND
  cross-machine (ROG controller <-> TUF worker over Wi-Fi).
  Evidence: `test/phase1-crossmachine.log`.
- Phase 2 (Path A): real model split across ROG+TUF + AUTOMATIC RECOVERY.
  Evidence: `test/phase2-recovery-VERIFIED.log` (shows gen1 -> node DOWN -> gen2 REBUILT).

## 3. What is LEFT (in priority order)
1. **Rubric strategy (HIGH VALUE for winning).** See `docs/STRATEGY_rubric_TODO.md`.
   - Impact (30%) and creative Meta use (25%) are our weak areas = 55% of score.
   - Decide the lead beneficiary (school/clinic/field-site) + problem statement.
   - Decide the creative Meta angle (e.g. local Llama + Meta Model API fallback).
   - OPEN QUESTIONS to answer: (a) do we have Meta Model API $150 credits?
     (b) which beneficiary is most genuine?
   - WATCH OUT (anti-projects list): do NOT pitch as "AI tutor" or "medical tool".
     Pitch as INFRASTRUCTURE; school/clinic is only the demo setting.
2. **Demo polish.** Script the live "kill a laptop" demo; RECORD a backup video
   (never rely on live Wi-Fi in front of judges). Rubric Demo = 25%.
3. **Stretch (only if time):** seamless token-resume instead of restart+rerun.
   High risk, parked. See `docs/PHASE2_design_pathA.md`.

## 4. HOW TO RUN IT (exact steps to reproduce the working demo)

### Hardware / IPs
- ROG (Linux, RTX 3050) = controller + llama-server. Wi-Fi IP `192.168.1.20`.
- TUF (Windows, RTX 4050) = rpc-server. Wi-Fi IP `192.168.1.38`.
- Both must be on the SAME Wi-Fi (phone hotspot is most reliable).

### On the TUF (Windows) — just one command, NO git pull needed
```
C:\llama\ggml-rpc-server.exe -H 0.0.0.0 -p 50052
```
(The TUF only runs this llama.cpp binary; our code lives on the ROG.)

### On the ROG (Linux) — run the verification demo
```
# confirm TUF reachable
timeout 5 bash -c 'cat </dev/null >/dev/tcp/192.168.1.38/50052' && echo UP

# run the kill-recovery demo (kill the TUF when it prints the banner)
bash /tmp/verify-recovery.sh
```
Expect log: gen1 split over TUF -> "RPC worker ... is DOWN" -> gen2 "REBUILT
over survivors" -> prompt after recovery answered -> "TRUE RECOVERY VERIFIED".

NOTE: if `/tmp/verify-recovery.sh` is gone (temp), it is reproduced from
`scripts/run-supervisor.sh` logic; env vars are documented in that script.

## 5. KEY TECHNICAL FACTS (so we don't re-learn them)
- Build tools installed on ROG via **linuxbrew** (no sudo): cmake, go. Rust via rustup.
- llama.cpp built at `test/llama.cpp/build/bin/` with `-DGGML_CUDA=ON -DGGML_RPC=ON`.
  Binaries: `llama-cli`, `llama-server`, `ggml-rpc-server`.
- Model: `test/models/Llama-3.2-1B-Instruct-Q4_K_M.gguf` (small on purpose).
- Controller supervisor mode env: HYPERPOT_LLAMA_SERVER, HYPERPOT_MODEL,
  HYPERPOT_RPC_WORKERS (comma host:port), HYPERPOT_SERVER_PORT=9931, HYPERPOT_CTX=2048.
- RECOVERY DESIGN: `controller/supervisor.go`
  - `remoteHeavySplit()` gives remotes a 4:1 share so a dead node truly breaks things.
  - `monitorWorkers()` TCP-probes active RPC workers every 2s; on death it rebuilds
    llama-server over survivors (this is what makes recovery fire deterministically).
- GOTCHA: this sandbox SIGTERMs backgrounded processes when the spawning command
  returns. Always run multi-step tests as ONE self-contained script.
- Recovery = restart pipeline + RE-RUN request (short pause). NOT seamless token
  resume (that is the parked stretch goal).

## 6. REPO MAP
```
controller/   Go  — main.go (API+registry+recovery), supervisor.go (llama-server supervisor)
worker/       Rust — src/main.rs (register, heartbeat, /run, runs llama-cli)
scripts/      run-controller.sh, run-worker.sh, run-rpc-server.sh, run-supervisor.sh
test/         llama.cpp build (gitignored), model (gitignored), evidence *.log
docs/         HyperPot_v1/Simplified/*.tex+pdf, STRATEGY_rubric_TODO.md,
              PHASE2_design_pathA.md, this handoff
PROTOCOL.md   controller<->worker message format
```

## 7. SUGGESTED FIRST MOVE TOMORROW
Start with the rubric strategy conversation (Impact + Meta use) — it is the
highest-leverage thing left for actually winning, and it is thinking/decisions,
not heavy coding. Answer the two open questions in section 3.1 first.
