// HyperPot Worker (Phase 1)
//
// A worker agent that:
//   - registers with the controller         (POST {controller}/register)
//   - sends heartbeats every few seconds     (POST {controller}/heartbeat)
//   - serves inference requests              (POST /run  on its own port)
//   - runs inference by invoking the local llama-cli binary on the model
//
// Phase 1 scope: one worker, runs the WHOLE model locally (no split yet).
// Later phases replace the "run whole model" step with "run my layer slice".
//
// Configuration via environment variables:
//   HYPERPOT_WORKER_ID        default "worker-A"
//   HYPERPOT_WORKER_BIND      default "0.0.0.0:9100"   (where /run listens)
//   HYPERPOT_WORKER_ADVERTISE default "http://127.0.0.1:9100" (told to controller)
//   HYPERPOT_CONTROLLER       default "http://127.0.0.1:8080"
//   HYPERPOT_LLAMA_CLI        path to llama-cli binary (required)
mod telemetry;
//   HYPERPOT_MODEL            path to .gguf model (required)
//   HYPERPOT_NGL              gpu layers, default "99"

use serde::{Deserialize, Serialize};
use std::process::Command;
use std::sync::Arc;
use std::thread;
use std::time::Duration;

#[derive(Serialize)]
struct RegisterReq {
    worker_id: String,
    address: String,
    rpc_addr: String,
    gpu: String,
    os: String,
    telemetry: telemetry::Telemetry,
}

#[derive(Serialize)]
struct HeartbeatReq {
    worker_id: String,
    telemetry: telemetry::Telemetry,
}

// Broadcast payload for LAN auto-discovery (matches the controller's listener).
#[derive(Serialize)]
struct DiscoveryAnnounce {
    #[serde(rename = "type")]
    kind: String, // "hyperpot-worker"
    worker_id: String,
    address: String,  // http base, e.g. http://192.168.1.38:9100
    rpc_addr: String, // host:port of this worker's ggml-rpc-server
    gpu: String,
    os: String,
    telemetry: telemetry::Telemetry,
}

#[derive(Deserialize)]
struct RunReq {
    prompt: String,
    #[serde(default)]
    n_predict: i32,
}

#[derive(Serialize)]
struct RunResp {
    ok: bool,
    text: String,
    error: String,
}

#[derive(Clone)]
struct Config {
    worker_id: String,
    bind: String,
    advertise: String,
    controller: String,
    llama_cli: String,
    model: String,
    ngl: String,
}

fn env_or(key: &str, default: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| default.to_string())
}

fn load_config() -> Result<Config, String> {
    let llama_cli = std::env::var("HYPERPOT_LLAMA_CLI")
        .map_err(|_| "HYPERPOT_LLAMA_CLI not set (path to llama-cli)".to_string())?;
    let model = std::env::var("HYPERPOT_MODEL")
        .map_err(|_| "HYPERPOT_MODEL not set (path to .gguf model)".to_string())?;
    if !std::path::Path::new(&llama_cli).exists() {
        return Err(format!("llama-cli not found at {llama_cli}"));
    }
    if !std::path::Path::new(&model).exists() {
        return Err(format!("model not found at {model}"));
    }
    Ok(Config {
        worker_id: env_or("HYPERPOT_WORKER_ID", "worker-A"),
        bind: env_or("HYPERPOT_WORKER_BIND", "0.0.0.0:9100"),
        advertise: env_or("HYPERPOT_WORKER_ADVERTISE", "http://127.0.0.1:9100"),
        controller: env_or("HYPERPOT_CONTROLLER", "http://127.0.0.1:8080"),
        llama_cli,
        model,
        ngl: env_or("HYPERPOT_NGL", "99"),
    })
}

type SharedTelemetry = Arc<std::sync::RwLock<telemetry::Telemetry>>;

/// Start one sampler shared by registration, heartbeats, and discovery. This
/// avoids launching duplicate nvidia-smi/PowerShell collectors on Windows.
fn start_telemetry_collector() -> SharedTelemetry {
    let shared = Arc::new(std::sync::RwLock::new(telemetry::collect()));
    let background = Arc::clone(&shared);
    thread::spawn(move || loop {
        thread::sleep(Duration::from_secs(5));
        let latest = telemetry::collect();
        if let Ok(mut current) = background.write() {
            *current = latest;
        }
    });
    shared
}

