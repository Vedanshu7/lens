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
git checkout -b feat/Lens_44_target_provider_layer
git checkout -b fix/Lens_12_memberlist_rejoin
git checkout -b docs/Lens_31_all_nats_stack
```

**Branch naming:**

Always include the project prefix and linked issue number:

| Type | Pattern | Example |
|---|---|---|
| Feature | `feat/Lens_<issue>_<description>` | `feat/Lens_44_target_provider_layer` |
| Bug fix | `fix/Lens_<issue>_<description>` | `fix/Lens_12_nats_reconnect` |
| Docs | `docs/Lens_<issue>_<topic>` | `docs/Lens_31_kubernetes_deploy` |
| Refactor | `refactor/Lens_<issue>_<area>` | `refactor/Lens_18_discovery_interface` |
| Test | `test/Lens_<issue>_<area>` | `test/Lens_27_persistence_backend` |

### 4. Run all checks locally

```bash
# Install the build tool if you haven't already
go install ./cmd/lens-build

# Build the binary from your lens.yaml
lens-build

# Run tests and vet
go test -race ./...
go vet ./...
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

Providers are the most common contribution. Each layer has a clean interface — implement it and register in `init()`. No build tags, no stub files.

### File structure

```
internal/<layer>/<providername>/
└── <providername>.go     # interface implementation + init() registration
```

```go
// The entire file — no build tag needed
package myprovider

import "github.com/Vedanshu7/lens/internal/<layer>"

func init() {
    <layer>.Register("my-provider", func(cfg map[string]any) (<layer>.Interface, error) {
        return newMyProvider(cfg)
    })
}
```

Then add one line to `cmd/lens-build/main.go` in the appropriate layer map:

```go
"my-provider": "github.com/Vedanshu7/lens/internal/<layer>/myprovider",
```

That's it. `lens-build` will include it automatically when `lens.yaml` requests it.

### Layer interfaces

| Layer | Interface | Register call |
|---|---|---|
| Transport | `transport.Transport` | `transport.Register("name", factory)` |
| Discovery | `discovery.Resolver` | `discovery.Register("name", factory)` |
| Persistence | `persistence.Backend` | `persistence.Register("name", factory)` |
| Observability | `observability.Observer` | `observability.Register("name", factory)` |
| Target | `target.TargetClient` | `target.Register("name", factory)` |

### Provider checklist

- [ ] `init()` calls the appropriate `Register()` function — no build tag
- [ ] One entry added to the layer map in `cmd/lens-build/main.go`
- [ ] At least one test
- [ ] README provider table updated with name and "best for" description
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
| `Build & Vet (1.25)` | `lens-build` + `go vet ./...` |
| `Test` | `go test -race ./...` |
| `Lint` | `golangci-lint run` |
| `go mod tidy` | Fails if `go.mod` / `go.sum` are not clean |
| `sidecar` | Multi-arch Docker build of the sidecar binary via `lens-build` |
| `dashboard` | Multi-arch Docker build of the React dashboard |

---

## Review Process

1. CI must be fully green.
2. At least one maintainer approval is required.
3. Stale reviews are dismissed when new commits are pushed.
4. All review conversations must be resolved before merge.

---

Questions? Open a [Discussion](https://github.com/Vedanshu7/lens/discussions) or comment in the issue thread.
