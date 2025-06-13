FROM golang:alpine AS builder

WORKDIR /

COPY go.mod go.mod
COPY go.sum go.sum
COPY tools.go tools.go
COPY buildfix.go buildfix.go
COPY config/builder-config.yaml config/builder-config.yaml
COPY internal/spattex/bigquery internal/spattex/bigquery

RUN --mount=type=cache,mode=0755,target=/root/.cache/go-build \
    GOARCH=amd64 GOOS=linux CGO_ENABLED=0 go build -o /tmp/ocb \
    go.opentelemetry.io/collector/cmd/builder

RUN /tmp/ocb --skip-compilation \
    --config config/builder-config.yaml

RUN --mount=type=cache,mode=0755,target=/root/.cache/go-build \
    GOARCH=amd64 GOOS=linux CGO_ENABLED=0 go build -o /tmp/bq/bq .

# Build the final (persistent) image
FROM alpine
RUN adduser -S -u 10000 otelex
COPY --from=builder /tmp/bq/bq /bq
COPY config/otelcol-config.yaml otelcol-config.yaml
ENTRYPOINT ["/bq"]
CMD ["--config", "otelcol-config.yaml"]
