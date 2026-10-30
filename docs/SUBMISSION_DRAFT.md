# HyperPod Submission Draft

Status: draft for the Meta Global AI Developer Hackathon. The authenticated
submission form is not publicly visible, so this document provides reusable
lengths rather than inventing field limits.

Track: Indie Developer  
Theme: Push the Frontier  
Attribution: **Built with Llama**

## Tagline

Turn the laptops you already own into one local Llama cluster that recovers when
a worker drops.

## One sentence

HyperPod discovers consumer laptops on a local network, measures their usable
GPU memory, distributes Meta Llama weights through llama.cpp, and rebuilds the
inference pipeline when a worker disappears.

## Short summary

Student builders often own several modest laptops but no single large GPU.
HyperPod combines those machines into a local Meta Llama runtime. It discovers
workers, reports measured hardware, assigns model weight using available VRAM,
and restores serving after an RPC node fails. It is verified on Windows and
Linux with CUDA and in one heterogeneous run with a macOS/Metal worker over
ordinary Wi-Fi.

## Medium description

I built HyperPod for a problem I had directly: several consumer laptops, but no
single large GPU for local AI. Existing llama.cpp RPC can combine remote
devices, but a failed RPC peer aborts the inference process. That behavior makes
ordinary laptops difficult to rely on because users close lids, change networks
and stop processes.

HyperPod adds a control plane. Rust agents discover the Go controller,
report current hardware measurements and advertise their llama.cpp RPC devices.
The controller calculates tensor weights from measured free VRAM, starts the
model server, records the actual weight placement reported by llama.cpp, and
rebuilds the model on reachable devices after a failure. A React dashboard shows
worker health, hardware measurements, scheduler decisions and model placement.

In a physical test, a Llama 3.2 3B request began across a Linux ROG, Windows TUF
and M4 Mac. Stopping the Mac RPC server aborted llama.cpp. HyperPod excluded the
failed endpoint, rebuilt across the ROG and TUF, retried the same request and
returned the answer.

## Full project description

### The problem

A student or independent developer may own several laptops accumulated over
time but still lack one machine with enough GPU memory for larger local models.
llama.cpp RPC can expose remote compute, but the upstream RPC implementation is
a proof of concept: when a peer disappears, the main process aborts. Consumer
laptops are also uneven and temporary. Their available memory changes, Wi-Fi
links vary, and a worker may leave without warning.

Treating these laptops as fixed servers does not work. The runtime needs to know
what resources are actually available and treat worker loss as a normal event.

### What HyperPod does

HyperPod adds a control plane around llama.cpp:

1. A Rust worker announces itself over UDP and registers with the Go controller.
2. One sampler collects current GPU, memory and power data. Unsupported values
   remain `null`; the system does not infer capacity from a GPU name.
3. The controller calculates deterministic tensor weights from measured free
   VRAM minus a 512 MiB reserve per device.
4. If a required measurement is unavailable, HyperPod provides no custom
   weight. It records an explicit `llama.cpp-auto` strategy and lets llama.cpp
   inspect the devices.
5. The controller starts llama-server across reachable RPC endpoints.
6. It parses llama.cpp's own load log to report where the weights actually
   reside. This distinguishes requested weights from real placement.
7. When the supervised process aborts after an RPC failure, HyperPod probes each
   endpoint, removes the unavailable one, rebuilds the model and retries the
   interrupted request.

### User experience

The dashboard shows the controller at the center of the topology. Workers appear
when they join the network. The Nodes page displays measured VRAM, GPU load,
temperature, power, system RAM, battery and AC state. The model-placement view
shows the weight allocation that llama.cpp actually loaded. The user can choose
a fast local 1B profile or the distributed profile.

### Current model profiles

- **Llama 3.2 1B:** fast local profile.
- **Llama 3.2 3B:** recovery profile; small enough for the ROG to take over after
  losing the TUF.
- **Llama 3.1 8B:** capacity profile; the 4.9 GB file does not fit wholly in the
  ROG's 4 GB GPU memory. In the measured run, the ROG GPU held 1,073 MiB and
  the TUF GPU held 3,330 MiB at 6.68 tok/s.

The 3B and 8B results demonstrate different properties. The 3B result proves
recovery. The two-machine 8B result proves pooled capacity; it does not promise
survival after removing the TUF.

## Who benefits?

The lead user is a student or independent AI builder who owns several modest
laptops but cannot justify a dedicated large GPU or recurring cloud inference
cost. HyperPod lets that builder experiment with Meta open-weight models using
hardware already available at home or in a shared workspace.

The same runtime could support small community labs and field teams, but those
are secondary use cases. HyperPod is infrastructure, not an education chatbot or
medical assistant.

## Why the problem matters

Local open-weight models give developers control over data and deployment, but
the useful model size is often limited by one machine's memory. Pooling existing
devices can raise that limit. Without recovery and measured placement, however,
the pool is as fragile as its least reliable laptop.

The measured result shows the trade: the tested Wi-Fi split is slower than one
local GPU, but it adds aggregate model capacity and restores service when the
selected recovery model still fits on the surviving devices.

## How HyperPod uses Meta AI

HyperPod uses the three Meta model profiles listed above as movable runtime
resources rather than remote chatbot endpoints. The controller assigns and
observes their weights across CUDA and Metal devices. When topology changes, it
rebuilds the placement and retries work.

