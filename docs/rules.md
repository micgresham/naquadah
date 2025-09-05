# Rules Engine

The rules engine modifies timing, status, and selected response fields without changing code. Rule matches increment `naquadah_rule_hits_total` when the metrics server is enabled with `-metrics`.

## Rule Object Schema

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | yes | Identifier for logging |
| match.request_types | []string | no | Internal request key list (empty = all) |
| match.probability | float 0..1 | no | Chance rule fires (default 1) |
| actions | []action | yes | Ordered list of actions |
| stop | bool | no | Stop evaluation after this rule if matched |

## Request Keys
See `requestKey` in `internal/rules/engine.go`.
```
get_status
wifi_get_clients
dish_get_context
get_ping
ping_host
speed_test
dish_get_config
wifi_get_config
other
```

## Actions

| Type | Phase | Fields | Description |
|------|-------|--------|-------------|
| delay | pre | ms | Sleep fixed duration |
| jitter | pre | ms, jitter_ms | Sleep ms +/- jitter_ms |
| log | pre/post | message | Log message with rule name |
| status | post | code, message | Override embedded status fields |
| field_override | post | field, value | Override whitelisted numeric field |
| error | post | error_code, message | Inject gRPC error (terminates) |
| drop | post | (none) | Suppress response (client may time out) |

### Field Override Whitelist

- dish.downlink_bps
- dish.uplink_bps
- dish.pop_ping_latency_ms
- wifi.ping_latency_ms
- wifi.downlink_bps
- wifi.ping_drop_rate

### Example
```yaml
- name: transient_unavailable
  match:
    request_types: ["get_status"]
    probability: 0.15
  actions:
    - type: delay
      ms: 150
    - type: error
      error_code: unavailable
      message: "TEMP_UNAVAIL"
  stop: true

- name: adjust_throughput
  match:
    request_types: ["get_status"]
  actions:
    - type: field_override
      field: dish.downlink_bps
      value: 90000000
```

### Probability
If probability < 1 a uniform random draw decides. Random seed controlled by `-seed` flag.

## Metrics
Enable metrics with e.g. `-metrics :9090` and scrape `/metrics` for:
- naquadah_requests_total{key}
- naquadah_rule_hits_total{rule}
- naquadah_request_latency_seconds_bucket{key}

Use labels to correlate injected delays vs. observed latency.

## Adding New Actions
1. Extend `Action` struct in `internal/rules/engine.go`.
2. Implement handling in `ApplyPre` or `ApplyPost`.
3. Add tests in `internal/rules/engine_test.go`.
4. Update this document & README.

## License
MIT – see root `LICENSE`.
