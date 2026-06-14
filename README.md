# Lens

A Go sidecar framework with five independently swappable layers: **transport**, **discovery**, **persistence**, **observability**, and **target**.

Pick any provider for each layer. Combine them freely. Switch at config time with no code changes.

---

## Providers

### Transport — how pods broadcast to each other

| Provider | Best for |
|---|---|
| `grpc` | Direct pod-to-pod, lowest latency, no broker required |
| `nats` | Broker fan-out, pods behind NAT or in separate subnets |
| `kafka` | High-throughput fan-out, Kafka already in stack |
| `zeromq` | Brokerless pub/sub, minimal footprint |
| `redis-streams` | Reuses an existing Redis instance, zero extra infra |

### Discovery — how pods find each other

| Provider | Best for |
|---|---|
| `memberlist` | Gossip over UDP, zero infrastructure |
| `nats` | Uses the same broker already running for transport |
| `dnssrv` | Kubernetes headless services, Consul DNS |
| `static` | Fixed known peer list, no infrastructure |

### Persistence — replay log, audit trail, shared metadata

| Provider | Best for |
|---|---|
| `redis` | Production default, durable, widely available. Always compiled in. |
| `natskv` | All-NATS stack, uses JetStream KV — no Redis needed |
| `memory` | Local dev and tests, zero infrastructure. Always compiled in. |

### Observability — multiple providers can run simultaneously

| Provider | What it does |
|---|---|
| `sql` | Structured events to SQLite, PostgreSQL, or MySQL. Powers the dashboard. Always compiled in. |
| `prometheus` | Scrape endpoint at `/metrics`. Always compiled in. |
| `otel` | OTLP traces and metrics to any OpenTelemetry collector |
| `webhook` | HTTP POST on every event to a configurable URL. Always compiled in. |
| `stdout` | JSON lines to stdout, feeds any log aggregation pipeline. Always compiled in. |
| `noop` | Discard all events (default when no provider is configured). Always compiled in. |

### Target — how the sidecar talks to its co-located app

| Provider | Best for |
|---|---|
| `http` | Default. Plain HTTP over TCP. Always compiled in. |
| `unix` | Same HTTP contract over a Unix domain socket — zero TCP overhead for same-host calls. |
| `grpc` | gRPC via the `LensTarget` proto service. Lowest overhead, strongly typed. |

---

## Provider map

```mermaid
graph TD
    Agent[Lens Agent]

    Agent --> Transport
    Agent --> Persistence
    Agent --> Discovery
    Agent --> Observability
    Agent --> Target

    Transport --> grpc[grpc\ndirect pod-to-pod]
    Transport --> nats_t[nats\nbroker fan-out]
    Transport --> kafka[kafka\nhigh-throughput]
    Transport --> zeromq[zeromq\nbrokerless]
    Transport --> redisstreams[redis-streams\nreuse existing instance]

    Persistence --> redis_p[redis\ndurable default]
    Persistence --> natskv[natskv\nJetStream KV]
    Persistence --> memory[memory\ndev / test]

    Discovery --> memberlist[memberlist\ngossip UDP]
    Discovery --> nats_d[nats\nsame broker as transport]
    Discovery --> dnssrv[dnssrv\nDNS SRV / K8s headless]
    Discovery --> static_d[static\nfixed seed list]

    Observability --> sql[sql\nSQLite / PostgreSQL / MySQL]
    Observability --> prometheus[prometheus\n/metrics]
    Observability --> otel[otel\nOTLP traces + metrics]
    Observability --> webhook[webhook\nHTTP POST events]
    Observability --> stdout[stdout\nJSON log lines]
    Observability --> noop[noop\ndiscard]

    Target --> http_t[http\nplain HTTP default]
    Target --> unix_t[unix\nUnix socket zero-overhead]
    Target --> grpc_t[grpc\nproto service]
```

---

## Example stacks

The same codebase, different `lens.yaml` — no code or build changes.

### Minimal (zero external infrastructure)

```yaml
transport:   { provider: grpc }
persistence: { provider: memory }
discovery:   { provider: memberlist }
```

```bash
lens-build
```

### Production (durable store + metrics)

