use serde::{Deserialize, Serialize};

/// Version of the JSON telemetry contract shared with the Go controller.
pub const TELEMETRY_SCHEMA_VERSION: u8 = 1;

/// A point-in-time measurement collected by a worker.
///
/// Every sensor group and every value which can be unavailable is optional.
/// `None` serializes as JSON `null`; callers must never infer a missing value
/// from a GPU name, operating system, or another field.
#[derive(Clone, Debug, Deserialize, PartialEq, Serialize)]
pub struct Telemetry {
    pub schema_version: u8,
    pub collected_at_unix_ms: u64,
    pub gpu: Option<GpuTelemetry>,
    pub memory: Option<MemoryTelemetry>,
    pub power: Option<PowerTelemetry>,
}

impl Telemetry {
    pub fn unavailable(collected_at_unix_ms: u64) -> Self {
        Self {
            schema_version: TELEMETRY_SCHEMA_VERSION,
            collected_at_unix_ms,
            gpu: None,
            memory: None,
            power: None,
        }
    }
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize)]
pub struct GpuTelemetry {
    /// Collector/backend which supplied the values (currently `nvidia-smi`).
    pub source: String,
    pub name: Option<String>,
    pub vram_total_mib: Option<u64>,
    pub vram_free_mib: Option<u64>,
    pub utilization_percent: Option<f64>,
    pub temperature_celsius: Option<f64>,
    pub power_draw_watts: Option<f64>,
    pub power_limit_watts: Option<f64>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize)]
pub struct MemoryTelemetry {
    pub source: String,
    pub total_mib: Option<u64>,
    pub available_mib: Option<u64>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize)]
pub struct PowerTelemetry {
    pub source: String,
    pub battery_percent: Option<f64>,
    pub ac_online: Option<bool>,
}

/// Collect all telemetry available on this host. A failing collector only makes
/// its own group `None`; it does not prevent registration or heartbeats.
pub fn collect() -> Telemetry {
    let collected_at_unix_ms = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0);
    Telemetry {
        schema_version: TELEMETRY_SCHEMA_VERSION,
        collected_at_unix_ms,
        gpu: collect_nvidia_gpu(),
        memory: collect_memory(),
        power: collect_power(),
    }
}

fn command_output(program: &str, args: &[&str]) -> Option<String> {
    let output = std::process::Command::new(program)
        .args(args)
        .output()
        .ok()?;
    if !output.status.success() {
        return None;
    }
    Some(String::from_utf8_lossy(&output.stdout).trim().to_string())
}

fn optional_text(value: &str) -> Option<String> {
    let value = value.trim();
    if value.is_empty() || value.eq_ignore_ascii_case("n/a") || value == "[N/A]" {
        None
    } else {
        Some(value.to_string())
    }
}

fn optional_f64(value: &str) -> Option<f64> {
    optional_text(value)?
        .parse::<f64>()
        .ok()
        .filter(|v| v.is_finite())
}

fn optional_u64(value: &str) -> Option<u64> {
    let value = optional_f64(value)?;
    if value < 0.0 {
        None
    } else {
        Some(value.round() as u64)
    }
}

/// Collect the first NVIDIA GPU reported by nvidia-smi. HyperPod currently
/// exposes one RPC accelerator per laptop; multi-GPU hosts are future work.
fn collect_nvidia_gpu() -> Option<GpuTelemetry> {
    let output = command_output(
        "nvidia-smi",
        &[
            "--query-gpu=name,memory.total,memory.free,utilization.gpu,temperature.gpu,power.draw,power.limit",
            "--format=csv,noheader,nounits",
        ],
    )?;
    parse_nvidia_csv(output.lines().next()?)
}

fn parse_nvidia_csv(line: &str) -> Option<GpuTelemetry> {
    let fields: Vec<&str> = line.split(',').map(str::trim).collect();
    if fields.len() != 7 {
        return None;
    }
    let telemetry = GpuTelemetry {
        source: "nvidia-smi".to_string(),
        name: optional_text(fields[0]),
        vram_total_mib: optional_u64(fields[1]),
        vram_free_mib: optional_u64(fields[2]),
        utilization_percent: optional_f64(fields[3]),
        temperature_celsius: optional_f64(fields[4]),
        power_draw_watts: optional_f64(fields[5]),
        power_limit_watts: optional_f64(fields[6]),
    };
    // A successful command which returned seven N/A values is not useful data.
    if telemetry.name.is_none()
        && telemetry.vram_total_mib.is_none()
        && telemetry.vram_free_mib.is_none()
        && telemetry.utilization_percent.is_none()
        && telemetry.temperature_celsius.is_none()
        && telemetry.power_draw_watts.is_none()
        && telemetry.power_limit_watts.is_none()
    {
        None
    } else {
        Some(telemetry)
    }
}