HyperPod does not fine-tune these models and does not claim a new model
architecture. Meta supplies the model weights; llama.cpp supplies the inference
kernels and RPC backend; HyperPod supplies discovery, measurement, scheduling,
observability and recovery.

## Technical implementation

### Go controller

- HTTP registration, heartbeat, status, pipeline and inference APIs
- UDP discovery listener
- Worker registry and heartbeat expiry
- Measured-VRAM scheduler
- llama.cpp process supervision
- Actual placement parsing
- Loading state and request retry

### Rust worker

- Windows, Linux and macOS agent
- UDP discovery announcements
- HTTP registration and recurring heartbeats
- Automatic re-registration after controller restart
- NVIDIA telemetry through `nvidia-smi`
- Linux memory and power telemetry through `/proc` and `power_supply`
- Windows memory and power telemetry through CIM/WMI
- macOS memory and power telemetry through `sysctl` and `pmset`
- Explicit unavailable values for unsupported measurements

### React dashboard

- Live node topology
- Node health and hardware telemetry
- Scheduler strategy and tensor weights
- Actual model placement
- Local/distributed model selection
- Inference playground

### llama.cpp

- GGUF loading and inference
- CUDA and Metal execution
- Remote device RPC
- Local tensor cache

## Verified results

### Measured scheduling

Before one 3B load:

```text
TUF RTX 4050 free VRAM: 5843 MiB
ROG RTX 3050 free VRAM: 3716 MiB
reserve per device:      512 MiB
computed weights for this run: 5331 (TUF RPC), 3204 (ROG local)
```

Actual placement reported by llama.cpp:

```text
CPU_Mapped                         308 MiB
ROG CUDA0                          842 MiB
TUF RPC0                         1,076 MiB
```

### Three-machine recovery

### Recovery run A: three machines to two

Initial placement:

```text
ROG CPU                             308 MiB
ROG CUDA                            552 MiB
M4 Mac RPC                          968 MiB
TUF RPC                             398 MiB
```

The Mac RPC server was stopped during a request capped at 420 output tokens. Its Rust agent stayed
alive, which tested accelerator health independently from agent health.
HyperPod:

```text
recognized llama-server abort
skipped the unreachable Mac RPC endpoint
rebuilt across ROG + TUF in 33 seconds
retried the same HTTP request
returned 1,937 characters in 75.68 seconds total
```

Recovered placement:

```text
ROG CPU                             308 MiB
ROG CUDA                            896 MiB
TUF RPC                           1,022 MiB
```

### Recovery run B: same-TUF native comparison

With the same TUF RPC endpoint and 3B split:

```text
Native llama.cpp:
  curl 52 / HTTP 000
  process aborted
  no health endpoint
  no restart

HyperPod:
  underlying llama.cpp process aborted
  failed endpoint removed
  local recovery model ready in about 3 seconds
  same request returned 1,828 characters
```

The two failure prompts requested different output lengths. These results compare
failure outcome, not throughput or latency.

### Performance

Five-run warm-model medians:

```text
ROG-only:             1.543 s, 75.54 tok/s
Native llama.cpp RPC: 11.947 s, 13.19 tok/s
HyperPod RPC:         11.804 s, 11.79 tok/s
```

The small difference between native and HyperPod split latency is within the
observed run-to-run variation. No control-plane speedup is claimed. Splitting
was substantially slower than ROG-only inference on the tested Wi-Fi.

Full method and per-run values: [`../BENCHMARKS.md`](../BENCHMARKS.md).

## Suggested demo narration

> I own several laptops, but none has a large GPU. llama.cpp can combine their
> memory, but if one RPC worker disappears, the entire model process aborts.
> HyperPod adds a measured recovery control plane. These Windows, Linux and Mac
> workers appeared automatically. The dashboard shows their real hardware and
> where llama.cpp placed the model. I will stop the Mac accelerator during this
> request. llama.cpp aborts underneath, HyperPod removes that endpoint, rebuilds
> across the ROG and TUF, and retries the same request. The response returns
> without manual intervention.

## Honest limitations

- Trusted local network only; RPC and controller traffic are not authenticated
  or encrypted.
- Recovery restarts inference and retries the request. It does not resume the KV
  cache or continue from the exact generated token.
- Wi-Fi model splitting is slower than one local GPU when the model already fits.
- The measured scheduler currently uses usable VRAM, not compute throughput or
  network bandwidth.
- Metal free-memory telemetry is unavailable; mixed CUDA/Metal placement uses an
  explicit llama.cpp fallback.
- Adding a worker does not reconfigure an already loaded pipeline automatically.
- The controller is a single point of failure.

## Claims not to make

Do not claim that HyperPod:

- invented distributed inference,
- is the first fault-tolerant LLM system,
- speeds up inference over Wi-Fi,
- continues from the exact failed token,
- makes the two-node 8B profile fault tolerant,
- securely pools untrusted machines,
- measures Metal free memory,
- performs distributed training or fine-tuning,
- is production ready.

## Evidence links

- [`../BENCHMARKS.md`](../BENCHMARKS.md)
- [`../test/phase5-telemetry-scheduler.log`](../test/phase5-telemetry-scheduler.log)
- [`../test/phase6-three-node-recovery.log`](../test/phase6-three-node-recovery.log)
- [`../test/phase7-native-benchmark.log`](../test/phase7-native-benchmark.log)
- [`../README.md`](../README.md)
