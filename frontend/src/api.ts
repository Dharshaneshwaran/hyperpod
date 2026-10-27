// API client for the HyperPot Go controller.
export const CONTROLLER: string =
  (import.meta.env.VITE_CONTROLLER as string) || "http://127.0.0.1:8080";

export type Worker = {
  worker_id: string;
  address: string;
  rpc_addr: string;
  gpu: string;
  os: string;
  healthy: boolean;
  last_seen_secs: number;
};

export type InferResp = {
  ok: boolean;
  worker_id?: string;
  text?: string;
  error?: string;
};

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
