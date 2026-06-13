# Lens

A Go sidecar framework with four independently swappable layers: **transport**, **discovery**, **persistence**, and **observability**.

Pick any provider for each layer. Combine them freely. Switch at config time with no code changes.

---

## Providers

### Transport — how pods broadcast to each other

| Provider | Build tag | Best for |
|---|---|---|
| `grpc` | `lens_grpc` | Direct pod-to-pod, lowest latency, no broker required |
| `nats` | `lens_nats` | Broker fan-out, pods behind NAT or in separate subnets |
| `kafka` | `lens_kafka` | High-throughput fan-out, Kafka already in stack |
| `zeromq` | `lens_zmq` | Brokerless pub/sub, minimal footprint |
| `redis-streams` | `lens_redisstreams` | Reuses an existing Redis instance, zero extra infra |

### Discovery — how pods find each other

| Provider | Build tag | Best for |
|---|---|---|
| `memberlist` | `lens_memberlist` | Gossip over UDP, zero infrastructure |
| `nats` | `lens_natsdiscovery` | Uses the same broker already running for transport |
| `dnssrv` | `lens_dnssrv` | Kubernetes headless services, Consul DNS |
| `static` | `lens_static` | Fixed known peer list, no infrastructure |

### Persistence — replay log, audit trail, shared metadata

| Provider | Build tag | Best for |
|---|---|---|
| `redis` | _(always compiled)_ | Production default, durable, widely available |
| `natskv` | `lens_natskv` | All-NATS stack, uses JetStream KV — no Redis needed |
| `memory` | _(always compiled)_ | Local dev and tests, zero infrastructure |

### Observability — multiple providers can run simultaneously

| Provider | Build tag | What it does |
|---|---|---|
| `sql` | _(always compiled)_ | Structured events to SQLite, PostgreSQL, or MySQL. Powers the dashboard. |
| `prometheus` | _(always compiled)_ | Scrape endpoint at `/metrics` |
| `otel` | `lens_otel` | OTLP traces and metrics to any OpenTelemetry collector |
| `webhook` | _(always compiled)_ | HTTP POST on every event to a configurable URL |
| `stdout` | _(always compiled)_ | JSON lines to stdout, feeds any log aggregation pipeline |
| `noop` | _(always compiled)_ | Discard all events (default when no provider is configured) |

---

## Provider map

```mermaid
graph TD
    Agent[Lens Agent]

    Agent --> Transport
    Agent --> Persistence
    Agent --> Discovery
    Agent --> Observability

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
```

---

## Example stacks

The same codebase, different build tags, different config — no logic changes.

### Minimal (zero external infrastructure)

```bash
go build -tags "lens_grpc lens_memberlist" -o lens .
```

```yaml
transport:   { provider: grpc }
persistence: { provider: memory }
discovery:   { provider: memberlist }
```

### Production (durable store + metrics)

```bash
go build -tags "lens_grpc lens_memberlist" -o lens .
```

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

### All-in-one broker (single NATS server for every layer)

```bash
go build -tags "lens_nats lens_natsdiscovery lens_natskv" -o lens .
```

```yaml
transport:   { provider: nats,   config: { natsUrl: "nats://broker:4222" } }
persistence: { provider: natskv, config: { natsUrl: "nats://broker:4222" } }
discovery:   { provider: nats,   config: { natsUrl: "nats://broker:4222" } }
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

Expose three HTTP endpoints on the target app. The sidecar calls these automatically.

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
// 2. Blank import in a providers_myprovider.go file at the root
//go:build lens_myprovider

package main

import _ "github.com/vedanshu/lens/internal/transport/myprovider"
```

```bash
# 3. Include the build tag when compiling
go build -tags "lens_grpc lens_memberlist lens_myprovider" -o lens .
```

No changes anywhere else in the codebase.

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

observer:
  enabled: true
  providers:
    - name: <name>
      config: <provider-specific>

agent:
  targetURL: "http://localhost:8080"
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
| `LENS_TARGET_URL` | `http://localhost:8080` | Base URL of the app this sidecar is attached to |
| `LENS_PORT` | `8900` | HTTP port the sidecar listens on |
| `LENS_BIND_ADDR` | `0.0.0.0` | Address the HTTP server binds to |
| `LENS_TOKEN` | _(empty)_ | Shared secret sent as `x-lens-token`. Empty disables auth. |
| `LENS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `LENS_ADVERTISE_ADDR` | _(auto)_ | IP peers use to reach this pod. Override when behind NAT. |
| `LENS_COOLDOWN_MS` | `1000` | Minimum ms between invalidations for the same service |
| `LENS_REPLAY_ENABLED` | `true` | Replay missed invalidations on startup |
| `LENS_REPLAY_WINDOW_HOURS` | `24` | How far back the replay log is scanned on startup |

---

## Building from source

```bash
git clone https://github.com/Vedanshu7/lens.git
cd lens

# Default build (gRPC transport, memberlist discovery)
go build -tags "lens_grpc lens_memberlist" -o lens .

# All-NATS build
go build -tags "lens_nats lens_natsdiscovery lens_natskv" -o lens .

# With OpenTelemetry
go build -tags "lens_grpc lens_memberlist lens_otel" -o lens .
```

Minimum Go version: **1.24**

---

## License

MIT. See [LICENSE](LICENSE).
