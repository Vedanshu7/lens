FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Install lens-build, then use it to compile a binary containing only the
# providers declared in lens.yaml. CGO_ENABLED=0 for a static binary.
RUN CGO_ENABLED=0 go install ./cmd/lens-build && \
    CGO_ENABLED=0 lens-build -config lens.yaml -output /lens

FROM gcr.io/distroless/static-debian12
COPY --from=build /lens /lens
ENTRYPOINT ["/lens"]