fn telemetry_snapshot(shared: &SharedTelemetry) -> telemetry::Telemetry {
    shared
        .read()
        .map(|value| value.clone())
        .unwrap_or_else(|_| telemetry::Telemetry::unavailable(0))
}

fn measured_gpu_name(snapshot: &telemetry::Telemetry) -> String {
    snapshot
        .gpu
        .as_ref()
        .and_then(|gpu| gpu.name.clone())
        .unwrap_or_else(|| "unknown".to_string())
}

fn detect_os() -> String {
    std::env::consts::OS.to_string()
}

/// Register with the controller (retries until it succeeds).
fn register(cfg: &Config, shared: &SharedTelemetry) {
    loop {
        let telemetry = telemetry_snapshot(shared);
        let req = RegisterReq {
            worker_id: cfg.worker_id.clone(),
            address: cfg.advertise.clone(),
            rpc_addr: env_or("HYPERPOT_RPC_ADVERTISE", ""),
            gpu: measured_gpu_name(&telemetry),
            os: detect_os(),
            telemetry,
        };
        match ureq::post(&format!("{}/register", cfg.controller)).send_json(&req) {
            Ok(_) => {
                println!("[worker] registered with controller at {}", cfg.controller);
                return;
            }
            Err(e) => {
                eprintln!("[worker] register failed ({e}); retrying in 2s");
                thread::sleep(Duration::from_secs(2));
            }
        }
    }
}

/// Background heartbeat loop.
fn start_heartbeat(cfg: Config, shared: SharedTelemetry) {
    thread::spawn(move || loop {
        let req = HeartbeatReq {
            worker_id: cfg.worker_id.clone(),
            telemetry: telemetry_snapshot(&shared),
        };
        match ureq::post(&format!("{}/heartbeat", cfg.controller)).send_json(&req) {
            Ok(_) => {}
            Err(e) => eprintln!("[worker] heartbeat failed: {e}"),
        }
        thread::sleep(Duration::from_secs(3));
    });
}

/// LAN auto-discovery: periodically UDP-broadcast this worker's presence so the
/// controller can auto-register it without any manual IP configuration.
fn start_discovery_broadcast(cfg: Config, shared: SharedTelemetry) {
    const DISCOVERY_PORT: u16 = 50999;
    thread::spawn(move || {
        let socket = match std::net::UdpSocket::bind("0.0.0.0:0") {
            Ok(s) => s,
            Err(e) => {
                eprintln!("[worker] discovery: could not open UDP socket: {e}");
                return;
            }
        };
        if let Err(e) = socket.set_broadcast(true) {
            eprintln!("[worker] discovery: set_broadcast failed: {e}");
            return;
        }
        let target = format!("255.255.255.255:{DISCOVERY_PORT}");
        println!("[worker] discovery: broadcasting presence to {target} every 3s");
        loop {
            let telemetry = telemetry_snapshot(&shared);
            let announce = DiscoveryAnnounce {
                kind: "hyperpot-worker".to_string(),
                worker_id: cfg.worker_id.clone(),
                address: cfg.advertise.clone(),
                rpc_addr: env_or("HYPERPOT_RPC_ADVERTISE", ""),
                gpu: measured_gpu_name(&telemetry),
                os: detect_os(),
                telemetry,
            };
            match serde_json::to_vec(&announce) {
                Ok(payload) => {
                    if let Err(e) = socket.send_to(&payload, &target) {
                        eprintln!("[worker] discovery: broadcast failed: {e}");
                    }
                }
                Err(e) => eprintln!("[worker] discovery: serialize failed: {e}"),
            }
            thread::sleep(Duration::from_secs(3));
        }
    });
}

/// Run llama-cli locally on the prompt and return the generated text.
/// Phase 1: run the whole model. `-st` = single-turn (no interactive chat).
fn run_inference(cfg: &Config, prompt: &str, n_predict: i32) -> Result<String, String> {
    let n = if n_predict > 0 { n_predict } else { 64 };
    let output = Command::new(&cfg.llama_cli)
        .arg("-m")
        .arg(&cfg.model)
        .arg("-ngl")
        .arg(&cfg.ngl)
        .arg("-n")
        .arg(n.to_string())
        .arg("--no-display-prompt")
        .arg("-st")
        .arg("-p")
        .arg(prompt)
        .output()
        .map_err(|e| format!("failed to launch llama-cli: {e}"))?;

    if !output.status.success() {
        return Err(format!(
            "llama-cli exited with status {:?}: {}",
            output.status.code(),
            String::from_utf8_lossy(&output.stderr)
        ));
    }
    let raw = String::from_utf8_lossy(&output.stdout).to_string();
    Ok(clean_llama_output(&raw, prompt))
}

