# Architecture

```
Client --> gRPC Server --> Core
                      |        
                      +--> Data Provider (random / playback / baseline)
                      +--> Config Profile (YAML)
                      +--> Rules Engine (pre/post)
                      +--> Recorder (sim) / Real Poller (optional, separate real-only file)
                      +--> Metrics Exporter (Prometheus)
```

## Core Responsibilities
- Accept gRPC requests (Device, Mesh, Unlock)
- Map oneof request -> response builder
- Generate random or provider-based metrics
- Apply rules (latency/errors/overrides)
- Maintain lightweight state

## Rules Flow
1. Pre: delay / jitter / (log)
2. Core handler builds response (random or sample-based)
3. Post: status / field_override / error / drop / log

## Providers
Encapsulate metric sourcing. If provider returns nil sample, random synthesis proceeds.

## Recorder / Real Poller
Recorder: synthetic (simulated) snapshots written when `-record-json` set.
Real Poller: polls actual dish endpoints when `-real-target` set.

Capture modes:
* Mixed (legacy): specify `-real-target` and `-record-json` only – real samples appended into the simulator file.
* Real-only: add `-real-record-json real_only.json` – real samples written exclusively there; simulator recorder (if any) is skipped to avoid contamination.

Both writers use atomic temp file rename.

## Thread Safety
- Provider swap under RWMutex
- Recorder writes via temp file rename
- Randomness: global seed plus provider-local `rand.Rand`

## Extending
- New gRPC method: implement handler, optionally map request key for rules
- New rule action: edit engine, tests, docs
- New sample fields: modify `Sample`, recorder, providers

## Error Injection
Rules `error` action returns a gRPC status. `drop` yields nil response (client may time out).

## Performance & Metrics
All in-memory; minimal allocations. Export counters & histograms with `-metrics :9090` and scrape `/metrics`.

## License
MIT License – see root `LICENSE` file.
