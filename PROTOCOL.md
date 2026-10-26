# HyperPot Protocol (Phase 1)

Transport: plain HTTP + JSON over the LAN/Wi-Fi.
Phase 1 is single-node (controller + one worker on the same machine), but the
protocol is designed so multiple workers can join later.

## Roles
- **Controller (Go):** accepts user prompts, tracks workers, dispatches an
  inference job to a worker, returns the generated text.
- **Worker (Rust):** registers with the controller, sends heartbeats, runs
  llama.cpp locally on a prompt, returns the generated text.

## Endpoints (served by the Controller)

### POST /register
Worker announces itself.
Request:
```json
{ "worker_id": "worker-A", "address": "http://127.0.0.1:9100", "gpu": "RTX 3050", "os": "linux" }
```
Response: `{ "ok": true }`

### POST /heartbeat
Worker proves it is alive (sent every few seconds).
Request:
```json
{ "worker_id": "worker-A" }
```
Response: `{ "ok": true }`

### POST /infer   (called by the USER)
Ask the cluster to generate text.
Request:
```json
{ "prompt": "In one sentence, what is a dragon?", "n_predict": 64 }
```
Response:
```json
{ "ok": true, "worker_id": "worker-A", "text": "A dragon is ..." }
```
If no healthy worker is available: `{ "ok": false, "error": "no healthy worker" }`

### GET /status
Human/debug view of known workers and their last-heartbeat age.

## Endpoints (served by each Worker)

### POST /run
Controller tells the worker to run inference.
Request:
```json
{ "prompt": "...", "n_predict": 64 }
```
Response:
```json
{ "ok": true, "text": "..." }
```

## Health rule
A worker is "healthy" if its last heartbeat was within `HEARTBEAT_TIMEOUT`
(default 10s). This timeout is the foundation that later phases use to detect a
dead node and trigger failover.
