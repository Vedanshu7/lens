# Lens

Distributed caches go stale. When one replica writes a change, every other pod keeps serving the old value until TTL expires. The usual fix (a cache client that calls a central invalidation bus) adds a new SDK dependency, couples your service to your cache infrastructure, and still misses pods that were offline during the event.

Lens is a zero-SDK cache-invalidation sidecar. Deploy one beside each replica. They find each other, broadcast invalidation events cluster-wide, and replay any events missed while a pod was down. No changes to your application code and no coupling to your cache library.

```
One sidecar per replica.  Pick any transport, discovery, and observability provider.
Switch at config time.    No code changes.  No rebuild.
```

---

## How it works

One sidecar runs beside each replica. Everything it touches sits behind one of
five provider interfaces, so the shape below holds for every configuration. Only
what sits on the far side of each seam changes.

```mermaid
graph TB
    subgraph pod["Each replica"]
        App["Your app"]
        SC["Lens sidecar"]
    end

    App <-->|"target layer"| SC

    SC <-->|"discovery layer"| Peers["Peer sidecars"]
    SC <-->|"transport layer"| Peers
    SC -->|"persistence layer"| Store[("Replay log, audit trail")]
    SC -->|"observability layer"| Obs["Metrics, traces, events"]

    Client["Client or dashboard"] -->|"HTTP :8900"| SC
```

**Lifecycle:**

1. The sidecar starts and asks the app it fronts for its service and instance identity, over the configured target layer
2. It registers itself with the discovery layer and begins watching for peers
3. A client or the app posts an invalidation to `POST /api/invalidate`, which is throttled and optionally batched
4. The event goes out over the transport layer to every peer
5. Each peer delivers the invalidation to the app it fronts over its target layer, and records the event through the persistence layer
6. On restart, a sidecar replays the invalidations it missed while it was down

Any client or dashboard only needs to reach **one** sidecar. It routes to the rest.

**What your provider choice changes:**

| Layer | What varies |
|---|---|
| Transport | `grpc` and `zeromq` deliver directly between sidecars, so peers form a mesh. `nats`, `kafka` and `redis-streams` publish to a broker that fans out, so sidecars never connect to each other. |
| Discovery | `memberlist`, `nats`, `zookeeper`, `mdns` and `dnssrv` announce the instance and track peers as they come and go. `static` announces nothing and reads a fixed seed list, so step 2 is local bookkeeping only. |
| Persistence | `redis` and `natskv` survive a restart, so step 6 replays. `memory` is in-process and loses everything when the sidecar exits, so there is nothing to replay. |
| Target | `http` and `unix` call `/internal/lens/*` on your app. `grpc` calls a proto service instead, so your app implements the service rather than the endpoints. `http` and `grpc` take an address and default to localhost, but nothing requires the app to be on the same host. Only `unix` is same-host by construction. |
| Observability | Any number can run at once, including none. |

---

## Quick start

```bash
cd example
docker compose -f docker-compose.nats-standalone.yml up --build -d
```

Three app replicas + three sidecars + NATS + PostgreSQL. Open `http://localhost:8921` for the live dashboard, then trigger a cluster-wide invalidation:

```bash
curl -X POST http://localhost:8900/api/invalidate \
  -H "Content-Type: application/json" \
  -d '{"service":"demo","pattern":"user:"}'
```

---

## Providers

### Transport: how sidecars broadcast to each other

| Provider | Best for |
|---|---|
| `grpc` | Direct pod-to-pod, lowest latency, no broker required |
| `nats` | Broker fan-out, pods behind NAT or in separate subnets |
| `kafka` | High-throughput fan-out, Kafka already in stack |
| `zeromq` | Brokerless pub/sub, minimal footprint |
| `redis-streams` | Reuses an existing Redis instance, zero extra infra |

### Discovery: how sidecars find each other

| Provider | Best for |
|---|---|
| `memberlist` | SWIM gossip over UDP, zero infrastructure |
| `nats` | Uses the same broker already running for transport |
| `dnssrv` | Kubernetes headless services, Consul DNS |
| `static` | Fixed known peer list, no infrastructure |
| `zookeeper` | ZooKeeper ensemble, ephemeral znodes; for existing ZK infra |
| `mdns` | mDNS/Zeroconf `_lens._tcp`, zero config, local network only |

