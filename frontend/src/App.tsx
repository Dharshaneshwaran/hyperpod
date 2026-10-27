import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  IconAffiliate,
  IconBolt,
  IconBrandApple,
  IconBrandUbuntu,
  IconBrandWindows,
  IconCircleCheck,
  IconCircleX,
  IconCopy,
  IconCheck,
  IconCpu,
  IconEye,
  IconLayoutDashboard,
  IconMessageChatbot,
  IconSearch,
  IconSelector,
  IconServer,
  IconX,
} from "@tabler/icons-react";
import { CONTROLLER, fetchPipeline, fetchStatus, infer, type Pipeline, type Worker } from "./api";
import "./App.css";

type Page = "overview" | "nodes" | "playground";
type Filter = "all" | "healthy" | "down";

function OsIcon({ os }: { os: string }) {
  const o = os.toLowerCase();
  if (o.includes("win")) return <IconBrandWindows size={16} />;
  if (o.includes("darwin") || o.includes("mac")) return <IconBrandApple size={16} />;
  return <IconBrandUbuntu size={16} />;
}

function StatusPill({ healthy }: { healthy: boolean }) {
  return healthy ? (
    <span className="pill pill-ok">
      <IconCircleCheck size={13} /> Healthy
    </span>
  ) : (
    <span className="pill pill-down">
      <IconCircleX size={13} /> Down
    </span>
  );
}

