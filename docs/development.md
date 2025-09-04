# Development Guide

## Prerequisites
- Go (version per go.mod)
- Protobuf toolchain (only if regenerating .pb.go files)

## Common Tasks

Run:
```
go run ./cmd/naquadah -noisy
```

Test:
```
go test ./...
```

Cross build:
```
./scripts/build.sh
```

## Adding a Rule Action
1. Extend `Action` struct in `internal/rules/engine.go`.
2. Implement logic in `ApplyPre` or `ApplyPost`.
3. Update tests (`engine_test.go`).
4. Document (README + docs/rules.md).

## Adding a Data Provider
1. Implement `DataProvider`.
2. Call `core.SetDataProvider(newProvider)` during startup.
3. (Optional) Add flags to select it.

## Adding Sample Fields
1. Edit `Sample` struct in `internal/sim/provider.go`.
2. Update recorder capture logic.
3. Adjust baseline & playback providers if needed.
4. Add docs & tests.

## gRPC Endpoint Extension
1. Add/update proto under `api-protoc` (ensure imports ok).
2. Regenerate (protoc) if needed.
3. Implement switch case in `Core.HandleDeviceRequest`.
4. Map request key (rules) if action targeting desired.

## Testing Strategy
- Unit: rules, provider selection, error/drop behaviors
- Golden tests: playback scaling, baseline jitter
- Coverage guard: static list of request oneof types (`coverage_test.go`)
- Potential future: latency bounds & rule action duration histograms

## Code Style
- Small focused handlers
- Avoid global mutable state besides deterministic random
- Document new exported types

## Commit Guidelines
- Prefix: feat:, fix:, docs:, refactor:, test:, chore:
- Keep README current when adding user-facing behavior

## Future Enhancements (Dev Quality)
- Add lint workflow (golangci-lint)
- Add GitHub Actions CI matrix build
- Introduce benchmark tests for request throughput
- Protoreflect-driven dynamic endpoint coverage test
