# HyperPod Protocol

Current protocol version: application messages are JSON over HTTP; worker
discovery uses JSON over UDP. Worker telemetry has its own independently
versioned schema (`schema_version: 1`).

This protocol is intended for a **trusted local network only**. It currently has
no authentication or encryption.

## Roles

- **Controller (Go):** worker registry, health, telemetry, measured split
  scheduling, llama.cpp process supervision and public HTTP API.
- **Worker agent (Rust):** collects telemetry, registers, sends heartbeats,
  broadcasts discovery and exposes a whole-model `/run` fallback endpoint.
- **llama.cpp RPC server:** exposes a worker accelerator at `host:50052`.
- **llama.cpp server:** launched by the controller with healthy RPC endpoints and
  optional measured `--tensor-split` weights.

## Worker → controller

### `POST /register`

```json
{
  "worker_id": "worker-TUF",
  "address": "http://192.168.1.5:9100",
  "rpc_addr": "192.168.1.5:50052",
  "gpu": "NVIDIA GeForce RTX 4050 Laptop GPU",
  "os": "windows",
  "telemetry": {
    "schema_version": 1,
    "collected_at_unix_ms": 1793379939410,
    "gpu": {
      "source": "nvidia-smi",
      "name": "NVIDIA GeForce RTX 4050 Laptop GPU",
      "vram_total_mib": 6141,
      "vram_free_mib": 5843,
      "utilization_percent": 0,
      "temperature_celsius": 50,
      "power_draw_watts": 4.09,
      "power_limit_watts": null
    },
    "memory": {
      "source": "windows-cim",
      "total_mib": 16003,
      "available_mib": 4845
    },
    "power": {
      "source": "windows-cim-wmi",
      "battery_percent": 84,
      "ac_online": true
    }
  }
}
```

Unknown groups and values are `null`, never fabricated zeros. The legacy `gpu`
field is retained for compatibility; `telemetry.gpu` is authoritative.

### `POST /heartbeat`

Sent every three seconds. Telemetry is sampled by one shared collector every
five seconds.

```json
{
  "worker_id": "worker-TUF",
  "telemetry": { "schema_version": 1, "collected_at_unix_ms": 1793379939410, "gpu": null, "memory": null, "power": null }
}
```

A missing telemetry object is accepted for compatibility and preserves the last
snapshot. An unsupported schema version is rejected with HTTP 400. If a
heartbeat fails (including after a controller restart), the worker re-registers.

### UDP discovery on port `50999`

Workers broadcast the registration identity plus the current telemetry snapshot:

```json
{
  "type": "hyperpod-worker",
  "worker_id": "worker-TUF",
  "address": "http://192.168.1.5:9100",
  "rpc_addr": "192.168.1.5:50052",
  "gpu": "NVIDIA GeForce RTX 4050 Laptop GPU",
  "os": "windows",
  "telemetry": { "schema_version": 1, "collected_at_unix_ms": 1793379939410, "gpu": null, "memory": null, "power": null }
}
```

Discovery works only within one broadcast domain.

## Public controller API

### `GET /status`

Returns worker identity, health, last-seen age, whether the worker is local, and
the complete nullable telemetry snapshot.

### `GET /pipeline`

Returns the supervised big-tier process state and the real placement parsed from
llama.cpp logs:

```json
{
  "ok": true,
  "enabled": true,
  "description": "CPU_Mapped 308 MiB + CUDA0 842 MiB + RPC0[192.168.1.5:50052] 1076 MiB",
  "pipeline": {
    "generation": 1,
    "state": "ready",
    "rpc_workers": ["192.168.1.5:50052"],
    "devices": [
      {"device": "CPU_Mapped", "mib": 308.23},
      {"device": "CUDA0", "mib": 842.41},
      {"device": "RPC0[192.168.1.5:50052]", "mib": 1075.95}
    ],
    "offloaded": "29/29",
    "split": true,
    "scheduler_strategy": "measured-free-vram",
    "tensor_split": "5331,3204",
    "split_weights_mib": [5331, 3204],
    "vram_reserve_mib": 512,
    "loading_secs": 0,
    "ready_secs": 59.7
  }
}
```

Weight order is RPC workers first, then the local GPU. Each measured weight is
`free_vram_mib - 512`. If a required measurement is unavailable, no custom
weights are supplied and the strategy reports a `llama.cpp-auto (...)` reason.

### `POST /infer`

```json
{
  "prompt": "In one sentence, what is distributed computing?",
  "n_predict": 64,
  "tier": "big"
}
```

- `small`: local Llama 3.2 1B server (if configured).
- `big` or omitted: supervised configurable model, split across healthy workers.

A successful response reports real placement:

```json
{
  "ok": true,
  "worker_id": "big tier · split",
  "text": "...",
  "placement": "CPU_Mapped 308 MiB + CUDA0 842 MiB + RPC0[192.168.1.5:50052] 1076 MiB",
  "split": true
}
```

If the big model is still loading, the API returns HTTP 503 with
`"loading": true`; loading continues in the background. If llama-server exits
after an RPC failure, HyperPod rebuilds over reachable survivors and retries.

## Controller → worker fallback

### `POST /run`

The Phase 1 whole-model fallback endpoint remains available:

```json
{"prompt":"...", "n_predict":64}
```

```json
{"ok":true, "text":"...", "error":""}
```

This is not the layer-split path. Distributed placement is performed by the
controller-supervised llama.cpp RPC backend.
