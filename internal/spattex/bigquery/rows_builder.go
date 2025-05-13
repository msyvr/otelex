package bigquery

import (
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// Enable row insertion into a BigQuery table by formatting each row
// as a map, with keys matching the table schema fields. A batch of
// rows may be inserted in one API call by creating an array of row-maps.
type bigqueryrow map[string]interface{}

// The OpenTelemetry ptrace.Traces type has a defined nested structure.
// Navigate to the nest level of span attributes to extract those for the map.
func buildRows(td ptrace.Traces) []bigqueryrow {
	var rows []bigqueryrow
	rspans := td.ResourceSpans()
	for i := 0; i < rspans.Len(); i++ {
		rspan := rspans.At(i)
		sspans := rspan.ScopeSpans()
		for j := 0; j < sspans.Len(); j++ {
			sspan := sspans.At(j)
			spans := sspan.Spans()
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)
				row := bigqueryrow{
					"name": span.Name(),
				}
				// Span attributes exist at both the 'resource' (i.e., parent trace) level
				// and at the individual span level.
				rspan.Resource().Attributes().Range(func(k string, v pcommon.Value) bool {
					row.addKeyValue(k, v)
					return true
				})
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

// Parse key value pairs to align with field name preferences
// and BigQuery type equivalents for span attribute value types.
func (row bigqueryrow) addKeyValue(k string, v pcommon.Value) {
	// Names with periods are inconvenient for SQL.
	k = strings.Replace(k, ".", "_", -1)
	// BigQuery types vs OTel span attribute types.
	// https://pkg.go.dev/cloud.google.com/go/bigquery#Table.Metadata
	// https://github.com/googleapis/google-cloud-go/blob/ed488b94b46b50585f91e065dd877c06d85ce879/bigquery/value.go#L32
	// https://opentelemetry.io/docs/concepts/signals/traces/#attributes
	switch v.Type() {
	case pcommon.ValueTypeBool:
		row[k] = v.Bool()
	case pcommon.ValueTypeBytes:
		row[k] = v.Bytes()
	case pcommon.ValueTypeDouble:
		row[k] = v.Double()
	case pcommon.ValueTypeInt:
		row[k] = v.Int()
	case pcommon.ValueTypeMap:
		row[k] = v.Map()
	case pcommon.ValueTypeSlice:
		row[k] = v.Slice()
	case pcommon.ValueTypeStr:
		row[k] = v.Str()
	}
}
