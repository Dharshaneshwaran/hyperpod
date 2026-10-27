import { useEffect, useState, useCallback, useRef } from "react";
import "./App.css";

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
type InferResp = { ok: boolean; worker_id?: string; text?: string; error?: string };

export default function App() {
  const [workers, setWorkers] = useState<Worker[]>([]);
  const [reachable, setReachable] = useState(false);
  const seenRef = useRef<Set<string>>(new Set());
  const [joined, setJoined] = useState<Set<string>>(new Set()); // ids currently animating-in

  const [prompt, setPrompt] = useState("In one sentence, what is teamwork?");
  const [tier, setTier] = useState<"small" | "big">("big");
  const [answer, setAnswer] = useState("");
  const [servedBy, setServedBy] = useState("");
  const [busy, setBusy] = useState(false);
  const [elapsed, setElapsed] = useState<number | null>(null);

  const refresh = useCallback(async () => {
    try {
      const r = await fetch(`${CONTROLLER}/status`);
      const d: StatusResp = await r.json();
      const list = d.workers ?? [];
      // detect newly-joined workers to trigger the join animation
      for (const w of list) {
        if (!seenRef.current.has(w.worker_id)) {
          seenRef.current.add(w.worker_id);
          setJoined((prev) => new Set(prev).add(w.worker_id));
          setTimeout(() => {
            setJoined((prev) => {
              const n = new Set(prev);
              n.delete(w.worker_id);
              return n;
            });
          }, 1200);
        }
      }
      setWorkers(list);
      setReachable(true);
    } catch {
      setReachable(false);
      setWorkers([]);
    }
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, 1500);
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
      } else setAnswer(`Error: ${d.error ?? "unknown"}`);
    } catch (e) {
      setAnswer(`Request failed: ${String(e)}`);
    } finally {
      setBusy(false);
    }
  };

  const healthy = workers.filter((w) => w.healthy).length;

  // Topology geometry: controller at center, workers on a circle around it.
  const VIEW = 520;
  const C = VIEW / 2;
  const R = 170;
  const positions = workers.map((_, i) => {
    const angle = (-90 + (360 / Math.max(workers.length, 1)) * i) * (Math.PI / 180);
    return { x: C + R * Math.cos(angle), y: C + R * Math.sin(angle) };
  });

  return (
    <div className="app">
      <header className="hdr">
        <div className="brand">
          <div className="mark" aria-hidden />
          <div>
            <h1>HyperPot</h1>
            <p className="tag">Fault-tolerant distributed inference · live cluster</p>
          </div>
        </div>
        <span className={`conn ${reachable ? "up" : "down"}`}>
          <span className="cdot" />
          {reachable ? "Controller online" : "Controller offline"}
        </span>
      </header>

      {/* ---------- Topology ---------- */}
      <section className="stage">
        <div className="stage-meta">
          <span className="pill">{healthy} of {workers.length} nodes healthy</span>
        </div>

        <svg className="topo" viewBox={`0 0 ${VIEW} ${VIEW}`} role="img" aria-label="cluster topology">
          <defs>
            <radialGradient id="hubGlow" cx="50%" cy="50%" r="50%">
              <stop offset="0%" stopColor="#4c8bf5" stopOpacity="0.45" />
              <stop offset="100%" stopColor="#4c8bf5" stopOpacity="0" />
            </radialGradient>
          </defs>

          {/* links */}
          {positions.map((p, i) => {
            const w = workers[i];
            return (
              <line
                key={`l-${w.worker_id}`}
                x1={C} y1={C} x2={p.x} y2={p.y}
                className={`link ${w.healthy ? "ok" : "dead"}`}
              />
            );
          })}

          {/* hub glow + controller node */}
          <circle cx={C} cy={C} r={70} fill="url(#hubGlow)" />
          <g className="hub">
            <circle cx={C} cy={C} r={38} className="hub-core" />
            <text x={C} y={C - 2} className="hub-label">controller</text>
            <text x={C} y={C + 14} className="hub-sub">this laptop</text>
          </g>

          {/* worker nodes */}
          {positions.map((p, i) => {
            const w = workers[i];
            const isNew = joined.has(w.worker_id);
            return (
              <g
                key={`n-${w.worker_id}`}
                className={`wnode ${w.healthy ? "ok" : "dead"} ${isNew ? "join" : ""}`}
                transform={`translate(${p.x}, ${p.y})`}
              >
                <circle r={30} className="wnode-core" />
                <circle r={30} className="wnode-ring" />
                <text y={-2} className="wnode-id">{w.worker_id.replace("worker-", "")}</text>
                <text y={13} className="wnode-gpu">{w.gpu}</text>
              </g>
            );
          })}
        </svg>

        {workers.length === 0 && (
          <p className="empty">Waiting for laptops to join this Wi-Fi… they’ll appear around the controller automatically.</p>
        )}

        {/* legend / detail chips */}
        <div className="chips">
          {workers.map((w) => (
            <div key={w.worker_id} className={`chip ${w.healthy ? "ok" : "dead"}`}>
              <span className="chip-dot" />
              <span className="chip-id">{w.worker_id}</span>
              <span className="chip-meta">{w.gpu} · {w.os} · {w.last_seen_secs.toFixed(1)}s</span>
            </div>
          ))}
        </div>
      </section>

      {/* ---------- Ask ---------- */}
      <section className="ask">
        <h2>Ask the cluster</h2>
        <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={3} placeholder="Type a prompt…" />
        <div className="controls">
          <div className="seg">
            <button className={tier === "small" ? "on" : ""} onClick={() => setTier("small")}>Small · 1B</button>
            <button className={tier === "big" ? "on" : ""} onClick={() => setTier("big")}>Big · 3B split</button>
          </div>
          <button className="send" onClick={ask} disabled={busy || !reachable}>
            {busy ? "Generating…" : "Send"}
          </button>
        </div>

        {(answer || busy) && (
          <div className="answer">
            {servedBy && (
              <div className="served">
                <span className="served-badge">{servedBy}</span>
                {elapsed !== null && <span className="served-time">{elapsed.toFixed(1)}s</span>}
              </div>
            )}
            <pre>{busy ? "▋" : answer}</pre>
          </div>
        )}
      </section>

      <footer className="ftr">Kill a laptop — the cluster keeps serving.</footer>
    </div>
  );
}