### Persistence: replay log, audit trail, shared metadata

| Provider | Best for |
|---|---|
| `redis` | Production default, durable, widely available. Always compiled in. |
| `natskv` | All-NATS stack, uses JetStream KV; no Redis needed |
| `memory` | Local dev and tests, zero infrastructure. Always compiled in. |

### Observability: multiple providers can run simultaneously

| Provider | What it does |
|---|---|
| `sql` | Structured events to SQLite, PostgreSQL, or MySQL. Powers the dashboard. Always compiled in. |
| `prometheus` | Scrape endpoint at `/metrics`. Always compiled in. |
| `otel` | OTLP traces and metrics to any OpenTelemetry collector |
| `influxdb` | InfluxDB v2 line protocol, time-series metrics over HTTP |
| `opensearch` | OpenSearch / Elasticsearch bulk API, full-text search over events |
| `webhook` | HTTP POST on every event to a configurable URL. Always compiled in. |
| `stdout` | JSON lines to stdout, feeds any log aggregation pipeline. Always compiled in. |
| `noop` | Discard all events (default when no provider is configured). Always compiled in. |

### Target: how the sidecar talks to the app it fronts

Usually that app is in the same pod, which is what the sidecar model is for, but
`http` and `grpc` take an address and will talk to a remote one just as happily.

| Provider | Best for | Address |
|---|---|---|
| `http` | Default. Plain HTTP over TCP. Always compiled in. | `targetURL`, default `http://localhost:8080` |
| `unix` | Same HTTP contract over a Unix domain socket, zero TCP overhead. Same host only. | `socketPath`, required |
| `grpc` | gRPC via the `LensTarget` proto service. Lowest overhead, strongly typed. | `grpcAddr`, default `localhost:8902` |

---

## Example stacks

The same codebase, different `lens.yaml`. No code or build changes.

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

### ZooKeeper + OpenSearch (existing ZK and Elastic infra)

```yaml
discovery:
  provider: zookeeper
  config: { servers: "zk1:2181,zk2:2181,zk3:2181" }

observer:
  enabled: true
  providers:
    - name: opensearch
      config:
        url: "https://opensearch:9200"
        index: lens-events
        username: admin
        password: ${OPENSEARCH_PASSWORD}
```

```bash
lens-build
```

### mDNS + InfluxDB (zero-config local dev with time-series metrics)

```yaml
discovery:
  provider: mdns

observer:
  enabled: true
  providers:
    - name: influxdb
      config:
        url: "http://influxdb:8086"
        token: ${INFLUX_TOKEN}
        org: myorg
        bucket: lens
```

```bash
lens-build
```

---

## Integrating your app

The sidecar calls your app through the configured **target provider** (`http` by default, or `unix`/`grpc` for lower overhead). Expose these endpoints; the contract is the same regardless of which target provider is used.

### Identity endpoint

```
GET /internal/lens/info
→ { "service": "my-service", "instance": "pod-xyz" }
```

Called once on startup. `service` is shared by all replicas; `instance` is unique per pod (use the hostname or pod name).

### Invalidate endpoint

```
POST /internal/lens/invalidate
← { "pattern": "some-prefix" }
→ 200 OK
```

Remove cached entries whose key contains `pattern`. Pass `null` to clear everything.

### Fetch endpoint

```
POST /internal/lens/get
← { "key": "my-key:123" }
→ { "found": true, "value": "..." }
```

Return the current value of a key from this pod's cache. Return `"found": false` when absent.

### Declare endpoint (optional, enables dashboard key browsing)

```
POST http://localhost:8900/api/declare
← { "keyName": "my-key:123", "keySchema": null, "ttlInSeconds": 3600 }
```

Call this whenever your app writes to its cache. Keys appear in the dashboard without it, but schema metadata won't be stored.

---

## Dashboard

Each sidecar serves its own dashboard. Opening any sidecar port gives a live view of the cluster: services, nodes, cache keys, audit log, and observability charts. Provider stack badges show the active transport, persistence, discovery, and observer combination per service.

The dashboard subscribes to `GET /api/events/stream` (Server-Sent Events) for real-time invalidation updates with no polling.

