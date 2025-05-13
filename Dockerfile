FROM alpine:3.19 AS certs
RUN apk --update add ca-certificates

FROM golang:1.23.6 AS build-stage

COPY . /tmp/otelex

WORKDIR /tmp/otelex/collector/

RUN --mount=type=cache,mode=0755,target=/root/.cache/go-build \
    GOARCH=amd64 GOOS=linux CGO_ENABLED=0 go build -o /bqexporter .

FROM gcr.io/distroless/base:latest

ARG USER_UID=10001
USER ${USER_UID}

COPY ./tmp/otelcol-config.yaml /otelcol/collector-config.yaml
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build-stage /bqexporter /otelcol

ENTRYPOINT ["/otelcol"]
CMD ["--config", "/otelcol/collector-config.yaml"]

EXPOSE 4317 4318 12001
