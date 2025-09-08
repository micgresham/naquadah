# Roadmap

## Near Term
- Dynamic endpoint coverage via protoreflect (replace static list test)
- Expanded real poller coverage (transceiver telemetry, unlock flow)
- Docs: metrics usage examples & Grafana dashboard JSON
- Rule engine: per-action metrics (latency_injected_ms histogram, drops counter)
- Admin HTTP: coverage map visualization & WebSocket event stream (live dish/wifi samples)
- Admin HTTP: persistent + TTL based overrides & multi-shot error injection codes
- Optional: hot-reload rules (may move to Mid Term if complexity grows)

## Recently Completed (2025-09-05)
- Admin runtime HTTP interface (`-admin`) with:
	- Alarm toggling
	- Field overrides (throughput, latency, etc.)
	- One-shot error injection
	- Obstruction map hole editing + randomization
- Expanded dish status richness (alerts, gps stats, obstruction stats, alignment stats, software update state & stats, initialization durations, ready states, config, class of service)
- Router / WiFi component isolation toggles in YAML (`enable_router`, `enable_wifi`)
- Standalone client utility (`cmd/naquadah-client`) with snapshot & streaming modes (speedtest support)
- Safety fallback ensuring every response carries a non-nil oneof payload (prevents client panics)
- Endpoint coverage baseline guard test (`internal/sim/coverage_test.go`)
- Real dish polling mode (`-real-target`, `-real-token`, `-real-timeout`) with periodic capture
- Real-only capture isolation via `-real-record-json` (prevents mixing sim & real samples)
- Separate REST-only listener (`-rest`) and UI suppression flag (`-admin-no-ui`)
- Recorder & poller: include speedtest, ping-all, transceiver placeholder fields
- Prometheus metrics exporter (request counts, rule hits, latency histograms) via `-metrics` flag
- Accelerated playback scaling factor (`-playback-scale`)
- Golden tests for playback & baseline providers
- Expanded Sample schema (ping host aggregate, speedtest stats, transceiver stub)

## Mid Term
- Scenario timeline scripting (enable/disable rule sets over time)
- Web dashboard (runtime metrics, rule hit counters, live sample view)
- Failure pattern libraries (rain fade, obstruction cycles)
- Stateful degradation modeling (progressive throughput decay, recovery curves)
- Hot-reload rule engine (file watch) (if not finished earlier)

## Long Term
- Provider plugin architecture (dynamic loading)
- Multi-dish topology simulation (mesh of dishes/routers)
- Historical ingestion / analytic replay (time-warp + metrics diff)
- Satellite pass / orbital geometry approximations
- Scenario DSL (YAML / Lua) driving rule activation & provider switching

## Ideas / Research
- Statistical jitter distributions (normal, Pareto, log-normal) selectable per action
- Composite anomaly simulation (progressive packet loss -> outage cascade)
- ML-driven anomaly playback (train from real captures)
- Network impairment scripting integration (tc / netem wrappers)
