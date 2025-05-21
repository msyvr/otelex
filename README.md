For more details on this project, see the accompanying [blog post](https://monicaspisar.com/posts/otelcol-for-all).

## otelex: An OpenTelemetry Collector distribution with custom components

This is an exercise to build a custom OpenTelemetry Collector distribution that includes a custom exporter which unbundles span attributes (key-value pairs) into an independent map structure. 

### Context

Do not try this at home :) It was a creative solution to a particular need in a production software deploy. I've replicated the basic idea to stress test it and get a sense of limitations.

> Why a custom exporter, and not a custom processor?

The Collector is designed to process [OTLP](https://opentelemetry.io/docs/specs/otlp/) encoded data. Exporting to a target that doesn't expect OTLP data requires unbundling. Doing that decoding/unbundling at the last stage before export minimizes the footprint of non-OTLP data within the Collector. I don't have any data to show that this is necessary or even useful. Even if it's not, it feels _cleaner_ to structure it thus.

### Custom exporter capabilities

To test this on export to a real database, BigQuery is used here. The `internal/spattex/bigquery` exporter verifies that each span's attributes (key-value pairs) align with the target database schema. The key-value pairs are extracted to a map. Those maps are equivalent to rows for export to BigQuery and batches of rows are exported for insertion into the receiving table.

### Project outline

- [x] write the custom exporter component
- [x] create the custom collector build manifest that includes the custom exporter
- [x] build the custom collector distribution (use OTel's `ocb`)
- [ ] instrument a test service to emit at least two streams of data: 
  - [ ] observability traces 
  - [ ] key-value pairs bundled into span attributes
- [x] deploy an instance of the custom collector distribution (docker)
- [x] prep an observability data inspection tool (e.g., locally, Jaeger)
- [ ] evaluate collector performance limits
  - [ ] test for ingest rate limits
  - [ ] test for processing limits
  - [ ] test for export rate limits
  - [ ] investigate data unbundling impact on latency
  - [ ] security considerations

_This project is inspired by a data pipeline that handled both observability and client usage data. The approach allowed for fewer components, making for a simpler system. Here, I'm aiming to interrogate the limits of this implementation._

## OpenTelemetry Collector

An [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/) is a software component for consolidating the handling of one or more OTLP data streams en route to database or visualization targets.

### Pre-built distributions

A [pre-built Collector distribution can be downloaded](https://github.com/open-telemetry/opentelemetry-collector-releases/releases) directly from OpenTelemetry's Collector repo on Github. The choice of pre-built distribution establishes which components, services, and extensions are available for pipelines, though it doesn't establish the pipelines themselves. Three basic flavors of [pre-built distributions are maintained by OpenTelemetry](https://github.com/open-telemetry/opentelemetry-collector-releases/releases):
1. core
2. contrib
3. kubernetes

Separately, the pipeline configuration(s) are defined in a manifest that's used with the `--config` flag when running the distribution binary to deploy a Collector instance. We'll go over that in the section on [Collector config](#collector-config).

Using a pre-built distribution is convenient. For production systems, though, best practice is to customize the distribution to include only the required components. Let's look at how to do that.

### Custom distributions

There are two cases where custom Collector builds are the way to go -

First, building a custom distribution is best practice when using just a subset of components from a pre-built distribution: extraneous components increase the attack surface, posing an unnecessary security risk, and they also needlessly increase the size of the distribution binary.

Second, when components in the `core` and `contrib` collections don't provide the required functionality, a custom Collector build is the only option.

OpenTelemetry maintains a tool for generating custom builds: the [Collector Builder](https://github.com/open-telemetry/opentelemetry-collector/tree/main/cmd/builder). The builder tool is included in [OpenTelemetry Collector releases with `cmd/builder` tags](https://github.com/open-telemetry/opentelemetry-collector-releases/tags). For custom builds, you may choose to [download just the builder component](https://opentelemetry.io/docs/collector/custom-collector/#step-1---install-the-builder) separately from the Collector itself. Here's how you can do that for the version you'd need for macOS (ARM64) and OTel v0.125.0 (current as of 2025.05.07):
```bash
curl --proto '=https' --tlsv1.2 -fL -o ocb \
https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/cmd%2Fbuilder%2Fv0.125.0/ocb_0.125.0_darwin_arm64
chmod +x ocb
```

Once downloaded, an `ocb` directory will be added to the current directory, and guidance for using the builder can be accessed via:

```bash
./ocb help
```

At this point, the Collector _builder_ is ready for customization. There are two possibilities at this stage -

Option 1: Limit which standard components will be available. No custom components.

- All that's needed at this point is a configuration file that details which components to include. Details in the section on [Build config](#build-config).

Option 2: Include custom components.

- The custom components need to be constructed before inclusion in the distribution build config/manifest. Details in the section on [Building custom components](#building-custom-components).

Both options require a builder manifest that identifies which components to include in the resulting Collector distribution. With the builder manifest file as `builder-config.yaml` and the builder binary at `./ocb`, the custom Collector distribution is built as:

```bash
./ocb --config builder-config.yaml
```

The builder manifest will specify a name and path for the target folder which will contain the binary for the new distribution and the files from which the binary was built.

## Building custom components

Custom components are built from scratch: there's no official builder utility as of 2025.05.12.

The APIs for receiver, processor, and exporter components:
- [opentelemetry-collector/receiver/receiver.go](https://github.com/open-telemetry/opentelemetry-collector/blob/main/receiver/receiver.go)
- [opentelemetry-collector/processor/processor.go](https://github.com/open-telemetry/opentelemetry-collector/blob/main/processor/processor.go)
- [opentelemetry-collector/exporter/exporter.go](https://github.com/open-telemetry/opentelemetry-collector/blob/main/exporter/exporter.go)
 

Any component referenced in the distribution manifest will need a `NewFactory()` function that returns a `Factory` interface that'll enable production of that specific component for use in the Collector. Here's an example for [exporters](https://pkg.go.dev/go.opentelemetry.io/collector/exporter):

```go
func NewFactory(cfgType component.Type, createDefaultConfig component.CreateDefaultConfigFunc, options ...FactoryOption) Factory {
	f := &factory{
		cfgType:                 cfgType,
		CreateDefaultConfigFunc: createDefaultConfig,
	}
	for _, opt := range options {
		opt.applyOption(f)
	}
	return f
}
```

Both `cfgType` and `createDefaultConfig` are required to generate a component Factory. For otelex, those are defined in `factory.go`.

To export non-OTLP formatted data, otelex uses the [`exporter.WithTraces()`](https://pkg.go.dev/go.opentelemetry.io/collector/exporter#WithTraces) option. It takes as an argument a [`CreateTracesFunc`](https://pkg.go.dev/go.opentelemetry.io/collector/exporter#CreateTracesFunc) where custom actions for 'pushing' traces can be defined with the expectation of returning an `error` type. The OTLP traces are consumed, with no expectations attached to retaining their OTLP format.

For the BigQuery exporter included in otelex, custom actions are applied to batches of spans to extract, on a span-by-span basis, attributes into a map structure. The map structure represents a target table row (values) for the target table schema (keys). During extraction, both key and value types are checked and converted. Each map can then be appended to an array of map-rows, and the batch exported via an API request.

A useful reference for building custom Collector components is the collection of custom components in the [`opentelemetry-collector-contrib` collection](https://github.com/open-telemetry/opentelemetry-collector-contrib).


## Adding custom components to a Collector

The [Collector builder manifest](https://monicaspisar.com/posts/otelcol-for-all/#build-config) itemizes which components to include in the distribution. If the custom component is published as a module visible to Go tools, you can reference it similarly to existing/standard components from `core` or `contrib`.

If the custom component is local, you can reference it by its full path. So long as the distribution library build is run locally, or the component files are exported to the filesystem of the deploy environment, the component will be accessible and will be incorporated into the distribution.

## Configuration files

### Build config

This is for the Collector _distribution_ binary.

The `ocb` utility for building custom Collector distributions requires a manifest that tells it which components to include. (It doesn't specify the pipelines, just the components that will be available for building the pipelines.) 

An [example build manifest from OpenTelemetry](https://opentelemetry.io/docs/collector/custom-collector/) shows how to include components from `core` or `contrib`.

In [otelex](https://github.com/msyvr/otelex), the [custom exporter component](https://github.com/msyvr/otelex/tree/main/internal/spattex/bigquery) is included in the `builder_config.yaml` with a local path as it's not a published Go module (more on that in [Docker deploy](#docker-deploy)):
```yaml
exporters:
  - gomod: "github.com/msyvr/otelex/internal/spattex/bigquery v0.1.0"
    path: "msyvr/o11y/otelex/internal/spattex/bigquery"
```

### Collector config

This is for the deployed Collector _instance_.

At this point, the Collector _distribution_ is prepared, whether prebuilt or customized. The distribution determines which components (and some other options) are available for building pipelines. Those will be composed into pipelines in the Collector [configuration](https://opentelemetry.io/docs/collector/configuration/#basics) file.

The otelex uses a basic configuration: [`config/otelcol-config.yaml`](https://github.com/msyvr/otelex/blob/main/config/otelcol-config.yaml)

## Deploy Collector instances

The manifest is provided to the `--config` flag when the run command for the Collector instance is executed. Locally, run the binary at `otelcol/custom-build` with manifest at `/path/to/collector-config.yaml` as:

```bash
otelcol-custom --config /path/to/config-filename.yaml
```

>Validate the config file: Running the above with`validate` preceding `--config` will check that the provided config file aligns with a valid OpenTelemetry Collector configuration.

>Chained config flags are merged: Multiple `--config` flags will be merged into a single configuration; if the merger doesn't yield a complete configuration, the command will error.

Alternatives for loading the configuration include - 

**environment variables:**
```bash
otelcol/custom-build --config=env:OTELCOL_CUSTOM_CONFIG
```
**YAML strings:**
```bash
otelcol/custom-build --config="yaml:exporters::custom-exporter"
```
**external URLs:**
```bash
otelcol/custom-build --config=https://my-domain.com/collector-config.yaml
```

### Docker deploy

When the Collector is deployed using a pre-built Docker image `otelcol-custom:latest`, the manifest file can be mounted as a volume on launch:

```bash
docker run -v /path/to/collector-config.yaml:/etc/otelcol-custom/config.yaml otelcol-custom:latest
```

The image needs to be built first, of course. This deserves a note for the case where unpublished components need to be included. The component may be unpublished for a reason and a multistage Docker build can be used to avoid exposing the custom component files.

The image will be built locally in the environment where the component files can be accessed directly, so they can be copied into a (non-final) stage of the Docker build. There, the distribution that includes that custom component is built, referencing the files. However, a subsequent stage involves copying in only the distribution binary. That way, the final (persistent) image exposes the distribution binary without exposing the individual component.

## Visualization

Visualization options include open source tools and third party providers. Provisioned here is a [Jaeger](https://www.jaegertracing.io/) Docker image to run locally: `sh/jaeger.sh`

