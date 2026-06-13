BINARY   = lens
FULL_TAGS = lens_grpc lens_nats lens_zmq lens_redisstreams lens_kafka \
            lens_memberlist lens_static lens_dnssrv lens_otel

.PHONY: build build-full build-grpc build-nats build-kafka build-zmq \
        docker docker-full clean help

build: ## gRPC transport + memberlist discovery (smallest useful binary)
	go build -tags "lens_grpc lens_memberlist" -o $(BINARY) .

build-full: ## All providers compiled in
	go build -tags "$(FULL_TAGS)" -o $(BINARY) .

build-grpc: ## gRPC transport + memberlist discovery
	go build -tags "lens_grpc lens_memberlist" -o $(BINARY) .

build-nats: ## NATS transport + memberlist discovery
	go build -tags "lens_nats lens_memberlist" -o $(BINARY) .

build-kafka: ## Kafka transport + memberlist discovery
	go build -tags "lens_kafka lens_memberlist" -o $(BINARY) .

build-zmq: ## ZeroMQ transport + memberlist discovery
	go build -tags "lens_zmq lens_memberlist" -o $(BINARY) .

docker: ## Docker image with default providers (gRPC + memberlist)
	docker build --build-arg LENS_TAGS="lens_grpc lens_memberlist" -t lens:grpc .

docker-full: ## Docker image with all providers
	docker build --build-arg LENS_TAGS="$(FULL_TAGS)" -t lens:full .

clean:
	rm -f $(BINARY)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*## "}; {printf "  %-20s %s\n", $$1, $$2}'
