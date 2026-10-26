# HyperPot — Phase 2 Design Decision (Path A: Supervised Split)

Date: 26 Oct 2026. Status: ACTIVE (building now).

## The decision
Build the STRONG story (Path A): one real Llama model split across multiple
laptops, with automatic recovery when a laptop dies. Recovery is done the only
way the facts allow: external supervision + restart + re-run.

## Why Path A (not Path B)
Path B (each laptop runs the whole small model, controller load-balances) was
rejected: it is the weak story — if each laptop can run the whole model alone,
pooling is pointless. Path A matches our VERIFIED gap: "nobody survives a node
dying while a model is split across laptops."

## How the split works (VERIFIED from llama.cpp docs)
llama.cpp itself performs the split:
```
llama-server -m model.gguf -ngl 99 \
   --rpc <hostA:port>,<hostB:port> --tensor-split <a>,<b>
```
Each worker runs `ggml-rpc-server`; llama-server distributes layers across them
in proportion to `--tensor-split`. We do NOT write the split ourselves.

## The unavoidable fact (VERIFIED)
llama.cpp RPC aborts the whole process on any peer failure
(`RPC_STATUS_ASSERT` -> `GGML_ABORT`). We cannot catch this inside the process.
Proven on our own hardware in the Phase 0 kill-test.

## Therefore: the supervisor model (what we build)
The Go controller becomes a SUPERVISOR of the llama.cpp RPC pipeline:
1. Launch `llama-server` with `--rpc` over the registered healthy workers and a
   capability-based `--tensor-split`.
2. Proxy the user's `/infer` to the supervised llama-server, stream tokens back.
3. On node death: detect it (worker heartbeat loss + llama-server process exit).
4. Recompute the split for the SURVIVING workers.
5. Relaunch llama-server on the survivors.
6. RE-RUN the in-flight request automatically; tokens resume for the user.

This is the accepted real-world pattern for llama.cpp crashes (watchdog +
restart), applied to distributed inference.

## The HONEST tradeoff (state this to judges)
- REAL model split across laptops .......................... YES (strong story)
- Automatic recovery when a laptop dies ................... YES
- Recovery = restart pipeline + re-run request (~seconds) . YES (short pause)
- Seamless token-level resume (continue exact token) ...... NO -> STRETCH GOAL

So the demo is: "kill a laptop -> llama.cpp crashes like it always does -> the
HyperPot controller detects it, rebuilds the pipeline on the survivors, and
re-runs your request automatically -> you still get your answer."

## Parked stretch goal (only if time remains at the very end)
Seamless token-level resume (no re-run, continue from the last token). This needs
deep llama.cpp/C++ work (export + ship activations/KV state) and is HIGH RISK for
the timeline. Attempt ONLY after the restart+rerun version is working and
demo-ready.

## Reused from earlier phases
- Go controller + worker registry + heartbeats (Phase 1) ... reused
- ggml-rpc-server built + tested on ROG and TUF (Phase 0) .. reused
- llama-server binary (built Phase 2) ..................... new
