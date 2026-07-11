---
title: Lens
type: Infrastructure
projectURL: lens
descriptionShort: A zero-SDK cache-invalidation sidecar for distributed services — pluggable transport, discovery, persistence, and observability.
descriptionLong: Distributed caches go stale. When one replica writes a change, every other pod keeps serving the old value until its TTL expires — and the usual fix, a cache client that calls a central invalidation bus, adds an SDK dependency, couples your service to your cache infrastructure, and still misses pods that were offline during the event. Lens solves this as a sidecar. Deploy one beside each replica; they discover each other, broadcast invalidation events cluster-wide, and replay any events a pod missed while it was down. No application code changes and no coupling to your cache library. Every layer is a swappable provider chosen at config time — transport (NATS, gRPC), discovery (Consul, Kubernetes), persistence (PostgreSQL, Redis, SQLite), and observability (OpenTelemetry, Prometheus) — so you can change infrastructure without a rebuild.
viewCodeUrl: https://github.com/Vedanshu7/lens
viewProjectUrl:
projectImg: /project-image/lens.svg
technologies:
  - Go
  - NATS
  - gRPC
  - Redis
  - PostgreSQL
  - Kubernetes
  - OpenTelemetry
  - Prometheus
  - Docker
---