```yaml
transport:
  provider: grpc
  config: { grpcPort: "8901" }

persistence:
  provider: redis
  config: { addr: "redis:6379" }

discovery:
  provider: memberlist
  config: { bindPort: 7946 }

observer:
  enabled: true
  providers:
    - name: sql
      config: { driver: postgres, dsn: "postgres://lens:lens@postgres:5432/lens?sslmode=disable" }
    - name: prometheus
```

```bash
lens-build
```

### All-in-one broker (single NATS server for every layer)

```yaml
transport:   { provider: nats,   config: { natsUrl: "nats://broker:4222" } }
persistence: { provider: natskv, config: { natsUrl: "nats://broker:4222" } }
discovery:   { provider: nats,   config: { natsUrl: "nats://broker:4222" } }
```

```bash
lens-build
```

---

## Quick start

```bash
cd example
docker compose -f docker-compose.nats-standalone.yml up --build -d
```

Three app pods + three sidecars + NATS + PostgreSQL. Open `http://localhost:8921` for the dashboard.

---

## Architecture

Each pod runs one sidecar. Sidecars discover each other through the configured discovery layer and communicate through the configured transport. Any client or dashboard only needs to reach one sidecar — it routes to the rest.

```mermaid
graph LR
    subgraph Pod A
        AppA[App A]
        SA[Sidecar A\n:8900]
        AppA <-->|localhost| SA
    end

    subgraph Pod B
        AppB[App B]
        SB[Sidecar B\n:8900]
        AppB <-->|localhost| SB
    end

    subgraph Pod C
        AppC[App C]
        SC[Sidecar C\n:8900]
        AppC <-->|localhost| SC
    end

    Store[(Persistence layer)]

    SA <-->|transport layer| SB
    SA <-->|transport layer| SC
    SB <-->|transport layer| SC

    SA <-->|replay log\naudit log| Store
    SB <--> Store
    SC <--> Store

    Dashboard[Dashboard / API client] -->|HTTP| SA
```

---

## Integrating your app

The sidecar calls your app through the configured **target provider** (`http` by default, or `unix`/`grpc` for lower overhead). Expose these endpoints on the app — the contract is the same regardless of which provider is used.

### Identity endpoint

```
GET /internal/lens/info
-> { "service": "my-service", "instance": "pod-xyz" }
```

Called once on startup. `service` is shared by all replicas; `instance` is unique per pod (use hostname or pod name).

### Invalidate endpoint

```
POST /internal/lens/invalidate
<- { "pattern": "some-prefix" }
-> 200 OK
```

Remove cached entries whose key contains `pattern`. Pass `null` to clear everything.

### Fetch endpoint

```
POST /internal/lens/get
<- { "key": "my-key:123" }
-> { "found": true, "value": "..." }
```

Return the current value of a key from this pod's cache. Return `"found": false` when absent.

### Declare endpoint (optional — enables dashboard key browsing)

```
POST http://localhost:8900/api/declare
<- { "keyName": "my-key:123", "keySchema": null, "ttlInSeconds": 3600 }
```

Call this whenever your app writes to its cache. Keys appear in the dashboard without this call but the schema metadata won't be stored.

---

## Adding your own provider

Any layer can be extended without touching existing code.

```go
// 1. Implement the interface and register in init()
func init() {
    transport.Register("my-provider", func(host transport.TransportHost, cfg map[string]any) (transport.Transport, error) {
        return newMyTransport(host, cfg)
    })
}
```

```go
// 2. Add one entry to the import map in cmd/lens-build/main.go
"my-provider": "github.com/Vedanshu7/lens/internal/transport/myprovider",
```

```yaml
# 3. Set it in lens.yaml and rebuild
transport:
  provider: my-provider
```

```bash
lens-build
```

No build tags. No stub files. No Makefile changes.

---

## Sidecar API

