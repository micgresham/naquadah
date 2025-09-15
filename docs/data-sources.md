# Data Sources

Simulation metrics come from a pluggable `DataProvider`.

```go
type DataProvider interface {
    Next(now time.Time) *Sample
}
```

## Modes

| Mode | Activation | Variation |
|------|------------|-----------|
| Random | default | High |
| Playback | -playback-json file.json | None (timeline replay; scale with -playback-scale) |
| Baseline Hybrid | -baseline-json file.json | Light jitter (+/-5%) |
| Recording (synthetic) | -record-json file.json | Captures simulator random baseline snapshots |
| Real Polling (mixed) | -real-target host:port [-record-json file.json] | Appends real samples into sim file (legacy) |
| Real Polling (real-only) | -real-target host:port -real-record-json real.json | Real samples only, never mixed |

## Playback
Samples are re-based to simulator start; playback advances with wall time. If `-playback-loop` is true, index resets at end.

## Baseline Hybrid
Cycles through captured samples applying small jitter to selected numeric fields (throughput, latency). Adjust logic in `baselineProvider` for additional variation.

## Recording
`Recorder` (started with `-record-json`) captures simulator-generated snapshots every interval (default 60s) and appends them to a JSON file.

### Real Dish Polling
Enable with `-real-target host:port` (optionally `-real-token` and `-real-timeout`). Poller issues a subset of real requests (dish & wifi status, clients, speedtest, ping) at the same interval.

Capture selection:
* Mixed mode: omit `-real-record-json`; if `-record-json` set, real data appended there, else defaults to `real_capture.json`.
* Real-only mode: set `-real-record-json`; simulator recorder is suppressed so file contains only authentic samples.

Fields not returned by the real device remain omitted.

## JSON Schema
```json
[
  {
    "ts": "2025-09-04T12:00:00Z",
  "dish_status": { /* DishGetStatusResponse */ },
  "wifi_status": { /* WifiGetStatusResponse */ },
  "wifi_clients": { /* WifiGetClientsResponse */ },
  "speedtest": { /* SpeedTestResponse */ },
  "ping_all": { /* GetPingResponse */ },
  "transceiver_status": { /* TransceiverGetStatusResponse (placeholder) */ }
  }
]
```

## Extending Samples
Add more fields to `Sample` then update:
- Recorder capture logic in `provider.go`
- Baseline provider jitter adjustments
- Documentation & tests

If you expose metrics (`-metrics :9090`) you can monitor request volume and latency while experimenting with providers.

## Custom Provider Example
```go
type patternProvider struct { seq []*Sample; i int }
func (p *patternProvider) Next(now time.Time) *Sample {
    if len(p.seq)==0 { return nil }
    s := p.seq[p.i]
    p.i = (p.i+1) % len(p.seq)
    return s
}
```
Set provider:
```go
core.SetDataProvider(&patternProvider{seq: samples})
```

## License
MIT – see root `LICENSE`.