**Dev mode:**

```bash
cd dashboard
cp .env.example .env      # set VITE_SIDECAR_PORT to your sidecar's port
npm install && npm run dev
```

Two clusters side by side:

```bash
VITE_PORT=5173 VITE_SIDECAR_PORT=8901 npm run dev   # cluster A
VITE_PORT=5174 VITE_SIDECAR_PORT=8921 npm run dev   # cluster B
```

Pre-built image: `ghcr.io/vedanshu7/lens-dashboard:main`

---

## Hot reload

Lens watches the active `lens.yaml` for changes (200 ms debounce) and applies `logLevel` and `cooldownMs` immediately without restart. Non-reloadable fields (transport, discovery, and persistence providers) log a warning and take effect only after the next restart.

---

## Multi-region / cross-datacenter

Add a `regions` block to broadcast each invalidation to remote clusters in parallel:

```yaml
agent:
  regions:
    - name: us-west
      url: "https://lens-us-west.internal"
      token: ${LENS_REGION_TOKEN}
    - name: eu-central
      url: "https://lens-eu.internal"
      token: ${LENS_REGION_TOKEN_EU}
```

Lens forwards to each region concurrently with a 5 s timeout (fire-and-forget). An `X-Lens-Cross-Region: true` header prevents A→B→A loops.

---

## TLS / mTLS for gRPC transport

```yaml
transport:
  provider: grpc
  config:
    grpcPort: "8901"
    tlsCertFile: /etc/lens/tls/tls.crt
    tlsKeyFile:  /etc/lens/tls/tls.key
    tlsCAFile:   /etc/lens/tls/ca.crt   # omit for TLS only; include to enable mTLS
```

When `tlsCAFile` is set, the server requires and verifies client certificates (`RequireAndVerifyClientCert`). Omit it for one-way TLS. Omit all three to use plaintext (default).

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
| `GET` | `/api/events/stream` | Server-Sent Events stream of live invalidation events |
| `POST` | `/api/fetch` | Read a value from a specific instance's cache |
| `POST` | `/api/invalidate` | Broadcast a cache clear across all instances |
| `POST` | `/api/declare` | Register a cache key schema (called by your app) |
| `GET` | `/api/audit` | Invalidation audit log (last 500 entries) |
| `GET` | `/metrics` | Prometheus metrics (when prometheus provider active) |
| `GET` | `/api/obs/latency` | Latency percentiles over time (SQL observer required) |
| `GET` | `/api/obs/flow` | Invalidation and fetch throughput |
| `GET` | `/api/obs/deadpods` | Pods that timed out during invalidation |
| `GET` | `/api/obs/discovery` | Peer join and leave events |
| `GET` | `/api/obs/summary` | Aggregate metrics for a service |

---

## Configuration reference

All configuration is via `lens.yaml` or `LENS_*` environment variables. YAML wins for every field it sets; env vars serve as fallbacks. Secrets (`token`, passwords) are always read from env even when a config file is present.

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
    targetURL: "http://localhost:8080"   # http provider
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
  cooldownMs: 1000
  batchWindowMs: 0
  replay:
    enabled: true
    windowHours: 24
  regions:
    - name: us-west
      url: "https://lens-us-west.internal"
      token: ${LENS_REGION_TOKEN}
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
| `LENS_BATCH_WINDOW_MS` | `0` | Coalesce invalidations within this window (ms); 0 disables batching |
| `LENS_RATE_LIMIT_RPS` | `100` | Per-IP request rate limit (requests/s); 0 disables |
| `LENS_RATE_LIMIT_BURST` | `200` | Per-IP token-bucket burst size |
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

# Optional: install the CLI companion for querying sidecars from the terminal
go install ./cmd/lenscli

# Write your lens.yaml, then build
lens-build                          # outputs ./lens
lens-build -output /usr/local/bin/lens
lens-build -dry-run                 # preview what will be compiled
```

`lens-build` reads `lens.yaml`, generates a minimal import file for the configured providers, runs `go build`, and cleans up. The binary contains only what the config asked for.

Minimum Go version: **1.25.7**

---

## License

MIT. See [LICENSE](LICENSE).
