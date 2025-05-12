package bigquery

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
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

Streaming data rows (OTel spans) are batched with consideration for:
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
*/

// Partitioning the table into single days is useful for efficient queries.
const tablePartitionFieldKey = "ts"

// The exporterhelper package enables queued retry capabilities.
// https://github.com/open-telemetry/opentelemetry-collector/blob/v0.125.0/exporter/exporterhelper/README.md

func TunedQueueSettings() exporterhelper.QueueSettings {
	return exporterhelper.QueueSettings{
		Enabled: true,
	}
}

func TunedRetrySettings() exporterhelper.RetrySettings {
	return exporterhelper.RetrySettings{
		Enabled:         true,
		InitialInterval: 60 * time.Second,
		MaxInterval:     60 * time.Second,
		MaxElapsedTime:  5 * time.Minute,
	}
}

func TunedTimeoutSettings() exporterhelper.TimeoutSettings {
	// Handle delays caused by (occasional) schema updates.
	return exporterhelper.TimeoutSettings{
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

	return exporterhelper.NewTracesExporter(
		context.Background(),
		settings,
		cfg,
		sender.sendRows,
		exporterhelper.WithQueue(TunedQueueSettings()),
		exporterhelper.WithRetry(TunedRetrySettings()),
		exporterhelper.WithTimeout(TunedTimeoutSettings()),
	)
}

func (s *bigquerySender) sendRows(ctx context.Context, td ptrace.Traces) error {
	rows := buildTraceRows(td)
	err := s.insertRows(ctx, rows)
	if err != nil {
		fmt.Printf("Error pushing traces: %v\n", err)
	}
	return err
}

func (sender *bigquerySender) insertRows(ctx context.Context, rows []bqrow) error {
	table := sender.bigqueryClient.Dataset(sender.Dataset).Table(sender.Table)
	err := table.Inserter().Put(ctx, rows)
	if err != nil && strings.Contains(err.Error(), "no such field") {
		err := sender.updateSchema(ctx, table, rows)
		if err != nil {
			return err
		}

		// Avoid failed inserts with an enforced delay after schema updates.
		// Typically, it's best practice to have a fixed schema, so this won't
		// come up in those cases. This delay accommodates the (ideally, exceptional)
		// case where schema alterations occur on-the-fly.
		const wait = 1 * time.Minute
		fmt.Printf("Waiting %v to allow schema updates to register fully", wait)
		time.Sleep(wait)

		// table.Inserter().Put() does not set skipInvalidRows to true. If any
		// row fails, the entire batch will fail. In that case, retry the full batch.
		// TODO This deserves analysis to evaluate whether a single retry is
		// sufficient.
		fmt.Println("Retrying insert")
		return table.Inserter().Put(ctx, rows)
	}
	return err
}

// Attempt to update the target table schema when new fields are identified.
// If no BigQuery type maps to the span value type, block the export.
func (s *bigquerySender) updateSchema(ctx context.Context, table *bigquery.Table, rows []bqrow) error {
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
		case bigquery.BooleanFieldType:
			knownFieldsTypes[field.Name] = "bool"
		case bigquery.BigNumericFieldType:
			knownFieldsTypes[field.Name] = "float64"
		case bigquery.NumericFieldType:
			knownFieldsTypes[field.Name] = "int64"
		case bigquery.StringFieldType:
			knownFieldsTypes[field.Name] = "string"
		case bigquery.BytesFieldType:
			knownFieldsTypes[field.Name] = "byte"
		case bigquery.TimestampFieldType:
			knownFieldsTypes[field.Name] = "int64"
		default:
			return fmt.Errorf("bigquery field %v has type not permitted as OTel span attribute: %v", field.Name, field.Type)
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
				// TODO Improve handling of fields with duplicate names and different types.
				if knownFieldsTypes[key] != valueType {
					fmt.Printf("Existing field %v is not of type: %v. Export may fail.\n", key, reflect.TypeOf(value))
				}
			}

			if !knownFields[key] {
				// OTel span attributes must be of these specific types (cases).
				// Conveniently, these each map to a BigQuery type.
				var fieldType bigquery.FieldType
				if key == "ts" {
					fieldType = bigquery.TimestampFieldType
				} else {
					switch value.(type) {
					case bool:
						fieldType = bigquery.BooleanFieldType
					case float64:
						fieldType = bigquery.BigNumericFieldType
					case int64:
						fieldType = bigquery.NumericFieldType
					case string:
						fieldType = bigquery.StringFieldType
					case byte:
						fieldType = bigquery.BytesFieldType
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
