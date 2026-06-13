# Contributing to Lens

Thank you for taking the time to contribute. This document covers the development setup, code standards, and submission process.

---

## Development setup

```bash
git clone https://github.com/vedanshu/lens.git
cd lens
go mod download
```

No external tools are required for the core sidecar. To work on the dashboard:

```bash
cd dashboard
npm install
npm run dev
```

---

## Running tests

```bash
go test ./...
```

To build without Redis or NATS (uses in-memory providers):

```bash
LENS_PERSISTENCE=memory LENS_DISCOVERY=static go run .
```

---

## Code style

### Go

All Go source files must follow these rules. They are enforced during review.

**Formatting.** Run `gofmt` and `goimports` before every commit. The CI build will fail on unformatted files.

**Package doc comment.** Every package must have a doc comment immediately above the `package` declaration, describing the package's purpose in one or two sentences.

**Exported symbol doc comments.** Every exported function, type, constant, variable, and interface method must have a doc comment that starts with the symbol name (Go godoc convention). The comment must describe what the symbol is or does, including any non-obvious parameters or return values.

**Comments end with a period.** Every doc comment sentence ends with a period. Sentence fragments are not permitted.

**No inline comments.** Comments must appear on their own line above the relevant code. Trailing end-of-line comments (`x = 1 // set x`) are not permitted.

**Single point of return.** Each function has at most one `return` statement at the bottom, except for immediate guard clauses at the very top of a function that return before any meaningful work begins.

**DRY.** Shared logic belongs in a shared package. Before adding a helper function, check whether an equivalent already exists in the codebase.

**No noise comments.** Do not write comments that restate what the code already says. Describe invariants, constraints, side-effects, or non-obvious behaviour instead.

**Canonical example:**

```go
// Package store defines the key naming conventions used across all persistence backends.
package store

// CheckpointKey returns the key that stores the last-seen timestamp for an instance.
// Used by replayMissed to determine which log entries arrived while the instance was offline.
func CheckpointKey(service, instance string) string {
    return fmt.Sprintf("%s:checkpoint:%s:%s", KeyPrefix, service, instance)
}
```

### TypeScript (dashboard)

Follow the existing patterns in `dashboard/src`. Use named exports, keep components small, and avoid inline styles.

---

## Adding a new provider

Lens uses a registry pattern for all pluggable subsystems (transport, persistence, discovery, observability).

1. Create the package under the appropriate directory (e.g. `internal/transport/kafka/kafka.go`).
2. Register the provider in `init()`:
   ```go
   func init() {
       transport.Register("kafka", func(host transport.TransportHost, cfg map[string]any) (transport.Transport, error) {
           // ...
       })
   }
   ```
3. Add a blank import in `main.go`.
4. Document the provider in `config/lens.example.yaml`.
5. Add the provider to the relevant table in `README.md`.

For optional providers that add heavy dependencies, use a build tag (`//go:build lens_kafka`) and document the tag in the README.

---

## Submitting a pull request

**Branch naming:** `feat/<name>`, `fix/<name>`, or `docs/<name>`.

**One logical change per PR.** Refactoring and feature work belong in separate PRs.

**PR description.** Explain the *why* — what problem this change solves and why this approach was chosen. The diff explains the *what*.

**Before opening the PR:**

```bash
go build ./...
go vet ./...
gofmt -l .          # must print nothing
go build ./...      # must succeed with no errors
```

---

## Reporting bugs

Open an issue on GitHub with:

- The Lens version or commit hash.
- The provider combination in use (transport, persistence, discovery).
- A minimal reproduction case.
- The full log output with `LENS_LOG_LEVEL=debug`.
