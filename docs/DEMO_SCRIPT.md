# HyperPod Demo Script

Target length: 100–120 seconds. Adjust only after the authenticated portal
reveals its actual video limit.

## Preparation

- Use the 3B recovery profile for the live failure sequence.
- Start ROG controller/dashboard, TUF RPC+agent and M4 RPC+agent.
- Wait until `GET /pipeline` reports `state=ready` and placement lists all three
  machines.
- Close unrelated windows and hide IPs only if the submission policy requires
  it. Do not hide the measured hardware names or placement.
- Record at 1440p or 1080p with readable terminal/UI text.
- Keep `BENCHMARKS.md` and `test/phase6-three-node-recovery.log` open as backup.

## Narration and shots

### 0–12 seconds — problem

**Narration**

> I am a student builder with several modest laptops, but no single large GPU.
> llama.cpp can combine remote devices, but losing one RPC worker aborts the
> entire model process.

**Shot**

- Show the three physical laptops briefly.
- Show the native error line for less than two seconds:
  `Remote RPC server crashed or returned malformed response`.

### 12–30 seconds — workers and measurements

**Narration**

> HyperPod adds a local control plane. The Windows, Linux and Mac workers appear
> automatically and report the resources they actually have.

**Shot**

- Dashboard Overview, then Nodes.
- Point to RTX 4050/3050, measured VRAM, temperature, RAM and power state.
- Point out that unavailable Metal free-memory is shown as unavailable.

### 30–48 seconds — scheduling and placement

**Narration**

> When free VRAM is known, HyperPod calculates tensor weights from measured
> memory with a safety reserve. It then checks llama.cpp's own load report to
> show where the model really landed.

**Shot**

- Pipeline strategy and requested weights.
- Three-machine actual placement:
  ROG CUDA 552 MiB, M4 RPC 968 MiB, TUF RPC 398 MiB.
- State clearly that the mixed Metal run uses `llama.cpp-auto` because Metal free
  memory is unavailable; no value is guessed.

### 48–85 seconds — failure and recovery

**Narration**

> This request is running through all three machines. I will stop only the Mac
> accelerator. Its agent remains alive, so HyperPod must distinguish a healthy
> heartbeat from a dead RPC device.

**Shot**

- Submit the prepared 250-word collaboration prompt.
- Stop the Mac `ggml-rpc-server`, leaving the Mac Rust worker open.
- Show event/log text:
  - llama-server aborted
  - Mac RPC endpoint unreachable and skipped
  - generation 2 launched over ROG + TUF
  - ready after 33 seconds
- Show recovered placement: ROG 896 MiB + TUF 1,022 MiB.

**Narration**

> llama.cpp aborts underneath. HyperPod removes the unavailable accelerator,
> rebuilds across the ROG and TUF, and retries the same request.

### 85–103 seconds — completed request and capacity

**Shot**

- Show the original request return successfully.
- Display: 1,937 characters, 75.68 seconds total.
- Cut to the recorded 8B placement graphic: ROG 1,073 MiB + TUF 3,330 MiB.

**Narration**

> The same request completes without manual resubmission. For capacity, I also
> ran Llama 3.1 8B with most GPU-resident weights on the TUF.

### 103–115 seconds — close

**Narration**

> HyperPod is for students and independent builders who already own several
> laptops but cannot justify one large GPU. It trades Wi-Fi throughput for local
> capacity, measured placement and automatic recovery. Built with Llama.

**Shot**

- HyperPod name, GitHub URL and `Built with Llama`.

## Claims to avoid in narration

Do not say:

- seamless token continuation,
- first distributed or fault-tolerant LLM system,
- faster than local inference,
- secure for untrusted devices,
- 8B survives a TUF failure in the two-node setup,
- Metal free memory is measured,
- production ready.

## Backup edit if live failure timing is awkward

Use three continuous clips with visible timestamps:

1. request starts on three-machine placement,
2. Mac RPC stops and generation 1 aborts,
3. generation 2 placement appears and the same request returns.

Do not shorten the gap in a way that implies instant recovery. Put `33 s model
reload` on screen.
