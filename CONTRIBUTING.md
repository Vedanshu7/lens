# Contributing to Lens

Thank you for contributing. Please read this fully before opening a PR.

## Hard Requirements

| Requirement | Detail |
|---|---|
| **Fork the repo** | Do not push branches directly to `Vedanshu7/lens` |
| **Branch naming** | `feat/`, `fix/`, `docs/`, `refactor/`, `test/`, or `chore/` prefix |
| **At least 1 test** | Every code change needs a test — no exceptions |
| **Isolated scope** | One PR = one problem. No bundling unrelated changes |
| **All CI must pass** | `build`, `test`, `lint`, `tidy`, `sidecar`, `dashboard` — all green |

---

## Workflow

### 1. Open an issue first

Discuss the approach before writing code. PRs without a linked issue may be closed.

**Issue title format:**

```
[Type]: Short description
```

| Type | When to use | Example |
|---|---|---|
| `[Feat]` | New provider or feature | `[Feat]: Add Consul discovery provider` |
| `[Fix]` | Bug or incorrect behaviour | `[Fix]: memberlist fails to rejoin after restart` |
| `[Test]` | Test coverage | `[Test]: Add layer independence matrix tests` |
| `[Docs]` | Documentation only | `[Docs]: Document all-NATS stack configuration` |
| `[Chore]` | Maintenance, deps, CI | `[Chore]: Bump golangci-lint to v2.x` |

### 2. Fork and clone

```bash
git clone https://github.com/<your-username>/lens.git
cd lens
```

### 3. Create a branch — never work on `main`

```bash
git checkout -b feat/consul-discovery
git checkout -b fix/memberlist-rejoin
git checkout -b docs/all-nats-stack
```

**Branch naming:**

| Type | Pattern | Example |
|---|---|---|
| Feature | `feat/<description>` | `feat/consul-discovery` |
| Bug fix | `fix/<description>` | `fix/nats-reconnect` |
| Docs | `docs/<topic>` | `docs/kubernetes-deploy` |
| Refactor | `refactor/<area>` | `refactor/discovery-interface` |
| Test | `test/<area>` | `test/persistence-backend` |

### 4. Run all checks locally

```bash
go build -tags "lens_grpc lens_memberlist" ./...
go test  -tags "lens_grpc lens_memberlist" -race ./...
go vet   ./...
go mod tidy
```

### 5. Open a pull request

Push to your fork and open a PR against `Vedanshu7/lens:main`. Fill in the PR template fully, including CI run links.

---

## Testing Requirements

- Every PR must include at least one test. No exceptions.
- Tests live alongside the package they test (`internal/discovery/nats/nats_test.go`).
- Use table-driven tests where possible.
- Do not make real network calls in tests — use mock servers or in-process listeners.
- Run with the race detector: `go test -race ./...`

---

## Adding a New Provider

Providers are the most common contribution. Each layer has a clean interface — implement it and register in `init()`.

### File structure

```
internal/<layer>/<providername>/
└── <providername>.go     # interface implementation + init() registration
```

```go
// providers_<name>.go at repo root
//go:build lens_<name>

package main

import _ "github.com/vedanshu/lens/internal/<layer>/<providername>"
```

### Layer interfaces

| Layer | Interface | Register call |
|---|---|---|
| Transport | `transport.Transport` | `transport.Register("name", factory)` |
| Discovery | `discovery.Resolver` | `discovery.Register("name", factory)` |
| Persistence | `persistence.Backend` | `persistence.Register("name", factory)` |
| Observability | `observability.Observer` | `observability.Register("name", factory)` |

### Provider checklist

- [ ] Build tag: `//go:build lens_<name>` at top of implementation file
- [ ] `init()` calls the appropriate `Register()` function
- [ ] `providers_<name>.go` created at repo root with matching build tag
- [ ] At least one test
- [ ] README provider table updated with name, build tag, and "best for" description
- [ ] `lens.yaml` config options documented if the provider accepts config

---

## Code Style

- Run `gofmt` before committing.
- Follow standard Go idioms — [Effective Go](https://go.dev/doc/effective_go) is the reference.
- No comments that explain what the code does. Only comments explaining why — hidden constraints, workarounds, non-obvious invariants.
- Keep interfaces stable. Think carefully before adding methods to any layer interface.

---

## Commit Message Format

[Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short summary>
```

**Types:** `feat` `fix` `docs` `refactor` `test` `chore`

```
feat(discovery): add Consul provider
fix(nats): handle reconnect during broadcast
docs: document all-NATS stack configuration
test(persistence): add Redis backend integration test
```

---

## CI Checks

All checks must be green before a review will be assigned:

| Check | What it runs |
|---|---|
| `Build & Vet (1.25)` | `go build` + `go vet` with both gRPC and NATS build tag sets |
| `Test` | `go test -race ./...` |
| `Lint` | `golangci-lint run` |
| `go mod tidy` | Fails if `go.mod` / `go.sum` are not clean |
| `sidecar` | Multi-arch Docker build of the sidecar binary |
| `dashboard` | Multi-arch Docker build of the React dashboard |

---

## Review Process

1. CI must be fully green.
2. At least one maintainer approval is required.
3. Stale reviews are dismissed when new commits are pushed.
4. All review conversations must be resolved before merge.

---

Questions? Open a [Discussion](https://github.com/Vedanshu7/lens/discussions) or comment in the issue thread.