export default function App() {
  const [page, setPage] = useState<Page>(() => {
    const h = window.location.hash.slice(1);
    return h === "nodes" || h === "playground" ? h : "overview";
  });
  const [workers, setWorkers] = useState<Worker[]>([]);
  const [online, setOnline] = useState(false);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [sortAsc, setSortAsc] = useState(true);
  const [selected, setSelected] = useState<Worker | null>(null);
  const [copied, setCopied] = useState("");
  const [joined, setJoined] = useState<Set<string>>(new Set());
  const seen = useRef<Set<string>>(new Set());
  const searchRef = useRef<HTMLInputElement>(null);

  // playground
  const [prompt, setPrompt] = useState("In one sentence, what is teamwork?");
  const [tier, setTier] = useState<"small" | "big">("big");
  const [answer, setAnswer] = useState("");
  const [servedBy, setServedBy] = useState("");
  const [placement, setPlacement] = useState("");
  const [pipeline, setPipeline] = useState<Pipeline | null>(null);
  const [elapsed, setElapsed] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const list = await fetchStatus();
      for (const w of list) {
        if (!seen.current.has(w.worker_id)) {
          seen.current.add(w.worker_id);
          setJoined((p) => new Set(p).add(w.worker_id));
          setTimeout(
            () =>
              setJoined((p) => {
                const n = new Set(p);
                n.delete(w.worker_id);
                return n;
              }),
            900,
          );
        }
      }
      setWorkers(list);
      setPipeline(await fetchPipeline().catch(() => null));
      setOnline(true);
      setSelected((cur) => (cur ? (list.find((w) => w.worker_id === cur.worker_id) ?? cur) : cur));
    } catch {
      setOnline(false);
    }
  }, []);

  useEffect(() => {
    window.history.replaceState(null, "", `#${page}`);
  }, [page]);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, 1500);
    return () => clearInterval(id);
  }, [refresh]);

  // keyboard: Ctrl/Cmd+K search, 1/2/3 switch pages, Esc closes panel
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = (e.target as HTMLElement)?.tagName.match(/INPUT|TEXTAREA/);
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPage("nodes");
        setTimeout(() => searchRef.current?.focus(), 0);
      } else if (e.key === "Escape") {
        setSelected(null);
      } else if (!typing && ["1", "2", "3"].includes(e.key)) {
        setPage((["overview", "nodes", "playground"] as Page[])[Number(e.key) - 1]);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const healthy = workers.filter((w) => w.healthy).length;
  const down = workers.length - healthy;

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase();
    return workers
      .filter((w) => (filter === "all" ? true : filter === "healthy" ? w.healthy : !w.healthy))
      .filter((w) =>
        !q
          ? true
          : [w.worker_id, w.gpu, w.os, w.address, w.rpc_addr].some((v) => v.toLowerCase().includes(q)),
      )
      .sort((a, b) =>
        sortAsc ? a.worker_id.localeCompare(b.worker_id) : b.worker_id.localeCompare(a.worker_id),
      );
  }, [workers, filter, query, sortAsc]);

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(text);
      setTimeout(() => setCopied(""), 1200);
    } catch {
      /* clipboard unavailable */
    }
  };

  const ask = async () => {
    if (!prompt.trim() || busy) return;
    setBusy(true);
    setAnswer("");
    setServedBy("");
    setPlacement("");
    setElapsed(null);
    const t0 = performance.now();
    try {
      const d = await infer(prompt, tier);
      setElapsed((performance.now() - t0) / 1000);
      if (d.ok) {
        setAnswer(d.text ?? "");
        setServedBy(d.worker_id ?? "");
        setPlacement(d.placement ?? "");
      } else setAnswer(`Error: ${d.error ?? "unknown"}`);
    } catch (e) {
      setAnswer(`Request failed: ${String(e)}`);
    } finally {
      setBusy(false);
    }
  };

  // topology geometry
  const V = 460;
  const C = V / 2;
  const R = 150;
  const remote = workers.filter((w) => !w.local);
  const localNode = workers.find((w) => w.local);
  const pos = remote.map((_, i) => {
    const a = ((-90 + (360 / Math.max(remote.length, 1)) * i) * Math.PI) / 180;
    return { x: C + R * Math.cos(a), y: C + R * Math.sin(a) };
  });

  const nav: { id: Page; label: string; icon: React.ReactNode }[] = [
    { id: "overview", label: "Overview", icon: <IconLayoutDashboard size={18} /> },
    { id: "nodes", label: "Nodes", icon: <IconServer size={18} /> },
    { id: "playground", label: "Playground", icon: <IconMessageChatbot size={18} /> },
  ];

  return (
    <div className="shell">
      {/* icon rail */}
      <aside className="rail">
        <div className="rail-logo" title="HyperPot">
          <IconAffiliate size={20} />
        </div>
        {nav.map((n) => (
          <button
            key={n.id}
            className={`rail-btn ${page === n.id ? "active" : ""}`}
            onClick={() => setPage(n.id)}
            aria-label={n.label}
          >
            {n.icon}
            <span>{n.label}</span>
          </button>
        ))}
      </aside>

      {/* section sidebar */}
      <aside className="side">
        <div className="side-title">HyperPot</div>
        <div className="side-group">Cluster</div>
        {nav.map((n) => (
          <button
            key={n.id}
            className={`side-item ${page === n.id ? "active" : ""}`}
            onClick={() => setPage(n.id)}
          >
            {n.icon}
            {n.label}
            {n.id === "nodes" && <span className="side-count">{workers.length}</span>}
          </button>
        ))}
        <div className="side-foot">
          <span className={`dot ${online ? "dot-ok" : "dot-down"}`} />
          <div>
            <div className="side-foot-t">{online ? "Controller online" : "Controller offline"}</div>
            <div className="side-foot-s">{CONTROLLER.replace("http://", "")}</div>
          </div>
        </div>
      </aside>

      {/* main */}
      <div className="main">
        <header className="topbar">
          <label className="search">
            <IconSearch size={15} />
            <input
              ref={searchRef}
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setPage("nodes");
              }}
              placeholder="Search nodes…"
            />
            <kbd>Ctrl K</kbd>
          </label>
          <span className={`conn ${online ? "conn-ok" : "conn-down"}`}>
            {online ? <IconCircleCheck size={14} /> : <IconCircleX size={14} />}
            {online ? "Connected" : "Disconnected"}
          </span>
        </header>

        <main className="content">
          {page === "overview" && (
            <>
              <div className="page-head">
                <IconLayoutDashboard size={18} />
                <h1>Overview</h1>
                <span className="page-sub">
                  · {workers.length} nodes · {healthy} healthy
                </span>
              </div>

              <div className="stats">
                <div className="stat">
                  <div className="stat-l">Nodes</div>
                  <div className="stat-v">{workers.length}</div>
                </div>
                <div className="stat">
                  <div className="stat-l">Healthy</div>
                  <div className="stat-v ok">{healthy}</div>
                </div>
                <div className="stat">
                  <div className="stat-l">Down</div>
                  <div className={`stat-v ${down ? "down" : ""}`}>{down}</div>
                </div>
                <div className="stat">
                  <div className="stat-l">Big-tier model (3B)</div>
                  <div className={`stat-v sm ${pipeline?.split ? "ok" : ""}`}>
                    {!pipeline || pipeline.state === "down"
                      ? "Not loaded"
                      : pipeline.state === "loading"
                        ? "Loading…"
                        : pipeline.split
                          ? "Split"
                          : "Local only"}
                  </div>
                </div>
              </div>

              <section className="card">
                <div className="card-head">
                  <h2>Topology</h2>
                  <span className="muted">Controller at center · nodes join automatically</span>
                </div>
                <svg className="topo" viewBox={`0 0 ${V} ${V}`} role="img" aria-label="Cluster topology">
                  {pos.map((p, i) => (
                    <line
                      key={`l${remote[i].worker_id}`}
                      x1={C}
                      y1={C}
                      x2={p.x}
                      y2={p.y}
                      className={`link ${remote[i].healthy ? "link-ok" : "link-down"}`}
                    />
                  ))}
                  <g className="hub">
                    <circle cx={C} cy={C} r={42} />
                    <text x={C} y={C - 3} className="hub-t">Controller</text>
                    <text x={C} y={C + 13} className="hub-s">{localNode ? `this laptop · ${localNode.gpu}` : "this laptop"}</text>
                  </g>
                  {pos.map((p, i) => {
                    const w = remote[i];
                    return (
                      <g
                        key={`n${w.worker_id}`}
                        transform={`translate(${p.x} ${p.y})`}
                        className={`tnode ${w.healthy ? "tn-ok" : "tn-down"} ${joined.has(w.worker_id) ? "tn-join" : ""}`}
                        onClick={() => setSelected(w)}
                      >
                        <circle r={32} />
                        <text y={-3} className="tn-id">{w.worker_id.replace("worker-", "")}</text>
                        <text y={12} className="tn-s">{w.healthy ? w.gpu : "DOWN"}</text>
                      </g>
                    );
                  })}
                </svg>
                {workers.length === 0 && (
                  <p className="empty">Waiting for laptops on this Wi-Fi. Start a worker and it appears here.</p>
                )}
              </section>

              <section className="card place-card">
                <div className="card-head">
                  <h2>Big-tier model placement</h2>
                  <span className="muted">
                    Reported by llama.cpp while loading
                    {pipeline && pipeline.generation > 0 && <> · build {pipeline.generation}</>}
                  </span>
                </div>
                {!pipeline || !pipeline.devices || pipeline.devices.length === 0 ? (
                  <p className="empty">
                    {pipeline?.state === "loading" ? "Loading the model…" : "Not loaded yet. Send a Big-tier prompt to load it."}
                  </p>
                ) : (
                  <div className="place-list">
                    {(() => {
                      const total = pipeline.devices.reduce((a, d) => a + d.mib, 0) || 1;
                      return pipeline.devices.map((d) => {
                        const remoteDev = d.device.startsWith("RPC");
                        return (
                          <div key={d.device} className="place-row">
                            <span className="place-dev mono">{d.device}</span>
                            <span className="place-where">{remoteDev ? "remote laptop" : d.device.startsWith("CPU") ? "this laptop · RAM" : "this laptop · GPU"}</span>
                            <div className="place-bar">
                              <span className={remoteDev ? "remote" : ""} style={{ width: `${(d.mib / total) * 100}%` }} />
                            </div>
                            <span className="num place-mib">{d.mib.toFixed(0)} MiB</span>
                          </div>
                        );
                      });
                    })()}
                    <div className="place-foot muted">
                      {pipeline.split
                        ? "Split: part of the model runs on a remote laptop."
                        : "Not split: the whole model is on this laptop."}
                    </div>
                  </div>
                )}
              </section>
            </>
          )}

          {page === "nodes" && (
            <>
              <div className="page-head">
                <IconServer size={18} />
                <h1>Nodes</h1>
                <span className="page-sub">
                  · {workers.length} nodes · {healthy} healthy
                </span>
              </div>

              <div className="tabs-row">
                <div className="tabs">
                  {(
                    [
                      ["all", "All", workers.length],
                      ["healthy", "Healthy", healthy],
                      ["down", "Down", down],
                    ] as [Filter, string, number][]
                  ).map(([id, label, n]) => (
                    <button
                      key={id}
                      className={`tab ${filter === id ? "active" : ""}`}
                      onClick={() => setFilter(id)}
                    >
                      {label} <span className="tab-n">{n}</span>
                    </button>
                  ))}
                </div>
              </div>

              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>
                        <button className="th-sort" onClick={() => setSortAsc((s) => !s)}>
                          Node <IconSelector size={13} />
                        </button>
                      </th>
                      <th>GPU</th>
                      <th>RPC endpoint</th>
                      <th>Last seen</th>
                      <th>Status</th>
                      <th className="th-r">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((w) => (
                      <tr key={w.worker_id} className={selected?.worker_id === w.worker_id ? "sel" : ""}>
                        <td>
                          <div className="node-cell">
                            <span className="avatar">
                              <OsIcon os={w.os} />
                            </span>
                            <div>
                              <div className="node-name">
                                {w.worker_id}
                                {w.local && <span className="tag tag-inline">This laptop</span>}
                              </div>
                              <div className="node-sub">{w.address.replace("http://", "")}</div>
                            </div>
                          </div>
                        </td>
                        <td>
                          <div className="cell-main">
                            <IconCpu size={14} /> {w.gpu}
                          </div>
                          <div className="node-sub">{w.os}</div>
                        </td>
                        <td>
                          <span className="mono">{w.rpc_addr || "—"}</span>
                        </td>
                        <td className="num">{w.last_seen_secs.toFixed(1)}s ago</td>
                        <td>
                          <StatusPill healthy={w.healthy} />
                        </td>
                        <td className="td-r">
                          <button className="icon-btn" onClick={() => setSelected(w)} aria-label="View details">
                            <IconEye size={16} />
                          </button>
                          <button
                            className="icon-btn"
                            onClick={() => copy(w.rpc_addr)}
                            disabled={!w.rpc_addr}
                            aria-label="Copy RPC endpoint"
                          >
                            {copied === w.rpc_addr && w.rpc_addr ? <IconCheck size={16} /> : <IconCopy size={16} />}
                          </button>
                        </td>
                      </tr>
                    ))}
                    {rows.length === 0 && (
                      <tr>
                        <td colSpan={6} className="empty-row">
                          {workers.length === 0 ? "No nodes have joined yet." : "No nodes match this filter."}
                        </td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
              <div className="table-foot">
                Showing <b>{rows.length}</b> of <b>{workers.length}</b>
              </div>
            </>
          )}

          {page === "playground" && (
            <>
              <div className="page-head">
                <IconMessageChatbot size={18} />
                <h1>Playground</h1>
                <span className="page-sub">· send a prompt to the cluster</span>
              </div>
              <section className="card">
                <label className="field-l" htmlFor="prompt">
                  Prompt
                </label>
                <textarea id="prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={4} />
                <div className="pg-controls">
                  <div className="seg" role="radiogroup" aria-label="Model tier">
                    <button className={tier === "small" ? "on" : ""} onClick={() => setTier("small")}>
                      <IconBolt size={14} /> Small · 1B
                    </button>
                    <button className={tier === "big" ? "on" : ""} onClick={() => setTier("big")}>
                      <IconAffiliate size={14} /> Big · 3B split
                    </button>
                  </div>
                  <button className="btn-primary" onClick={ask} disabled={busy || !online}>
                    {busy ? "Generating…" : "Send"}
                  </button>
                </div>
                {(answer || busy) && (
                  <div className="answer" aria-live="polite">
                    {servedBy && (
                      <div className="served">
                        <span className="tag">{servedBy}</span>
                        {elapsed !== null && <span className="muted num">{elapsed.toFixed(1)}s</span>}
                      </div>
                    )}
                    {placement && (
                      <div className="placement">
                        <span className="muted">Weights on:</span> <span className="mono">{placement}</span>
                      </div>
                    )}
                    <p>{busy ? "Generating…" : answer}</p>
                  </div>
                )}
              </section>
            </>
          )}
        </main>

        <footer className="hints">
          <kbd>Ctrl</kbd>
          <kbd>K</kbd> search
          <span className="sep" />
          <kbd>1</kbd>
          <kbd>2</kbd>
          <kbd>3</kbd> switch page
          <span className="sep" />
          <kbd>Esc</kbd> close panel
        </footer>
      </div>

      {/* nonmodal side panel (Sheet) */}
      {selected && (
        <aside className="sheet" aria-label="Node details">
          <div className="sheet-head">
            <span className="avatar">
              <OsIcon os={selected.os} />
            </span>
            <div className="sheet-title">
              <div className="node-name">{selected.worker_id}</div>
              <StatusPill healthy={selected.healthy} />
            </div>
            <button className="icon-btn" onClick={() => setSelected(null)} aria-label="Close">
              <IconX size={16} />
            </button>
          </div>
          <dl className="kv">
            <dt>GPU</dt>
            <dd>{selected.gpu}</dd>
            <dt>Operating system</dt>
            <dd>{selected.os}</dd>
            <dt>Worker address</dt>
            <dd className="mono">{selected.address}</dd>
            <dt>RPC endpoint</dt>
            <dd className="mono">{selected.rpc_addr || "—"}</dd>
            <dt>Last heartbeat</dt>
            <dd className="num">{selected.last_seen_secs.toFixed(1)}s ago</dd>
          </dl>
        </aside>
      )}
    </div>
  );
}
