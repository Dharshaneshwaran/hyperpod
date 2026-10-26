# HyperPot

Fault-tolerant distributed LLM inference on a pool of consumer laptops.

HyperPot pools ordinary laptops (over Wi-Fi) into one cluster that runs a Meta
Llama model. Its defining capability is **fault tolerance**: when a laptop drops
off the network mid-generation, the controller re-routes the work and the answer
keeps streaming — instead of the whole thing crashing (which is what plain
llama.cpp RPC does; see `docs/.../12-evidence-log`).

## Layout
```
controller/   Go  — coordinator: HTTP API, worker registry, heartbeats, dispatch
worker/       Rust — worker agent: registers, heartbeats, runs llama.cpp locally
PROTOCOL.md        — controller <-> worker message format
test/              — llama.cpp build + model + kill-test evidence
docs/              — the idea/plan/evidence document (LaTeX + PDF)
```

## Phase status
- [x] Phase 0: kill-test — verified llama.cpp RPC crashes on node loss (see docs)
- [ ] **Phase 1: controller + one worker + local llama.cpp, single node (CURRENT)**
- [ ] Phase 2: two workers, static pipeline over Wi-Fi
- [ ] Phase 3: failover (the novel core)
- [ ] Phase 4: add third worker (capability-aware)
- [ ] Phase 5: Macs (Metal), cross-OS
- [ ] Phase 6: demo + submission

## Phase 1 quick start (single machine)
See `controller/README` and `worker/README`, or the repo-level run notes.

# hyperpod
