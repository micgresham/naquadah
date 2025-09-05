# naquadah

A lightweight (mock) Starlink gRPC simulator and experimentation harness.

> NOT affiliated with SpaceX / Starlink. For local development, testing, and research only.

## Features

- gRPC server (default port 9200) implementing Device, Mesh, Unlock services
- Deterministic pseudo‑random simulation (seeded)
- Optional device YAML config (static profile overrides)
- Rules engine (latency, jitter, field/status overrides, error/drop injection, logging)
- Embedded Admin Web UI (enable with `-admin :PORT`) for live alarms, field overrides, rain fade & snow simulation, obstruction map, weather path editing
 - Versioned REST API (`/api/v1/*`) for external admin tooling (see `docs/rest-api.md`) with optional JWT auth (`-auth` flags)
- Data source modes:
	- Pure random (default)
	- Playback of recorded JSON time‑series (scalable with -playback-scale)
	- Baseline hybrid (recorded samples + jitter)
	- Sample recorder (synthetic snapshots every N seconds)
	- Real dish polling (append real samples with -real-target)
- Prometheus metrics exporter (-metrics) counting requests, rule hits, latency
- Cross‑platform build script (Linux / macOS arm/intel / Windows)
- Extensible action & provider architecture

## Quick Start

Generate a template device config:

```
naquadah -gen-config -config naquadah.yaml
```

Run with defaults (random mode):

```
naquadah
```

Enable verbose logging & events:
Launch with admin UI (serve on :8081):

```
naquadah -admin :8081
```

Then browse http://localhost:8081/ (see `docs/admin-ui.md`).


```
naquadah -noisy -events
```

Generate a rules file:

```
naquadah -gen-rules -rules rules.yaml
```

Run with rules:

```
naquadah -rules rules.yaml
```

## Data Source Modes

| Mode | Flags | Description |
|------|-------|-------------|
| Random | (none) | Default PRNG-based synthesis |
| Playback | -playback-json file.json | Replays recorded timeline (loop with -playback-loop, scale with -playback-scale) |
| Baseline Hybrid | -baseline-json file.json | Uses recorded samples + controlled jitter |
| Record (synthetic) | -record-json file.json | Capture simulator-generated samples to JSON (append) |

Record synthetic samples:

```
naquadah -record-json capture.json
```

Playback later:

```
naquadah -playback-json capture.json
```

Baseline hybrid (some variation):

```
naquadah -baseline-json capture.json
```

### More Examples

Record while serving (synthetic every 30s):

```
naquadah -record-json samples.json -record-interval 30s
```

Playback once (no loop):

```
naquadah -playback-json samples.json -playback-loop=false
```

Baseline jitter from file plus rules:

```
naquadah -baseline-json samples.json -rules rules.yaml
```

Enable real dish polling (adds real metrics alongside synthetic if recording simultaneously):

```
naquadah -real-target 192.168.100.1:9200 -record-json real_samples.json
```

Add auth token & custom timeout:

```
naquadah -real-target dish.lan:9200 -real-token SECRET -real-timeout 3s -record-json real_samples.json
```

## Recorder / Poller JSON Schema (array)

```json
[
	{
		"ts": "2025-09-04T12:00:00Z",
		"dish_status": { /* DishGetStatusResponse */ },
		"wifi_status": { /* WifiGetStatusResponse */ },
		"wifi_clients": { /* WifiGetClientsResponse */ },
		"speedtest": { /* SpeedTestResponse */ },
		"ping_all": { /* GetPingResponse */ },
		"transceiver_status": { /* TransceiverGetStatusResponse */ }
	}
]
```

Extend by editing `internal/sim/provider.go` (`Sample` struct) & recorder logic.

### Component Isolation
Disable router / WiFi related data while keeping dish telemetry by setting in your YAML config:

```yaml
enable_router: false
enable_wifi: false
```

WiFi endpoints then return minimal empty responses.

## Rules Engine

Generate template:

```
naquadah -gen-rules -rules rules.yaml
```

Example:

```yaml
- name: latency_and_error
	match:
		request_types: ["get_status"]
		probability: 0.3
	actions:
		- type: jitter
			ms: 200
			jitter_ms: 150
		- type: error
			error_code: unavailable
			message: "INJECTED_UNAVAILABLE"
		- type: log
			message: "Injected transient unavailability"
	stop: true

- name: override_downlink
	match:
		request_types: ["get_status"]
	actions:
		- type: field_override
			field: dish.downlink_bps
			value: 125000000
```

