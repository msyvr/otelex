#!/bin/sh

# Run locally and set an otlp exporter endpoint 127.0.0.1:16686
# to use Jaeger in-browser.

docker run -d --name jaeger \
  -e COLLECTOR_OTLP_ENABLED=true \
  -p 16686:16686 \
  -p 14317:4317 \
  -p 14318:4318 \
  jaegertracing/all-in-one:1.41