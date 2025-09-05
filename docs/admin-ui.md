# Admin Web UI

The built‑in admin interface (enable with `-admin :8081`) provides real‑time control and visibility into the simulator.

## Enable

```
naquadah -admin :8081
```
Then open: http://localhost:8081/ (root path only).

## Sections

### Alarms
Toggle dish alert flags (e.g. `thermal_throttle`, `lower_signal_than_predicted`). Buttons light up red when active. "Clear All" resets all alarm overrides.

### Field Overrides
Select a known field or specify a custom JSON path (e.g. `dish.device_state.uptime_s`). Numeric values become persistent overrides until cleared. Raw string overrides allow enums/bools as text.

### Error Injection
Injects a single gRPC error for the next matching device request (UNAVAILABLE, INTERNAL, etc.). Use Disable to clear.

### Rain Fade
Simulates a moving rain cell across the 8x8 weather grid.
* Intensity slider 0–10 (log scale) maps to ~0.1–1.0 actual severity.
* Duration / Delay now specified in seconds (`duration_s`, `delay_s`).
* Iterations = cycle count (0 = infinite). Residual attenuation decays after completion.
* Heavy rain (severity ≥ ~0.65) auto‑asserts `lower_signal_than_predicted` alarm; it auto‑clears below ~0.35 unless manually forced.

### Snow Accumulation
Adds obstruction and moderate throughput / latency impact. Durations & delays also in seconds. For heavier accumulation the `lower_signal_than_predicted` alarm may assert.

### Obstruction Map (Manual Override)
8x8 grid (bottom‑left origin). Click cells to carve holes (black). External axis labels: X across the top (0‑7), Y along the left (0 at bottom ascending upward). "Randomize" seeds a sparse pattern. Clearing manual override occurs via Weather section (Clear Manual Override button).

### Weather Grid / Storm Path
Shows synthesized combined grid (rain + snow) when no manual obstruction override. Axis labels X top / Y left (top‑left origin). Configure a rain path (start/end coordinates) and add extra rain cells. Coordinates for manual extra cells use bottom‑left origin in the UI and are converted internally.

### Refresh Controls
Header provides:
* Refresh Now button
* Auto toggle (enabled by default)
* Interval seconds input (default 60s)
The scheduler re‑arms after each successful refresh. Set to a small value for near real‑time updates.

## API Endpoints (JSON)
| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/alarms` | GET/POST | Get or toggle alarms (`name`, `value`) |
| `/api/fields` | POST | Set/clear numeric (`value`) or raw (`raw`) field override |
| `/api/error` | POST | Next request error injection |
| `/api/obstruction` | GET/POST | Get current snapshot or modify obstruction grid (x,y or `randomize`) |
| `/api/rainfade` | GET/POST | Start/stop rain fade (accepts `duration_s`,`delay_s` or legacy `_ms`) |
| `/api/snow` | GET/POST | Start/stop snow accumulation (same second/legacy fields) |
| `/api/weather` | GET/POST | Get weather snapshot or set rain path / extra cells / clear manual obstruction |
| `/` | GET | Serves the SPA HTML |

## Request Bodies (Examples)
Start rain fade:
```json
{"action":"start","intensity":0.5,"duration_s":30,"iterations":2,"delay_s":5}
```
Stop rain fade:
```json
{"action":"stop"}
```
Add extra rain cells:
```json
{"extra_rain_cells":[{"X":2,"Y":3},{"X":6,"Y":5}]}
```
Set rain path:
```json
{"rain_path":{"start_x":0,"start_y":0,"end_x":7,"end_y":7}}
```
Manual obstruction hole (bottom‑origin Y):
```json
{"x":3,"y":1}
```

## Snapshot Fields (subset)
`/api/alarms` (and snapshot portions returned by other endpoints) includes:
* `alarms` – active alarm overrides
* `fields` / `raw_fields` – applied overrides
* `obstruction` – manual obstruction grid (if active)
* `weather_grid` – synthesized dynamic grid (when manual override absent)
* `rain` – state (`active`, `intensity`, `duration_ms`, `delay_ms`, path, extra_cells)
* `snow` – state (same pattern)
* `last_dish` – lightweight subset of last synthesized dish metrics for UI impact table

Note: Snapshot still exposes `duration_ms` / `delay_ms` for backward compatibility; control endpoints accept both seconds and legacy millisecond names.

## Coordinate Systems
* Obstruction map UI: bottom‑left origin for user input.
* Weather grid: top‑left origin internally and visually.
* Extra rain cells: UI entry uses bottom‑left, converted to top‑left internally.

## Alarm Auto‑Behavior
* `lower_signal_than_predicted` auto‑enabled when rain severity ≥0.65 or heavy snow (sev>0.7) and auto‑cleared below thresholds if not manually forced.

## Version Info
CLI embeds description, author, homepage and version (`AppVersion` in `cmd/naquadah/constants.go`). `--help` displays common usage examples plus the version footer.

## Roadmap Additions (related to Admin UI)
Planned ideas:
* Scenario scripting panel
* Time travel / playback controls for recorded sequences
* Export current override profile to JSON
* Theming toggle (light/dark)

---
Feel free to open issues/PRs to extend the admin capabilities.