Supported actions:

- delay (ms)
- jitter (ms, jitter_ms)
- status (code, message)
- error (error_code, message)
- drop
- log (message)
- field_override (field, value)

Field override whitelist:

- dish.downlink_bps
- dish.uplink_bps
- dish.pop_ping_latency_ms
- wifi.ping_latency_ms
- wifi.downlink_bps
- wifi.ping_drop_rate

Request key values today (see `requestKey`):

```
get_status
dish_get_context
wifi_get_clients
get_ping
ping_host
speed_test
dish_get_config
wifi_get_config
```

## CLI Flags

| Flag | Description | Default |
|------|-------------|---------|
| -port | gRPC listen port | 9200 |
| -seed | PRNG seed | now |
| -noisy | Log each request | false |
| -events | Emit stream events | true |
| -config | Device YAML config path | naquadah.yaml |
| -gen-config | Write template config then exit | false |
| -rules | Rules YAML file | (none) |
| -gen-rules | Write example rules YAML then exit | false |
| -record-json | JSON file to write samples | (none) |
| -record-interval | Poll interval | 60s |
| -playback-json | Playback JSON file | (none) |
| -playback-loop | Loop playback | true |
| -baseline-json | Baseline JSON (hybrid mode) | (none) |
| -playback-scale | Playback advancement scale (>1 faster, <1 slower) | 1.0 |
| -metrics | Prometheus listen address (e.g. :9090) | (disabled) |
| -real-target | Real dish host:port to poll | (none) |
| -real-token | Auth token for real dish | (none) |
| -real-timeout | Per-request timeout for real poller | 5s |
| (YAML) enable_router | Toggle router/WiFi endpoints | true |
| (YAML) enable_wifi | Toggle WiFi-related data (alias) | true |
| -tls | Enable self-signed TLS (experimental) | false |
| -tls-cert | TLS cert file (with -tls) | (self-signed) |
| -tls-key | TLS key file (with -tls) | (self-signed) |
| -mdns | Announce _starlink._tcp via mDNS (TXT: app, ver, proto) | false |
| -admin | Serve admin web UI at host:port (e.g. :8081) | (disabled) |
| -admin-no-ui | Suppress embedded UI on -admin listener (API only) | false |
| -rest | Standalone REST API listener (no UI) | (disabled) |
| -auth | Enable JWT auth for admin/REST | false |
| -auth-issuer | Expected JWT issuer | (empty) |
| -auth-audience | Expected JWT audience | (empty) |
| -auth-hs256-secret | HS256 shared secret | (empty) |
| -auth-jwks | JWKS URL | (empty) |
| -auth-jwks-refresh | JWKS refresh interval | 5m |

## Build

Local build:

```
go build ./cmd/naquadah
```

Cross compile:

```
./scripts/build.sh
```

## License

Licensed under the MIT License. See `LICENSE` for details.

Outputs placed in `dist/`.

## Metrics

Expose metrics with:

```
naquadah -metrics :9090
```

Scrape `http://localhost:9090/metrics` for:
- naquadah_requests_total{key}
- naquadah_rule_hits_total{rule}
- naquadah_request_latency_seconds_bucket{key}

## Tests

```
go test ./...
```

Rules tests cover probability, delay/jitter bounds, field overrides, error & drop actions.

## Architecture (Brief)

```
Client -> gRPC Server -> Core
					|              |
					|              +-> Data Provider (random/playback/baseline)
					|              +-> Config Profile
					|              +-> Rules (pre: delay/jitter, post: mutate/error/drop)
					|
					+-> Recorder (optional side task)
```

More detail: see `docs/architecture.md` once generated.

Admin UI details: see `docs/admin-ui.md`.

## Extending

- Add rule action: update `internal/rules/engine.go`, docs & tests.
- Add provider: implement `DataProvider`, set via `core.SetDataProvider`.
- Add recorded fields: expand `Sample` struct & recorder logic.
- Add gRPC endpoint: add proto, implement stub in server/core, map request key.

## Roadmap

See `docs/roadmap.md` (coverage matrix, metrics exporter, web dashboard, scenario scripting, plugin architecture).

## License

Add a LICENSE file (MIT/Apache recommended).

## Disclaimer

Starlink, SpaceX, and related marks belong to their owners. This project is an independent simulator for educational/testing use.

