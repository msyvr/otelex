package bigquery

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"go.opentelemetry.io/collector/config/configretry"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

/*
Do not exceed BigQuery API request rate quota + data volume ingest rate limit.

The example export target is a BigQuery table. Export via the BQ legacy streaming API:
https://cloud.google.com/bigquery/docs/streaming-data-into-bigquery
Quota limits for streaming inserts:
https://cloud.google.com/bigquery/quotas#streaming_inserts
Tip: Consider using the Streaming inserts API (improved cost-efficiency):
https://cloud.google.com/bigquery/docs/samples/bigquery-table-insert-rows

Estimate optimal batch size -

Rows are batched with consideration for:
1. API call rate limit, and 2. data ingest/insert rate limit.
Specs (BQ quota values - verify vs. current values at link above):
-> BQ ingest rate limit: 1 GB/s (per region in the US and EU, for all BQ tables)
-> BQ maximum row size: 10 MB
-> BQ HTTP request size limit: 10 MB
-> BQ maximum rows per request: 50,000 rows (500 recommended, depending on row size)

Batch size determination:
-> Example data row is assumed to be ~1 kB.
-> Without batching or throttling, the API call rate could reach 10^6 /s.
-> With batching of 8196 spans/batch (<10 MB) -> API call rate <200 /s.

! It's useful to batch early in the pipeline. Also, if there's a need to tune batching,
it's less of a lift to do so in the config used at deploy (no need to rebuild the
Collector distribution binary). So, batching will be set in /builders/otelcol-config.yaml.
*/

// Partitioning the table into single days is useful for efficient queries.
const tablePartitionFieldKey = "ts"

func TunedQueueSettings() exporterhelper.QueueBatchConfig {
	return exporterhelper.QueueBatchConfig{
		Enabled: true,
	}
}

func TunedRetrySettings() configretry.BackOffConfig {
	return configretry.BackOffConfig{
		Enabled:         true,
		InitialInterval: 60 * time.Second,
		MaxInterval:     60 * time.Second,
		MaxElapsedTime:  5 * time.Minute,
	}
}

func TunedTimeoutSettings() exporterhelper.TimeoutConfig {
	// Long-ish, to accommodate (occasional) target table schema updates.
	return exporterhelper.TimeoutConfig{
		Timeout: 120 * time.Second,
	}
}

type bigquerySender struct {
	*Config
	bigqueryClient *bigquery.Client
}

func newBigQuerySender(cfg *Config) (*bigquerySender, error) {
	client, err := bigquery.NewClient(context.Background(), cfg.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("create bigquery client: %w", err)
	}
	defer client.Close()

	sender := &bigquerySender{
		Config:         cfg,
		bigqueryClient: client,
	}

	return sender, nil
}

func newRowsExporter(cfg *Config, settings exporter.Settings) (exporter.Traces, error) {
	sender, err := newBigQuerySender(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create traces exporter: %w", err)
	}

	return exporterhelper.NewTraces(
		context.Background(),
		settings,
		cfg,
		sender.consumeTraces,
		exporterhelper.WithQueue(TunedQueueSettings()),
		exporterhelper.WithRetry(TunedRetrySettings()),
		exporterhelper.WithTimeout(TunedTimeoutSettings()),
	)
}

func (s *bigquerySender) consumeTraces(ctx context.Context, td ptrace.Traces) error {
	rows := buildRows(td)
	err := s.sendRows(ctx, rows)
	if err != nil {
		fmt.Printf("Error pushing traces: %v\n", err)
	}
	return err
}

func (sender *bigquerySender) sendRows(ctx context.Context, rows []bigqueryrow) error {
	table := sender.bigqueryClient.Dataset(sender.Dataset).Table(sender.Table)
	err := table.Inserter().Put(ctx, rows)
	if err != nil && strings.Contains(err.Error(), "no such field") {
		// When a span attribute key is not represented in the schema, it will
		// be updated if the exporter is configured to have a flexible schema.
		// New fields cannot be REQUIRED fields. Existing table rows will have
		// a NULL value in new field(s).
		if sender.SchemaFlexible {
			err := sender.updateSchema(ctx, table, rows)
			if err != nil {
				return err
			}

			// Avoid failed inserts with an enforced delay after schema updates.
			// Typically, it's best practice to have a fixed schema, so this won't
			// come up in those cases. This delay accommodates the (nominally
			// exceptional) case where schema alterations occur on-the-fly.
			const wait = 60 * time.Second
			fmt.Printf("Waiting %v to allow schema updates to register fully", wait)
			time.Sleep(wait)

			// table.Inserter().Put() does not skipInvalidRows. If any row fails,
			// the entire batch will fail. In that case, retry the full batch.
			fmt.Println("Retrying insert")
			return table.Inserter().Put(ctx, rows)
		}
	}
	return err
}

