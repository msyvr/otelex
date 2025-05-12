package bigquery

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

type bqrow map[string]interface{}

func buildTraceRows(td ptrace.Traces) []bqrow {
	var rows []bqrow
	rspans := td.ResourceSpans()
	for i := 0; i < rspans.Len(); i++ {
		rspan := rspans.At(i)
		sspans := rspan.ScopeSpans()
		for j := 0; j < sspans.Len(); j++ {
			sspan := sspans.At(j)
			spans := sspan.Spans()
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)
				row := bqrow{
					"name": span.Name(),
					"type": "trace",
				}
				rspan.Resource().Attributes().Range(func(k string, v pcommon.Value) bool {
					row.addKeyValue(k, v)
					return true
				})
				// Add span-level attributes
				span.Attributes().Range(func(k string, v pcommon.Value) bool {
					row.addKeyValue(k, v)
					return true
				})
				rows = append(rows, row)
			}
		}
	}

	return rows
}

func (row bqrow) addKeyValue(k string, v pcommon.Value) {
	// Names with periods are inconvenient in SQL, replace with underscore
	k = strings.Replace(k, ".", "_", -1)
	// BigQuery types vs OTel span attribute types.
	// https://pkg.go.dev/cloud.google.com/go/bigquery@v1.50.0#Table.Metadata
	// https://github.com/googleapis/google-cloud-go/blob/bigquery/v1.57.1/bigquery/value.go#L33
	// https://opentelemetry.io/docs/concepts/signals/traces/#attributes
	switch v.Type() {
	case pcommon.ValueTypeBool:
		row[k] = v.Bool()
	case pcommon.ValueTypeDouble:
		row[k] = v.Double()
	case pcommon.ValueTypeInt:
		row[k] = v.Int()
	case pcommon.ValueTypeStr:
		row[k] = v.Str()
	case pcommon.ValueTypeMap:
		row[k] = v.Map()
	case pcommon.ValueTypeSlice:
		row[k] = v.Slice()
	case pcommon.ValueTypeBytes:
		row[k] = v.Bytes()
	}
}
