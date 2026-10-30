// API client for the HyperPod Go controller.
export const CONTROLLER: string =
  (import.meta.env.VITE_CONTROLLER as string) || "http://127.0.0.1:8080";

export type Telemetry = {
  schema_version: number;
  collected_at_unix_ms: number;
  gpu: {
    source: string;
    name: string | null;
    vram_total_mib: number | null;
    vram_free_mib: number | null;
    utilization_percent: number | null;
    temperature_celsius: number | null;
    power_draw_watts: number | null;
    power_limit_watts: number | null;
  } | null;
  memory: {
    source: string;
    total_mib: number | null;
    available_mib: number | null;
  } | null;
  power: {
    source: string;
    battery_percent: number | null;
    ac_online: boolean | null;
  } | null;
};

export type Worker = {
  worker_id: string;
  address: string;
  rpc_addr: string;
  gpu: string;
  os: string;
  healthy: boolean;
  last_seen_secs: number;
  local: boolean; // runs on the controller's own laptop
  telemetry: Telemetry | null;
};

export type InferResp = {
  ok: boolean;
  worker_id?: string;
  text?: string;
  error?: string;
  placement?: string;
  split?: boolean;
  loading?: boolean;
};

export type DevicePlacement = { device: string; mib: number };
export type Pipeline = {
  generation: number;
  state: "down" | "loading" | "ready";
  rpc_workers: string[] | null;
  devices: DevicePlacement[] | null;
  offloaded: string;
  split: boolean;
  scheduler_strategy: string;
  tensor_split: string;
  split_weights_mib: number[] | null;
  vram_reserve_mib: number;
};

export async function fetchPipeline(): Promise<Pipeline | null> {
  const r = await fetch(`${CONTROLLER}/pipeline`);
  const d = (await r.json()) as { enabled: boolean; pipeline?: Pipeline };
  return d.enabled && d.pipeline ? d.pipeline : null;
}

export async function fetchStatus(): Promise<Worker[]> {
  const r = await fetch(`${CONTROLLER}/status`);
  const d = (await r.json()) as { ok: boolean; workers?: Worker[] };
  return d.workers ?? [];
}

export async function infer(
  prompt: string,
  tier: "small" | "big",
): Promise<InferResp> {
  const r = await fetch(`${CONTROLLER}/infer`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ prompt, tier, n_predict: 96 }),
  });
  return (await r.json()) as InferResp;
}
