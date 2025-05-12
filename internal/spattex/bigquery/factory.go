package bigquery

import (
	"context"
	"errors"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/exporter"
)

var (
	typeStr = component.MustNewType("exporter")
)

const (
	stability component.StabilityLevel = component.StabilityLevelStable

	defaultProjectID      = "msyvr"
	defaultDataset        = "otelex"
	defaultTable          = "spattex"
	defaultSchemaFlexible = false
)

func NewFactory() exporter.Factory {
	return exporter.NewFactory(
		typeStr,
		createDefaultConfig,
		exporter.WithTraces(CreateBigQueryExporterFunc, stability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		ProjectID:      defaultProjectID,
		Dataset:        defaultDataset,
		Table:          defaultTable,
		SchemaFlexible: defaultSchemaFlexible,
	}
}

func CreateBigQueryExporterFunc(
	ctx context.Context,
	settings exporter.Settings,
	config component.Config,
) (exporter.Traces, error) {
	if config == nil {
		return nil, errors.New("exporter configuration required")
	}

	cfg := config.(*Config)
	exporter, err := newRowsExporter(cfg, settings)
	if err != nil {
		return nil, err
	}

	return exporter, nil
}
