import { useEffect, useState, useCallback } from "react";
import "./App.css";

// The Go controller base URL. Override at build/run with VITE_CONTROLLER.
const CONTROLLER: string =
  (import.meta.env.VITE_CONTROLLER as string) || "http://127.0.0.1:8080";

type Worker = {
  worker_id: string;
  address: string;
  rpc_addr: string;
  gpu: string;
  os: string;
  healthy: boolean;
  last_seen_secs: number;
};

type StatusResp = { ok: boolean; workers: Worker[] };

type InferResp = {
  ok: boolean;
  worker_id?: string;
  text?: string;
  error?: string;
};

export default function App() {
  const [workers, setWorkers] = useState<Worker[]>([]);
  const [reachable, setReachable] = useState<boolean>(false);

  const [prompt, setPrompt] = useState("In one sentence, what is teamwork?");
  const [tier, setTier] = useState<"small" | "big">("big");
  const [answer, setAnswer] = useState("");
  const [servedBy, setServedBy] = useState("");
  const [busy, setBusy] = useState(false);
  const [elapsed, setElapsed] = useState<number | null>(null);

  // Poll /status every 2s so joining/leaving laptops appear live.
  const refresh = useCallback(async () => {
    try {
      const r = await fetch(`${CONTROLLER}/status`);
      const d: StatusResp = await r.json();
      setWorkers(d.workers ?? []);
      setReachable(true);
    } catch {
      setReachable(false);
      setWorkers([]);
    }
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, 2000);
    return () => clearInterval(id);
  }, [refresh]);

  const ask = async () => {
    if (!prompt.trim() || busy) return;
    setBusy(true);
    setAnswer("");
    setServedBy("");
    setElapsed(null);
    const t0 = performance.now();
    try {
      const r = await fetch(`${CONTROLLER}/infer`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ prompt, tier, n_predict: 48 }),
      });
      const d: InferResp = await r.json();
      setElapsed((performance.now() - t0) / 1000);
      if (d.ok) {
        setAnswer(d.text ?? "");
        setServedBy(d.worker_id ?? "");
      } else {
        setAnswer(`Error: ${d.error ?? "unknown"}`);
      }
    } catch (e) {
      setAnswer(`Request failed: ${String(e)}`);
    } finally {
      setBusy(false);
    }
  };

  const healthyCount = workers.filter((w) => w.healthy).length;

  return (
    <div className="app">
      <header className="hdr">
        <h1>HyperPot</h1>
        <p className="tag">
          Fault-tolerant distributed LLM inference on a pool of laptops
        </p>
        <span className={`conn ${reachable ? "up" : "down"}`}>
          controller {reachable ? "connected" : "unreachable"} · {CONTROLLER}
        </span>
      </header>

      <section className="panel">
        <div className="panel-head">
          <h2>Cluster</h2>
          <span className="count">
            {healthyCount}/{workers.length} healthy
          </span>
        </div>
        {workers.length === 0 ? (
          <p className="empty">
            No workers registered yet. Start a worker/rpc-server on another
            laptop on this Wi-Fi — it will appear here.
          </p>
        ) : (
          <div className="grid">
            {workers.map((w) => (
              <div
                key={w.worker_id}
                className={`node ${w.healthy ? "ok" : "dead"}`}
              >
                <div className="node-top">
                  <span className="dot" />
                  <span className="wid">{w.worker_id}</span>
                </div>
                <div className="node-meta">
                  <span>{w.gpu}</span>
                  <span>{w.os}</span>
                  <span>{w.healthy ? "healthy" : "DOWN"}</span>
                  <span>seen {w.last_seen_secs.toFixed(1)}s ago</span>
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="panel">
        <div className="panel-head">
          <h2>Ask the cluster</h2>
        </div>
        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          rows={3}
          placeholder="Type a prompt…"
        />
        <div className="controls">
          <label>
            Tier:&nbsp;
            <select
              value={tier}
              onChange={(e) => setTier(e.target.value as "small" | "big")}
            >
              <option value="small">small (1B, fast, single laptop)</option>
              <option value="big">big (3B, split across pool)</option>
            </select>
          </label>
          <button onClick={ask} disabled={busy || !reachable}>
            {busy ? "Generating…" : "Send"}
          </button>
        </div>

        {(answer || busy) && (
          <div className="answer">
            {servedBy && (
              <div className="served">
                served by <b>{servedBy}</b>
                {elapsed !== null && <> · {elapsed.toFixed(1)}s</>}
              </div>
            )}
            <pre>{busy ? "…" : answer}</pre>
          </div>
        )}
      </section>

      <footer className="ftr">
        Infrastructure demo · kill a laptop and the cluster keeps serving.
      </footer>
    </div>
  );
}