All endpoints are available from any sidecar. Clients only need to reach one.

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/health` | Connectivity check for all layers |
| `GET` | `/api/services` | List all services with live sidecars |
| `GET` | `/api/nodes?service=X` | List live instances for a service |
| `GET` | `/api/keys?service=X` | List declared cache keys |
| `GET` | `/api/providers?service=X` | Active provider stack for a service |
| `POST` | `/api/fetch` | Read a value from a specific instance's cache |
| `POST` | `/api/invalidate` | Broadcast a cache clear across all instances |
| `POST` | `/api/declare` | Register a cache key (called by your app) |
| `GET` | `/api/audit` | Invalidation audit log (last 500 entries) |
| `GET` | `/metrics` | Prometheus metrics (when prometheus provider active) |
| `GET` | `/api/obs/latency` | Latency percentiles over time (SQL observer required) |
| `GET` | `/api/obs/flow` | Invalidation and fetch throughput |
| `GET` | `/api/obs/deadpods` | Pods that timed out during invalidation |
| `GET` | `/api/obs/discovery` | Peer join and leave events |
| `GET` | `/api/obs/summary` | Aggregate metrics for a service |

---

## Dashboard

Each sidecar serves its own dashboard. Opening any sidecar port gives you that cluster's live view — services, nodes, keys, audit log, and observability charts. Provider stack badges show the active transport, persistence, discovery, and observer combination per service.

**Dev mode:**

```bash
cd dashboard
cp .env.example .env      # set VITE_SIDECAR_PORT to your sidecar's port
npm install && npm run dev
```

Two stacks side by side:

```bash
VITE_PORT=5173 VITE_SIDECAR_PORT=8901 npm run dev   # cluster A
VITE_PORT=5174 VITE_SIDECAR_PORT=8921 npm run dev   # cluster B
```

Pre-built image: `ghcr.io/vedanshu7/lens-dashboard:main`

---

## Configuration reference

All configuration is via `lens.yaml` or `LENS_*` environment variables.

```yaml
transport:
  provider: <name>
  config: <provider-specific>

persistence:
  provider: <name>
  config: <provider-specific>

discovery:
  provider: <name>
  config: <provider-specific>

target:
  provider: http          # http (default) | unix | grpc
  config:
    url: "http://localhost:8080"   # http provider
    socketPath: /tmp/app.sock      # unix provider
    grpcAddr: "localhost:8902"     # grpc provider

observer:
  enabled: true
  providers:
    - name: <name>
      config: <provider-specific>

agent:
  port: "8900"
  bindAddr: "0.0.0.0"
  token: ""
  logLevel: info
  replay:
    enabled: true
    windowHours: 24
```

| Variable | Default | Description |
|---|---|---|
| `LENS_TARGET_PROVIDER` | `http` | Target provider: `http`, `unix`, or `grpc` |
| `LENS_TARGET_URL` | `http://localhost:8080` | Base URL of the app (http provider) |
| `LENS_TARGET_SOCKET_PATH` | _(empty)_ | Unix socket path (unix provider) |
| `LENS_TARGET_GRPC_ADDR` | `localhost:8902` | gRPC address of the app (grpc provider) |
| `LENS_TOKEN` | _(empty)_ | Shared secret sent as `x-lens-token`. Empty disables auth. |
| `LENS_PORT` | `8900` | HTTP port the sidecar listens on |
| `LENS_BIND_ADDR` | `0.0.0.0` | Address the HTTP server binds to |
| `LENS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `LENS_ADVERTISE_ADDR` | _(auto)_ | IP peers use to reach this pod. Override when behind NAT. |
| `LENS_COOLDOWN_MS` | `1000` | Minimum ms between invalidations for the same service |
| `LENS_REPLAY_ENABLED` | `true` | Replay missed invalidations on startup |
| `LENS_REPLAY_WINDOW_HOURS` | `24` | How far back the replay log is scanned on startup |

---

## Building from source

Install `lens-build` once, then use it to compile a binary containing only the providers declared in your `lens.yaml`.

```bash
git clone https://github.com/Vedanshu7/lens.git
cd lens

# Install the build tool
go install ./cmd/lens-build

# Write your lens.yaml, then build
lens-build                          # outputs ./lens
lens-build -output /usr/local/bin/lens
lens-build -dry-run                 # preview what will be compiled
```

`lens-build` reads `lens.yaml`, generates a minimal import file for the configured providers, runs `go build`, and cleans up. The binary contains only what the config asked for.

Minimum Go version: **1.24**

---

## License

MIT. See [LICENSE](LICENSE).
