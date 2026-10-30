# HyperPod Handoff

Last updated: 30 October 2026.
Repository: https://github.com/Dharshaneshwaran/hyperpod

## Current verified state

- Canonical name: **HyperPod** / `hyperpod`.
- Controller: Go; worker: Rust; inference/device offload: llama.cpp; UI: React.
- Trusted LAN discovery, registration and recurring heartbeats work across Linux
  and Windows.
- Telemetry schema v1 reports nullable NVIDIA GPU/VRAM/load/temperature/power,
  system RAM and battery/AC measurements.
- Scheduler uses measured free VRAM minus 512 MiB/device. It never derives
  capacity from a model name; missing measurements invoke an explicit
  `llama.cpp-auto` fallback.
- The dashboard shows worker telemetry, scheduler weights and placement parsed
  from llama.cpp's own load log.
- Local tests: Go 7 tests; Rust 5 tests; strict Rust clippy; Go vet/build; React
  production build. React lint has one non-blocking set-state-in-effect warning.
- CI is intentionally out of scope by user request; tests remain local.

## Latest ROG/TUF measurement

Worker telemetry before model load:

- TUF Windows, RTX 4050: 5,843 / 6,141 MiB VRAM; 50°C; 4.09 W;
  4,845 / 16,003 MiB RAM; battery 84%; AC online.
- ROG Linux, RTX 3050: 3,716 / 4,096 MiB VRAM at scheduling time.

Scheduler:

```text
TUF RPC:   5843 - 512 = 5331
ROG local: 3716 - 512 = 3204
--tensor-split 5331,3204
```

Actual Llama 3.2 3B placement:

```text
CPU_Mapped                         308 MiB
CUDA0 (ROG RTX 3050)              842 MiB
RPC0[192.168.1.5:50052] (TUF)   1076 MiB
29/29 layers; split=true; ready in 60 seconds
```

Evidence: `test/phase5-telemetry-scheduler.log`, commit `8354e7f`.

## Earlier verified evidence

- Native llama.cpp RPC failure: `test/killtest2.log` and LaTeX evidence section.
- Cross-machine Phase 1: `test/phase1-crossmachine.log`.
- Supervisor/recovery development: `test/phase2-*`, `test/phase3-*`.
- Real 3B CUDA split and recovery: `test/phase4-real-split-cuda.log`,
  `test/phase4-kill-test.log`.
- Llama 3.1 8B capacity split: `test/phase4-8b-split.log`.

## Important claim boundaries

- HyperPod is not a speed-up system on the tested Wi-Fi; split 3B was much
  slower than ROG-only inference.
- Recovery is process restart + request retry, not KV-cache/token checkpointing.
- The real physical kill restored the next request. Same-request retry was
  verified by local SIGABRT simulation and still needs a final physical test.
- The 8B experiment proves pooled capacity but cannot survive loss of the TUF in
  the two-node topology.
- RPC and controller endpoints are unauthenticated. Trusted LAN only.

## Running the current demo

The canonical local path is `/mnt/newvolume/meta/hyperpod`.

Linux controller:

```bash
bash launchers/start-controller.sh
cd frontend
VITE_CONTROLLER=http://CONTROLLER_IP:8080 npm run dev
```

Windows worker:

1. Edit `launchers/start-worker.bat` once for controller IP and paths.
2. Run it to start the cached CUDA RPC server and Rust agent.
3. The agent re-registers automatically if the controller restarts.

Internal identifiers are now also renamed:

- binaries: `hyperpod-controller`, `hyperpod-worker`
- environment prefix: `HYPERPOD_`
- discovery type: `hyperpod-worker`

The Windows worker must be replaced after this breaking rename before the next
cross-machine test.

## Remaining work (ordered)

1. Rebuild/cross-compile and install the renamed Windows worker.
2. Set up the second TUF and verify three-node → two-node same-request recovery.
3. Run repeatable native-vs-HyperPod benchmarks.
4. Finalize beneficiary/impact wording after participant portal fields are
   supplied.
5. Add screenshots, architecture graphic, demo script and backup video.
6. Fresh-clone rehearsal and release checklist.

## User decisions still required

- Project source-code license: Apache-2.0, MIT, or remain unlicensed.
- Lead beneficiary.
- Participant-only submission form/rules screenshots.

No CI will be added.
