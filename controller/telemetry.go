package main

// TelemetrySchemaVersion is the JSON contract version shared with workers.
const TelemetrySchemaVersion = 1

// Telemetry is a point-in-time measurement reported by a worker.
// Sensor groups and values that may be unavailable use pointers, so JSON emits
// null rather than a fabricated zero. Missing values must never be inferred
// from a GPU name, OS, or another field.
type Telemetry struct {
	SchemaVersion     int              `json:"schema_version"`
	CollectedAtUnixMs int64            `json:"collected_at_unix_ms"`
	GPU               *GPUTelemetry    `json:"gpu"`
	Memory            *MemoryTelemetry `json:"memory"`
	Power             *PowerTelemetry  `json:"power"`
}

type GPUTelemetry struct {
	Source             string   `json:"source"`
	Name               *string  `json:"name"`
	VRAMTotalMiB       *uint64  `json:"vram_total_mib"`
	VRAMFreeMiB        *uint64  `json:"vram_free_mib"`
	UtilizationPercent *float64 `json:"utilization_percent"`
	TemperatureCelsius *float64 `json:"temperature_celsius"`
	PowerDrawWatts     *float64 `json:"power_draw_watts"`
	PowerLimitWatts    *float64 `json:"power_limit_watts"`
}

type MemoryTelemetry struct {
	Source       string  `json:"source"`
	TotalMiB     *uint64 `json:"total_mib"`
	AvailableMiB *uint64 `json:"available_mib"`
}

type PowerTelemetry struct {
	Source         string   `json:"source"`
	BatteryPercent *float64 `json:"battery_percent"`
	ACOnline       *bool    `json:"ac_online"`
}

func (t *Telemetry) validVersion() bool {
	return t == nil || t.SchemaVersion == TelemetrySchemaVersion
}
