## Description

<!-- What does this PR do? Why is this change needed? -->

Fixes #<!-- issue number -->

## Type of Change

- [ ] New provider (transport / discovery / persistence / observability)
- [ ] Bug fix
- [ ] New feature
- [ ] Refactor
- [ ] Documentation
- [ ] Test / chore

## Changes

<!-- Bullet list of what changed and why. -->

## Testing

**CI runs:**
- Branch creation CI run: <!-- paste link -->
- Last commit CI run: <!-- paste link -->

**Checklist:**
- [ ] `go build -tags "lens_grpc lens_memberlist" ./...` passes
- [ ] `go test -tags "lens_grpc lens_memberlist" ./...` passes
- [ ] `go vet ./...` passes
- [ ] `go mod tidy` — no changes to `go.mod` / `go.sum` unless intentional

## Scope

- [ ] This PR addresses **one specific problem** (no unrelated changes bundled in)

## For New Providers

- [ ] Implements the correct interface (`Transport`, `Resolver`, `Backend`, or `Observer`)
- [ ] Registered via `init()` with `transport.Register` / `discovery.Register` / etc.
- [ ] `providers_<name>.go` file added at repo root with the matching build tag
- [ ] Build tag documented in README provider table
- [ ] Config options documented in the `lens.yaml` example or README

## Screenshots / Proof

<!-- For bug fixes: before/after. For new features: example output or test result. Delete if not applicable. -->