// Attempt to update the target table schema when new fields are identified.
// If no BigQuery type maps to the span value type, block the export.
func (s *bigquerySender) updateSchema(ctx context.Context, table *bigquery.Table, rows []bigqueryrow) error {
	// If data contains field(s) not present in the target table schema, update the schema using the first
	// matching type for each. If the update is unsuccessful for any fields in a trace, the table will reject
	// the entire trace aka data row.
	meta, err := table.Metadata(ctx)
	if err != nil {
		return fmt.Errorf("table metadata: %w", err)
	}

	knownFields := make(map[string]bool, len(meta.Schema))
	knownFieldsTypes := make(map[string]string, len(meta.Schema))

	for _, field := range meta.Schema {
		knownFields[field.Name] = true
		switch field.Type {
		case bigquery.BigNumericFieldType:
			knownFieldsTypes[field.Name] = "float64"
		case bigquery.BooleanFieldType:
			knownFieldsTypes[field.Name] = "bool"
		case bigquery.BytesFieldType:
			knownFieldsTypes[field.Name] = "byte"
		case bigquery.NumericFieldType:
			knownFieldsTypes[field.Name] = "int64"
		case bigquery.StringFieldType:
			knownFieldsTypes[field.Name] = "string"
		case bigquery.TimestampFieldType:
			knownFieldsTypes[field.Name] = "int64"
		default:
			return fmt.Errorf("BigQuery field type %v incompatible with span attribute value types", field.Type)
		}
	}
	newFields := make(map[string]bool)
	metaUpdate := bigquery.TableMetadataToUpdate{
		Schema: meta.Schema,
	}

	for _, row := range rows {
		for key, value := range row {
			valueType := reflect.TypeOf(value).String()

			if knownFields[key] {
				// TODO Improve handling of fields with duplicate names but
				// different value types.
				if knownFieldsTypes[key] != valueType {
					fmt.Printf("Schema field %v doesn't map to span value type %v. Export may fail.\n", key, reflect.TypeOf(value))
				}
			}

			if !knownFields[key] {
				// OTel span attribute value types are limited to these cases.
				// Conveniently, they each map to a BigQuery type.
				var fieldType bigquery.FieldType
				if key == "ts" {
					fieldType = bigquery.TimestampFieldType
				} else {
					switch value.(type) {
					case bool:
						fieldType = bigquery.BooleanFieldType
					case byte:
						fieldType = bigquery.BytesFieldType
					case float64:
						fieldType = bigquery.BigNumericFieldType
					case int64:
						fieldType = bigquery.NumericFieldType
					case string:
						fieldType = bigquery.StringFieldType

					default:
						fmt.Printf("Schema update attempted: %v has unsupported type: %v.\n", key, reflect.TypeOf(value))
					}
				}
				fmt.Printf("Updating schema with field '%v' of type %v\n", key, fieldType)
				metaUpdate.Schema = append(metaUpdate.Schema, &bigquery.FieldSchema{
					Name: key,
					Type: fieldType,
				})
				knownFields[key] = true
				knownFieldsTypes[key] = valueType
				newFields[key] = true
			}
		}
	}

	if len(newFields) == 0 {
		// This case may arise when there are no new fields relative to a previously processed row (span),
		// but at least some of the (recently) updated schema fields have not yet registered with BigQuery.
		// No action required.
	} else {
		fmt.Printf("Updating schema with %d new fields\n", len(newFields))
		_, err = table.Update(ctx, metaUpdate, meta.ETag)
		if err != nil {
			return fmt.Errorf("unable to update schema: %w", err)
		}
	}

	return nil
}