/// llama-cli prints a banner, the echoed prompt (after "> "), the answer, then a
/// "[ Prompt: ... ]" stats line and "Exiting...". Extract just the answer.
fn clean_llama_output(raw: &str, prompt: &str) -> String {
    // Strip backspace artifacts from the loading spinner.
    let cleaned: String = raw.chars().filter(|&c| c != '\u{8}').collect();

    let mut text = cleaned.as_str();

    // Cut everything up to and including the echoed prompt line "> <prompt>".
    let marker = format!("> {prompt}");
    if let Some(idx) = text.find(&marker) {
        text = &text[idx + marker.len()..];
    } else if let Some(idx) = text.rfind("> ") {
        // Fallback: last "> " line is the prompt echo.
        if let Some(nl) = text[idx..].find('\n') {
            text = &text[idx + nl..];
        }
    }

    // Cut the trailing stats line "[ Prompt:" and anything after.
    if let Some(idx) = text.find("\n[ Prompt:") {
        text = &text[..idx];
    }
    // Also drop a trailing "Exiting..." if present.
    if let Some(idx) = text.find("Exiting...") {
        text = &text[..idx];
    }

    text.trim().to_string()
}

fn handle_run(cfg: &Config, body: &str) -> RunResp {
    let req: RunReq = match serde_json::from_str(body) {
        Ok(r) => r,
        Err(e) => {
            return RunResp {
                ok: false,
                text: String::new(),
                error: format!("bad /run payload: {e}"),
            }
        }
    };
    println!(
        "[worker] /run prompt=\"{}\" n_predict={}",
        req.prompt.chars().take(60).collect::<String>(),
        req.n_predict
    );
    match run_inference(cfg, &req.prompt, req.n_predict) {
        Ok(text) => RunResp {
            ok: true,
            text,
            error: String::new(),
        },
        Err(e) => RunResp {
            ok: false,
            text: String::new(),
            error: e,
        },
    }
}

fn main() {
    let cfg = match load_config() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("[worker] config error: {e}");
            std::process::exit(1);
        }
    };

    println!(
        "[worker] id={} bind={} advertise={} controller={}",
        cfg.worker_id, cfg.bind, cfg.advertise, cfg.controller
    );
    println!("[worker] llama_cli={} model={}", cfg.llama_cli, cfg.model);

    let telemetry = start_telemetry_collector();
    let initial = telemetry_snapshot(&telemetry);
    println!(
        "[worker] telemetry={}",
        serde_json::to_string(&initial).unwrap_or_else(|_| "null".to_string())
    );

    // Register, then start heartbeats and LAN discovery with the same measured snapshot.
    register(&cfg, &telemetry);
    start_heartbeat(cfg.clone(), Arc::clone(&telemetry));
    start_discovery_broadcast(cfg.clone(), Arc::clone(&telemetry));

    // Serve /run.
    let server = tiny_http::Server::http(&cfg.bind)
        .unwrap_or_else(|e| panic!("failed to bind {}: {e}", cfg.bind));
    println!("[worker] serving /run on {}", cfg.bind);

    let cfg = Arc::new(cfg);
    for mut request in server.incoming_requests() {
        let method = request.method().to_string();
        let url = request.url().to_string();

        if method == "POST" && url == "/run" {
            let mut body = String::new();
            if let Err(e) = request.as_reader().read_to_string(&mut body) {
                let resp = RunResp {
                    ok: false,
                    text: String::new(),
                    error: format!("read body failed: {e}"),
                };
                let _ = request.respond(json_response(&resp));
                continue;
            }
            let resp = handle_run(&cfg, &body);
            let _ = request.respond(json_response(&resp));
        } else {
            let resp = RunResp {
                ok: false,
                text: String::new(),
                error: "not found".to_string(),
            };
            let r = json_response(&resp).with_status_code(404);
            let _ = request.respond(r);
        }
    }
}

fn json_response(resp: &RunResp) -> tiny_http::Response<std::io::Cursor<Vec<u8>>> {
    let body = serde_json::to_vec(resp).unwrap_or_default();
    let header =
        tiny_http::Header::from_bytes(&b"Content-Type"[..], &b"application/json"[..]).unwrap();
    tiny_http::Response::from_data(body).with_header(header)
}
