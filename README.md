## otelex

>**OTel Collector + extras (custom components)**

This is an exercise to build a custom Open Telemetry Collector distribution that includes a custom exporter. 

The `internal/spanattex/bigquery` exporter module unbundles key-value pairs that were packaged at the data source into OTLP span attributes, and repackages them for export to BigQuery target table. The basic unbundling protocol: 
1. grab the key-value pairs from the span's `attributes` field
2. verify that the keys align with the export target database schema
3. repackage the associated values for export

Do not try this at home :) It was a creative solution to a particular need in a production software deploy. I've replicated the basic idea to stress test it and get a sense of any practical limitations.

> Why a custom exporter, and not a custom processor?

The Collector is designed to process OTLP encoded data. Exporting to a target that doesn't expect OTLP data requires unbundling. Doing that decoding/unbundling at the last stage before export minimizes the footprint of non-OTLP data within the Collector. I don't have any data to show that this is necessary or even useful. Even if it's not, it feels _cleaner_ to structure it thus.


### Project outline

- [ ] write the custom exporter component
- [ ] create the custom collector build manifest that includes the custom exporter
- [ ] build the custom collector distribution (use OTel's `ocb`)
- [ ] instrument a test service to emit at least two streams of data: 
  - [ ] observability traces 
  - [ ] key-value pairs bundled into span attributes
- [ ] deploy an instance of the custom collector distribution (docker)
- [ ] deploy an observability data inspection tool (jaeger or otel-desktop-viewer)
- [ ] evaluate collector performance limits
  - [ ] test for ingest rate limits
  - [ ] test for processing limits
  - [ ] test for export rate limits
  - [ ] investigate data unbundling impact on latency
  - [ ] security considerations

_This project is inspired by a data pipeline that handled both observability and client usage data. The approach allowed for fewer components, making for a simpler system. Here, I'm aiming to interrogate the limits of this implementation._

### Collector intro

An [Open Telemetry Collector](https://opentelemetry.io/docs/collector/) is a software component designed to handle ingest for [OTLP](https://opentelemetry.io/docs/specs/otlp/) encoded data, its processing, and ultimate export to designated targets.

A Collector may be deployed as either an agent, alongside a specific application/service, or as a standalone gateway that consolidates processing and export functions for input streams. A gateway deploy is useful in situations where a collection of data sources (say, a distributed fleet of servers) emit data streams which require an update in processing or export target. With the gateway, those changes can be made in just one place - the gateway - with no need to update the individual data sources that make up the collection. 

In either configuration, a Collector manages input streams, applies processing to designated streams, and fans streams out to one or more export targets. One way to think about it is that pipelines are set up within the Collector, and a given stream may be directed to follow one or more pipelines. 

When designed to act as a gateway for an application/service with high data flow rates, multiple identical Collectors can be set up downstream of a load balancer to improve reliability, resilience, and efficiency.

The Collector also emits its own observability data, for monitoring its performance/health.

### Collector distribution options

#### Pre-built

A [pre-built Collector distribution can be downloaded](https://github.com/open-telemetry/opentelemetry-collector-releases/releases) directly from OpenTelemetry's Collector repo on Github. 

The Collector instance's pipelines and services will ultimately be defined in a manifest file (`yaml`). The options for which pipelines and services may be named in that configuration file depend on the specific Collector distribution being run.

Three basic flavors of [pre-built distributions are maintained by Open Telemetry](https://github.com/open-telemetry/opentelemetry-collector-releases/releases):
1. core
2. contrib
3. kubernetes

The `core` distribution includes a limited set of components. The `contrib` distribution includes all components approved for inclusion in the [contrib collection](https://github.com/open-telemetry/opentelemetry-collector-contrib). And the `kubernetes` distribution is optimized for deploy in the context of a kubernetes cluster.

#### Custom builds

A custom Collector distribution might be useful to limit the build to a specific set of components (from `core` or `contrib` collections) or to incorporate custom components. 

Open Telemetry maintains a tool for generating custom builds: the [Collector Builder](https://github.com/open-telemetry/opentelemetry-collector/tree/main/cmd/builder). 

The builder tool is included in [Open Telemetry Collector releases with `cmd/builder` tags](https://github.com/open-telemetry/opentelemetry-collector-releases/tags). For custom builds, you may choose to [download just the builder component](https://opentelemetry.io/docs/collector/custom-collector/#step-1---install-the-builder) separately from the Collector itself. Here's how you can do that for the version you'd need for macOS (ARM64) and OTel v0.125.0 (current as of 2025.05.07):
```bash
curl --proto '=https' --tlsv1.2 -fL -o ocb \
https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/cmd%2Fbuilder%2Fv0.125.0/ocb_0.125.0_darwin_arm64
chmod +x ocb
```

Once downloaded, an `ocb` directory will be added to the current directory, and guidance for using the builder can be accessed via:

```bash
./ocb help
```

At this point, the Collector _builder_ is ready for customization. There are two options here. Both require that a builder manifest specifies the limited suite of components to be made available with the to-be-created Collector distribution.

Option 1: The goal of the customization is to limit which standard components will be available.

- All that's needed at this point is a configuration file that details which components to include. See the **Builder manifest** section.

Option 2: The customization will include custom components not included in either the `core` or `contrib` distributions.

- Before getting to the Builder manifest, the code for the custom components needs to be written. See the **Custom components** section, then follow on to the **Builder manifest** section.

#### Builder manifest (for all custom builds)

With the builder manifest file as `builder-config.yaml` and the builder binary at `./ocb`, the custom Collector distribution is built as:

```bash
./ocb --config builder-config.yaml
```

Unless the target output was renamed, an `otelcol-dev` folder has been created at the same folder level as the ocb folder.

### Collector components

Basic Open Telemetry Collector components are of three types:
1. receivers
2. processors
3. exporters

The OTel Collector [distribution](https://opentelemetry.io/docs/concepts/distributions/) you decide to use will determine which components will be available as options for building pipelines.

You'll define those pipelines in a Collector configuration file specified by the `--config` flag used during Collector deploy.

You can also build your own components... 

### Custom components

Custom components are built from scratch. Components need to be Go modules, and they need to meet certain requirements to work within the Collector framework.

The starting point for deciphering the mystery of what, precisely, is required in assembling the custom component is to recognize that any component referenced by in the distribution manifest must have a `NewFactory()` function that returns a `Factory` interface. Here's an example for [exporters](https://pkg.go.dev/go.opentelemetry.io/collector/exporter):

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

Both `cfgType` and `createDefaultConfig` are required, and we define those in `factory.go`. 

Choosing the `exporter.WithTraces()` option allows the inclusion of customized export functionality. We'll define that to unbundle span attributes, repackage as row data formatted for BigQuery ingest, and make an API call to send the data to BigQuery.

A useful approach is to reference custom components in the `contrib` collection as examples - [`opentelemetry-collector-contrib` collection](https://github.com/open-telemetry/opentelemetry-collector-contrib).

#### Adding custom components to a Collector

The Collector builder manifest is where components to include in the distribution are itemized.

If the custom component is published as a module visible to Go tools, you can reference it similarly to existing/standard components from `core` or `contrib`.

If the custom component is local, you can reference it by its full path. So long as the distribution library build is run locally, the component will be accessible and will be incorporated into the distribution.

#### Example: Custom exporter

The unbundling of span attributes to create value-rows which can be exported to a target table requires customization for the specific target; each such target-specific exporter should have its own module. 

The module for the exporter customized to export to a BigQuery table is `github.com/msyvr/internal/spattex/bigquery`. Include it in the `builder_config.yaml` as:
```yaml
exporters:
  - gomod: "github.com/msyvr/otelex/internal/spattex/bigquery v0.1.0"
    path: "msyvr/o11y/otelex/internal/spattex/bigquery"
```

### Collector manifest

At this point, we have a Collector _distribution_, whether prebuilt or customized. Now, we'll deploy a Collector _instance_.

The distribution used at deploy determines which components (and some other options) are available for building pipelines. Now, all that's needed is the [Collector configuration file](https://opentelemetry.io/docs/collector/configuration/), which is the manifest for building those pipelines and is referenced by the `--config` flag when the distribution binary is run.

[Example Collector manifest]

>By default, the Collector configuration file name is `otelcol.yaml`, but a different name - say, `config-filename.yaml` - is just as good as long as the `--config` flag references `/path/to/config-filename.yaml`.

### Deploy Collector instances

The manifest is provided to the `--config` flag when the run command for the Collector instance is executed, whether locally or containerized (Docker/Kubernetes).

Local deploy is generally limited to dev testing. Docker deploys are easy enough, so defaulting to using those locally is a fine option, too.

#### Local deploy

Locally, run the binary with the `config` flag:

```bash
otelcol-custom --config /path/to/config-filename.yaml
```

>Validate the config file: Running the above with`validate` preceding `--config` will check that the provided config file aligns with a valid OpenTelemetry Collector configuration.

>Chained config flags are merged: Multiple `--config` flags will be merged into a single configuration; if the merger doesn't yield a complete configuration, the command will error.

Alternatives for loading the configuration include environment variables:

```bash
otelcol-custom --config=env:OTELCOL_CUSTOM_CONFIG
```
... YAML strings:
```bash
otelcol-custom --config=https://my-domain.com/config-filename.yaml
```
... or external URLs:
```bash
otelcol-custom --config="yaml:exporters::custom-exporter"
```

#### Docker deploy

When the Collector is deployed using Docker, the manifest file can be mounted as a volume on launch:

```bash
docker run -v /path/to/config-filename.yaml:/etc/otelcol-custom/config.yaml otelcol-custom:latest
```

### Visualization

- [jaeger](https://www.jaegertracing.io/)
- [otel-desktop-viewer](https://github.com/CtrlSpice/otel-desktop-viewer?tab=readme-ov-file)
