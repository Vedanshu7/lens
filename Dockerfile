FROM golang:1.25-alpine AS build

# LENS_TAGS controls which providers are compiled into the binary.
# Override at build time: docker build --build-arg LENS_TAGS="lens_kafka lens_memberlist" .
# Use "full" target in Makefile to get all providers.
ARG LENS_TAGS="lens_grpc lens_memberlist"

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -tags "${LENS_TAGS}" -o /lens .

FROM gcr.io/distroless/static-debian12
COPY --from=build /lens /lens
ENTRYPOINT ["/lens"]
