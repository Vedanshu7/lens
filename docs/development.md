# Lens Development Guide

This guide covers the architectural rules, provider addition workflow, test conventions, and pre-commit checklist for working in this repo. Follow it when writing code or reviewing PRs.

---

## Layer architecture

Lens has five independently swappable provider layers. Each layer has an interface package and any number of concrete provider subdirectories.

```
internal/
  agent/            orchestrator — owns all providers, handles HTTP routes
  persistence/      Backend interface  +  redis/, memory/, natskv/
  discovery/        Resolver interface +  memberlist/, nats/, dnssrv/, static/
  transport/        Transport interface + grpc/, kafka/, nats/, zeromq/, redisstreams/
  target/           TargetClient interface + http/, unix/, grpc/
  observability/    Observer interface  +  sql/, prometheus/, otel/, webhook/, stdout/, noop/
  store/            key naming constants (shared utility, not a layer)
  proto/            generated protobuf (do not edit by hand)
```

### Import rules — enforced by `test/unit/imports/layer_isolation_test.go`

| Rule | Rationale |
|------|-----------|
| No provider package may import `internal/agent` | Would create an import cycle. Use the `TransportHost` callback pattern instead. |
| No provider in layer A may import a concrete provider from layer B | Layers are independently compilable. |
| `internal/discovery` (and its providers) may import `internal/persistence` | Allowlisted: discovery providers use the persistence backend for bootstrap seed storage. Providers that don't need it accept and ignore the parameter. |
| `internal/discovery/memberlist` may import `internal/store` | Allowlisted: memberlist uses shared key constants when writing to persistence. |

If you want to add a new cross-layer import, add it to the allowlist in `test/unit/imports/layer_isolation_test.go` and document the reason.

### The TransportHost callback pattern

`internal/transport/transport.go` defines `TransportHost` — a slim interface that transport providers call back into the agent. This breaks the transport ↔ agent import cycle:

```
transport provider  -->  TransportHost interface  <--  agent (implements)
```

When a transport receives a broadcast message from a peer, it calls:
- `host.WriteInvalidationLog(ctx, svc, payload)` — writes to the replay log
- `host.ApplyInvalidation(ctx, payload, origin)` — delivers to the local target service

When a transport handles a fetch request from a peer, it calls:
- `host.GetFromTarget(ctx, payload)` — proxies to the local target service

---

## Adding a new provider

Follow these steps exactly. CI will reject PRs that skip any step.

### 1. Pick the right layer

| I want to add... | Layer |
|-----------------|-------|
| A new message bus (Kafka, AMQP, etc.) | `transport` |
| A new service registry (Consul, etcd, etc.) | `discovery` |
| A new key-value store (MongoDB, DynamoDB, etc.) | `persistence` |
| A new metrics/log sink (Datadog, InfluxDB, etc.) | `observability` |
| A new way to talk to the target app (gRPC, Unix socket) | `target` |

### 2. Create the provider package

```
internal/<layer>/<provider-name>/
  <provider-name>.go   # implements the layer interface
```

### 3. Implement the interface

```go
package myprovider

import (
    "github.com/Vedanshu7/lens/internal/<layer>"
)

func init() {
    <layer>.Register("my-provider", func(cfg map[string]any) (<layer>.Interface, error) {
        // discovery factories also receive a persistence.Backend as first param
        return &myProvider{}, nil
    })
}

type myProvider struct { ... }

// Implement all interface methods here.
// Compile-time check:
var _ <layer>.Interface = (*myProvider)(nil)
```

### 4. Register in `cmd/lens-build/main.go`

Add a blank import to the layer map so `lens-build` can compile the provider in:

```go
var <layer>Providers = map[string]string{
    "my-provider": "github.com/Vedanshu7/lens/internal/<layer>/my-provider",
    // ...
}
```

### 5. Add a stub to `test/testutil/stubs.go`

Only required if your interface isn't already stubbed. Add a `Stub<Name>` struct with function fields for each method.

### 6. Write tests

- **Unit test** in `test/unit/<layer>/`: exercise the provider in isolation using an in-process stub or real implementation.
- **Integration test** in `test/integration/` (optional): wire the provider against a real external service, gated by `//go:build integration`.
- **Matrix entry** in `test/unit/matrix/matrix_test.go`: add your provider as a row if it has no external dependencies.

---

## Test conventions

| Convention | Detail |
|-----------|--------|
| Test file location | `test/unit/<layer>/` for unit tests; `test/integration/` for E2E |
| Package name | `<layer>_test` (black-box) or `<layer>` (white-box) |
| Test naming | `Test<Interface>_<Method>_<Scenario>` |
| Helper marking | `t.Helper()` at the top of every helper function |
| Cleanup | `t.Cleanup(func() { ... })` — never defer in test body |
| Fatal vs error | `t.Fatalf` when the test cannot continue; `t.Errorf` to collect multiple failures |
| Race detector | All tests must pass `go test -race ./...` |
| External services | Gate with `//go:build integration`; never required for unit tests |
| Stubs | Use `test/testutil/stubs.go` — do not write ad-hoc fakes in test files |

---

## Commit and branch conventions

Branch names follow `<type>/Lens_<issue>_<short-description>`:

```
feat/Lens_44_target_provider_layer
fix/Lens_53_bindaddr_ignored
docs/Lens_49_development_guide
test/Lens_16_matrix_tests
```

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(transport): add WebSocket transport provider
fix(agent): apply BindAddr to http.Server.Addr
docs: add architecture and development guides
test(matrix): add provider combination matrix tests
chore(ci): pin Actions to commit SHAs
```

Scope is the package or layer being changed. Keep the subject line under 72 characters.

---

## Pre-commit checklist

Run all of these locally before pushing. CI will run the same checks and fail the PR if any fail.

```bash
# 1. Format
gofmt -l .          # should print nothing

# 2. Vet
go vet ./...

# 3. Lint
golangci-lint run ./...

# 4. Tests with race detector
go test -race -count=1 ./test/...

# 5. Layer boundary check
go test ./test/unit/imports/...

# 6. Module hygiene
go mod tidy
git diff --exit-code go.mod go.sum
```

---

## Interface design rules

When extending an interface, follow these rules to keep providers independently implementable:

1. **Every method must be completable without calling other layer packages** — a `persistence.Backend` method must not require a `discovery.Resolver`.
2. **Use `map[string]string` for extensible metadata** — `ServiceInstance.Meta` follows this pattern for arbitrary labels.
3. **TTLs belong in the interface, not in the config** — pass TTL as a `time.Duration` parameter.
4. **Batch operations belong in `Pipeliner`, not in the main interface** — see `persistence.Pipeliner`.
5. **Blocking is forbidden in `Observer.Record`** — use a buffered channel internally and drop events when full.
