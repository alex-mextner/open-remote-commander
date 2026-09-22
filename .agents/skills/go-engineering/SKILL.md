---
name: go-engineering
description: Repository-local Go architecture, testing, style and performance rules for Open Remote Commander.
---

# Go engineering rules

1. Use `gofmt`; keep packages small and cohesive. Composition belongs in `cmd/*`; reusable implementation belongs in `internal/*`; stable external JSON contracts belong in `pkg/api/v1`.
2. Introduce interfaces only at substitution/testing boundaries. Prefer concrete return types and dependency injection through constructors.
3. Pass `context.Context` as the first parameter for blocking/network operations. Propagate cancellation. Never store request contexts in structs.
4. Errors must carry operation context with `%w`; compare sentinel errors with `errors.Is`.
5. Never start an unbounded goroutine per untrusted item. Bound channels, payload sizes, process count and concurrent remote calls.
6. Hot-path optimization requires a benchmark/profile. Prefer fewer allocations and streaming/bounded I/O over clever micro-optimizations.
7. Table-driven tests for boundary cases; race detector for concurrency; fuzz parsers/path containment; integration tests for HTTP/WebSocket flows.
8. Required checks before merge: `go test ./...`, `go test -race ./...`, `go vet ./...`, `govulncheck ./...`, lint, build.
