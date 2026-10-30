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
        gpu: collect_gpu(),
        memory: collect_memory(),
        power: collect_power(),
    }
}

/// Collect GPU facts from whichever backend this host supports. NVIDIA hosts
/// report full metrics; Apple Silicon reports only the values macOS exposes.
fn collect_gpu() -> Option<GpuTelemetry> {
    if let Some(nvidia) = collect_nvidia_gpu() {
        return Some(nvidia);
    }
    collect_apple_gpu()
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
    // macOS has no MemAvailable equivalent, so available memory is derived from
    // vm_stat page counts. If vm_stat is missing or unparsable it stays null.
    let available_mib =
        command_output("vm_stat", &[]).and_then(|out| parse_vm_stat_available_mib(&out));
    Some(MemoryTelemetry {
        source: "macos-sysctl+vm_stat".to_string(),
        total_mib: Some(total_bytes / 1024 / 1024),
        available_mib,
    })
}

/// Sum the vm_stat page classes macOS can hand back to a new allocation and
/// convert them to MiB using the reported page size.
///
/// Available ≈ free + inactive + speculative + purgeable. This is an estimate
/// of reclaimable memory, not a kernel-published "available" counter.
#[cfg(any(target_os = "macos", test))]
fn parse_vm_stat_available_mib(output: &str) -> Option<u64> {
    let mut page_size: Option<u64> = None;
    let mut pages: u64 = 0;
    let mut matched = false;
    for line in output.lines() {
        if page_size.is_none() {
            if let Some(rest) = line.split("page size of").nth(1) {
                page_size = rest
                    .split_whitespace()
                    .next()
                    .and_then(|v| v.parse::<u64>().ok());
            }
        }
        let (label, value) = match line.split_once(':') {
            Some(parts) => parts,
            None => continue,
        };
        let label = label.trim();
        let reclaimable = matches!(
            label,
            "Pages free" | "Pages inactive" | "Pages speculative" | "Pages purgeable"
        );
        if !reclaimable {
            continue;
        }
        if let Ok(count) = value.trim().trim_end_matches('.').parse::<u64>() {
            pages += count;
            matched = true;
        }
    }
    if !matched {
        return None;
    }
    let page_size = page_size?;
    Some(pages.saturating_mul(page_size) / 1024 / 1024)
}

/// Report the Apple GPU that macOS names. Apple Silicon shares one unified
/// memory pool, so there is no separate VRAM figure; those fields stay null
/// rather than reusing system RAM as if it were dedicated video memory.
#[cfg(target_os = "macos")]
fn collect_apple_gpu() -> Option<GpuTelemetry> {
    use std::sync::OnceLock;
    // The chipset name never changes, and system_profiler is slow, so resolve
    // it once instead of on every five-second sample.
    static NAME: OnceLock<Option<String>> = OnceLock::new();
    let name = NAME
        .get_or_init(|| {
            command_output("system_profiler", &["SPDisplaysDataType"])
                .and_then(|out| parse_chipset_model(&out))
        })
        .clone()?;
    Some(GpuTelemetry {
        source: "macos-system_profiler".to_string(),
        name: Some(name),
        vram_total_mib: None,
        vram_free_mib: None,
        utilization_percent: None,
        temperature_celsius: None,
        power_draw_watts: None,
        power_limit_watts: None,
    })
}

#[cfg(not(target_os = "macos"))]
fn collect_apple_gpu() -> Option<GpuTelemetry> {
    None
}

/// Extract the "Chipset Model" line from system_profiler SPDisplaysDataType.
#[cfg(any(target_os = "macos", test))]
fn parse_chipset_model(output: &str) -> Option<String> {
    output
        .lines()
        .find_map(|line| line.split_once("Chipset Model:").map(|(_, v)| v))
        .and_then(|value| optional_text(value))
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

    #[test]
    fn parses_apple_chipset_model() {
        let output = "Graphics/Displays:\n\n    Apple M4:\n\n      Chipset Model: Apple M4\n      Type: GPU\n      Bus: Built-In\n      Total Number of Cores: 10\n      Metal Support: Metal 3\n";
        assert_eq!(parse_chipset_model(output).as_deref(), Some("Apple M4"));
        assert!(parse_chipset_model("Graphics/Displays:\n").is_none());
    }

    #[test]
    fn sums_reclaimable_vm_stat_pages() {
        let output = concat!(
            "Mach Virtual Memory Statistics: (page size of 16384 bytes)\n",
            "Pages free:                               32768.\n",
            "Pages active:                            100000.\n",
            "Pages inactive:                           16384.\n",
            "Pages speculative:                         8192.\n",
            "Pages wired down:                         50000.\n",
            "Pages purgeable:                           8192.\n",
        );
        // (32768 + 16384 + 8192 + 8192) pages * 16 KiB = 1024 MiB
        assert_eq!(parse_vm_stat_available_mib(output), Some(1024));
    }

    #[test]
    fn vm_stat_without_usable_counters_is_unavailable() {
        assert!(parse_vm_stat_available_mib("").is_none());
        assert!(parse_vm_stat_available_mib("Pages active: 100000.\n").is_none());
    }
}
