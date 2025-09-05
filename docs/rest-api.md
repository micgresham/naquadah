# REST API (v1)

The external REST surface is exposed under `/api/v1/*` and mirrors the legacy internal admin endpoints (`/api/*`). New development should target the versioned paths. All responses are JSON with `Cache-Control: no-store` where appropriate.

You can expose APIs in three modes:
* `-admin :PORT` (default includes embedded UI)
* `-admin :PORT -admin-no-ui` (API only on that port)
* `-rest :PORT` (separate REST-only listener; can be combined with `-admin`)

## Authentication

Optional OAuth2-style Bearer (JWT) protection via flags:

```
-auth \
  -auth-issuer https://issuer.example/ \
  -auth-audience naquadah-admin \
  -auth-hs256-secret devsecret   # OR use JWKS
```

Or JWKS validation:

```
-auth -auth-issuer https://issuer.example/ \
  -auth-audience naquadah-admin \
  -auth-jwks https://issuer.example/.well-known/jwks.json \
  -auth-jwks-refresh 10m
```

If `-auth` is omitted the endpoints are open (development mode). Health endpoints (`/api/v1/health`, `/api/health`) are always public to allow liveness probes.

### Token Requirements
* `iss` must match `-auth-issuer` if provided
* `aud` must contain `-auth-audience` if provided
* Exp (`exp`) must be in the future
* Supported algs: HS256 (shared secret), RS256/ES256 via JWKS (parsing of asymmetric keys is stubbed for now; extend `internal/auth` accordingly)

## Versioning Strategy
`/api/v1` is a stability promise for field names; additive changes only. Breaking changes will appear under `/api/v2`.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/health | Liveness/keep-alive `{ok,ts}` |
| GET | /api/v1/alarms | Snapshot (includes alarms etc.) |
| POST | /api/v1/alarms | Toggle alarm `{name,value}` or `{name:"__clear_all__"}` |
| POST | /api/v1/fields | Set numeric `{name,value}` or raw `{name,raw}`; omit value to clear |
| POST | /api/v1/error | Inject one-shot error `{enable,code,msg}` |
| GET | /api/v1/obstruction | Snapshot (manual/effective/weather grids) |
| POST | /api/v1/obstruction | Modify grid `{x,y}`, `{x,y,value}` or `{randomize:true}` |
| GET | /api/v1/rainfade | Rain state snapshot |
| POST | /api/v1/rainfade | Start/stop rain fade `{action:start|stop, intensity, duration_s, iterations, delay_s}` |
| GET | /api/v1/snow | Snow state snapshot |
| POST | /api/v1/snow | Start/stop snow accumulation analogous to rain |
| GET | /api/v1/weather | Weather snapshot (effective + path + extra cells) |
| POST | /api/v1/weather | Update path `{rain_path:{start_x,...}}`, set extra cells `{extra_rain_cells:[...]}`, or `{clear_manual:true}` |

## Snapshot Object (Fields of Interest)
```
{
  "alarms": {"thermal_throttle":true},
  "fields": {"dish.downlink_throughput_bps":12345},
  "raw_fields": {"dish.software_update_state":"FETCHING"},
  "obstruction": [1,1,0,...],
  "weather_grid": [1,0,1,...],
  "effective_grid": [1,0,0,...],
  "dish_alerts": {"lower_signal_than_predicted":true},
  "last_dish": {"downlink_bps":..., "obstruction_fraction":...},
  "rain": {"active":true,"intensity":0.42,"duration_ms":30000,"iter":0,...},
  "snow": {...}
}
```

## Error Codes
Standard HTTP status codes for REST layer (400 parse/validation, 401 auth failure, 405 method). gRPC error injection returns 200 from the REST call (it configures future gRPC responses).

## Examples

Start rain fade:
```bash
curl -H "Authorization: Bearer $TOKEN" -X POST :8081/api/v1/rainfade \
  -d '{"action":"start","intensity":0.6,"duration_s":45,"iterations":2,"delay_s":5}'
```

Clear all alarms:
```bash
curl -H "Authorization: Bearer $TOKEN" -X POST :8081/api/v1/alarms -d '{"name":"__clear_all__"}'
```

Toggle manual obstruction cell:
```bash
curl -H "Authorization: Bearer $TOKEN" -X POST :8081/api/v1/obstruction -d '{"x":3,"y":2,"value":0}'
```

## Extending
Add a new endpoint by wiring a handler in `internal/admin/admin.go` under both `/api/*` and `/api/v1/*` (legacy + versioned). Update this doc and bump minor version if additive.

## Security Notes
The current JWKS handling fetches and caches keys but does not yet parse RSA/EC material; only HS256 is practically enforced right now. For production use extend `internal/auth` to parse `x5c` or modulus/exponent and construct a `crypto.PublicKey`.

## License
MIT – see root `LICENSE`.