#[cfg(target_os = "linux")]
fn collect_memory() -> Option<MemoryTelemetry> {
    let text = std::fs::read_to_string("/proc/meminfo").ok()?;
    let mut total_kib = None;
    let mut available_kib = None;
    for line in text.lines() {
        if let Some(value) = line.strip_prefix("MemTotal:") {
            total_kib = value.split_whitespace().next()?.parse::<u64>().ok();
        } else if let Some(value) = line.strip_prefix("MemAvailable:") {
            available_kib = value.split_whitespace().next()?.parse::<u64>().ok();
        }
    }
    if total_kib.is_none() && available_kib.is_none() {
        return None;
    }
    Some(MemoryTelemetry {
        source: "proc-meminfo".to_string(),
        total_mib: total_kib.map(|v| v / 1024),
        available_mib: available_kib.map(|v| v / 1024),
    })
}

#[cfg(target_os = "windows")]
fn collect_memory() -> Option<MemoryTelemetry> {
    let script = "$o=Get-CimInstance Win32_OperatingSystem; Write-Output \"$($o.TotalVisibleMemorySize),$($o.FreePhysicalMemory)\"";
    let output = powershell(script)?;
    let fields: Vec<&str> = output.trim().split(',').collect();
    if fields.len() != 2 {
        return None;
    }
    let total_kib = optional_u64(fields[0]);
    let free_kib = optional_u64(fields[1]);
    if total_kib.is_none() && free_kib.is_none() {
        return None;
    }
    Some(MemoryTelemetry {
        source: "windows-cim".to_string(),
        total_mib: total_kib.map(|v| v / 1024),
        available_mib: free_kib.map(|v| v / 1024),
    })
}

#[cfg(target_os = "macos")]
fn collect_memory() -> Option<MemoryTelemetry> {
    let total_bytes = command_output("sysctl", &["-n", "hw.memsize"])?
        .parse::<u64>()
        .ok()?;
    // macOS does not expose a directly equivalent MemAvailable value through
    // this stable command, so leave it explicitly unavailable.
    Some(MemoryTelemetry {
        source: "macos-sysctl".to_string(),
        total_mib: Some(total_bytes / 1024 / 1024),
        available_mib: None,
    })
}

#[cfg(not(any(target_os = "linux", target_os = "windows", target_os = "macos")))]
fn collect_memory() -> Option<MemoryTelemetry> {
    None
}

#[cfg(target_os = "linux")]
fn collect_power() -> Option<PowerTelemetry> {
    let entries = std::fs::read_dir("/sys/class/power_supply").ok()?;
    let mut battery_percent = None;
    let mut ac_online = None;
    for entry in entries.flatten() {
        let path = entry.path();
        let kind = std::fs::read_to_string(path.join("type"))
            .unwrap_or_default()
            .trim()
            .to_string();
        match kind.as_str() {
            "Battery" if battery_percent.is_none() => {
                battery_percent = std::fs::read_to_string(path.join("capacity"))
                    .ok()
                    .and_then(|v| optional_f64(v.trim()));
            }
            "Mains" if ac_online.is_none() => {
                ac_online = std::fs::read_to_string(path.join("online"))
                    .ok()
                    .and_then(|v| match v.trim() {
                        "1" => Some(true),
                        "0" => Some(false),
                        _ => None,
                    });
            }
            _ => {}
        }
    }
    if battery_percent.is_none() && ac_online.is_none() {
        None
    } else {
        Some(PowerTelemetry {
            source: "linux-power-supply".to_string(),
            battery_percent,
            ac_online,
        })
    }
}

#[cfg(target_os = "windows")]
fn collect_power() -> Option<PowerTelemetry> {
    let script = "$b=Get-CimInstance Win32_Battery | Select-Object -First 1; $p=Get-CimInstance -Namespace root\\wmi -Class BatteryStatus | Select-Object -First 1; Write-Output \"$($b.EstimatedChargeRemaining),$($p.PowerOnline)\"";
    let output = powershell(script)?;
    let fields: Vec<&str> = output.trim().split(',').collect();
    if fields.len() != 2 {
        return None;
    }
    let battery_percent = optional_f64(fields[0]);
    let ac_online = match fields[1].trim().to_ascii_lowercase().as_str() {
        "true" => Some(true),
        "false" => Some(false),
        _ => None,
    };
    if battery_percent.is_none() && ac_online.is_none() {
        None
    } else {
        Some(PowerTelemetry {
            source: "windows-cim-wmi".to_string(),
            battery_percent,
            ac_online,
        })
    }
}

#[cfg(target_os = "windows")]
fn powershell(script: &str) -> Option<String> {
    command_output(
        "powershell",
        &["-NoProfile", "-NonInteractive", "-Command", script],
    )
    .or_else(|| {
        command_output(
            "pwsh",
            &["-NoProfile", "-NonInteractive", "-Command", script],
        )
    })
}

#[cfg(target_os = "macos")]
fn collect_power() -> Option<PowerTelemetry> {
    let output = command_output("pmset", &["-g", "batt"])?;
    let battery_percent = output
        .split('%')
        .next()
        .and_then(|before| before.rsplit(|c: char| !c.is_ascii_digit()).next())
        .and_then(optional_f64);
    let ac_online = if output.contains("Now drawing from 'AC Power'") {
        Some(true)
    } else if output.contains("Now drawing from 'Battery Power'") {
        Some(false)
    } else {
        None
    };
    if battery_percent.is_none() && ac_online.is_none() {
        None
    } else {
        Some(PowerTelemetry {
            source: "macos-pmset".to_string(),
            battery_percent,
            ac_online,
        })
    }
}

#[cfg(not(any(target_os = "linux", target_os = "windows", target_os = "macos")))]
fn collect_power() -> Option<PowerTelemetry> {
    None
}
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn unavailable_values_serialize_as_null() {
        let value = serde_json::to_value(Telemetry::unavailable(42)).unwrap();
        assert_eq!(value["schema_version"], 1);
        assert_eq!(value["collected_at_unix_ms"], 42);
        assert!(value["gpu"].is_null());
        assert!(value["memory"].is_null());
        assert!(value["power"].is_null());
    }

    #[test]
    fn partially_available_gpu_keeps_unknown_field_null() {
        let telemetry = Telemetry {
            schema_version: TELEMETRY_SCHEMA_VERSION,
            collected_at_unix_ms: 42,
            gpu: Some(GpuTelemetry {
                source: "nvidia-smi".into(),
                name: Some("NVIDIA GPU".into()),
                vram_total_mib: Some(4096),
                vram_free_mib: Some(3716),
                utilization_percent: Some(10.0),
                temperature_celsius: Some(56.0),
                power_draw_watts: Some(10.07),
                power_limit_watts: None,
            }),
            memory: None,
            power: None,
        };
        let value = serde_json::to_value(telemetry).unwrap();
        assert_eq!(value["gpu"]["vram_free_mib"], 3716);
        assert!(value["gpu"]["power_limit_watts"].is_null());
    }

    #[test]
    fn parses_measured_nvidia_csv_and_keeps_na_null() {
        let gpu = parse_nvidia_csv(
            "NVIDIA GeForce RTX 3050 Laptop GPU, 4096, 3716, 10, 56, 10.07, [N/A]",
        )
        .unwrap();
        assert_eq!(
            gpu.name.as_deref(),
            Some("NVIDIA GeForce RTX 3050 Laptop GPU")
        );
        assert_eq!(gpu.vram_total_mib, Some(4096));
        assert_eq!(gpu.vram_free_mib, Some(3716));
        assert_eq!(gpu.utilization_percent, Some(10.0));
        assert_eq!(gpu.temperature_celsius, Some(56.0));
        assert_eq!(gpu.power_draw_watts, Some(10.07));
        assert_eq!(gpu.power_limit_watts, None);
    }

    #[test]
    fn rejects_malformed_or_fully_unavailable_nvidia_output() {
        assert!(parse_nvidia_csv("not,csv").is_none());
        assert!(parse_nvidia_csv("N/A,N/A,N/A,N/A,N/A,N/A,N/A").is_none());
    }

    #[test]
    fn numeric_parsers_do_not_turn_unknown_into_zero() {
        assert_eq!(optional_u64("4096"), Some(4096));
        assert_eq!(optional_u64("[N/A]"), None);
        assert_eq!(optional_f64("N/A"), None);
        assert_eq!(optional_f64(""), None);
    }
}
