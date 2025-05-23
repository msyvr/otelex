package bigquery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestBuildRows(t *testing.T) {
	// Create a test trace with known values
	traces := createTestTraces()

	// Build rows from the trace
	rows := buildRows(traces)

	// Validate the results
	assert.Equal(t, 2, len(rows), "Should have created 2 rows")

	// Verify first row
	assert.Equal(t, "span1", rows[0]["name"], "First row name should be 'span1'")
	assert.Equal(t, "service1", rows[0]["service_name"], "Resource attribute should be properly added")
	assert.Equal(t, "value1", rows[0]["str_key"], "Span attribute should be properly added")
	assert.Equal(t, int64(41), rows[0]["int_key"], "Int attribute should be properly added")

	// Verify second row
	assert.Equal(t, "span2", rows[1]["name"], "Second row name should be 'span2'")
	assert.Equal(t, "service1", rows[1]["service_name"], "Resource attribute should be properly added")
	assert.Equal(t, "value2", rows[1]["str_key"], "Span attribute should be properly added")
	assert.Equal(t, 3.24, rows[1]["double_key"], "Double attribute should be properly added")
}

func TestAddKeyValue(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    func() pcommon.Value
		expected interface{}
	}{
		{
			name: "string value",
			key:  "str_key",
			value: func() pcommon.Value {
				v := pcommon.NewValueEmpty()
				v.SetStr("string_value")
				return v
			},
			expected: "string_value",
		},
		{
			name: "int value",
			key:  "int_key",
			value: func() pcommon.Value {
				v := pcommon.NewValueEmpty()
				v.SetInt(123)
				return v
			},
			expected: int64(123),
		},
		{
			name: "double value",
			key:  "double_key",
			value: func() pcommon.Value {
				v := pcommon.NewValueEmpty()
				v.SetDouble(1.23)
				return v
			},
			expected: 1.23,
		},
		{
			name: "bool value",
			key:  "bool_key",
			value: func() pcommon.Value {
				v := pcommon.NewValueEmpty()
				v.SetBool(true)
				return v
			},
			expected: true,
		},
		{
			name: "replace dots in key",
			key:  "service.name",
			value: func() pcommon.Value {
				v := pcommon.NewValueEmpty()
				v.SetStr("test_service")
				return v
			},
			expected: "test_service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := bigqueryrow{}
			row.addKeyValue(tt.key, tt.value())

			// For keys with dots, check the transformed key
			key := tt.key
			if key == "service.name" {
				key = "service_name"
			}

			assert.Equal(t, tt.expected, row[key])
		})
	}
}

func TestAddKeyValueComplexTypes(t *testing.T) {
	// Test map value
	t.Run("map value", func(t *testing.T) {
		row := bigqueryrow{}
		val := pcommon.NewValueEmpty()
		m := val.SetEmptyMap()
		m.PutStr("nested_key", "nested_value")

		row.addKeyValue("map_key", val)

		// Since Map() returns the internal representation, we just check that it exists
		assert.NotNil(t, row["map_key"])
	})

	// Test slice value
	t.Run("slice value", func(t *testing.T) {
		row := bigqueryrow{}
		val := pcommon.NewValueEmpty()
		s := val.SetEmptySlice()
		s.AppendEmpty().SetStr("item1")
		s.AppendEmpty().SetStr("item2")

		row.addKeyValue("slice_key", val)

		// Since Slice() returns the internal representation, we just check that it exists
		assert.NotNil(t, row["slice_key"])
	})

	// Test bytes value
	t.Run("bytes value", func(t *testing.T) {
		row := bigqueryrow{}
		val := pcommon.NewValueEmpty()
		val.SetEmptyBytes().FromRaw([]byte("test bytes"))

		row.addKeyValue("bytes_key", val)

		// We check that bytes were properly added
		assert.NotNil(t, row["bytes_key"])
	})
}

// Helper function to create test traces with predictable data
func createTestTraces() ptrace.Traces {
	traces := ptrace.NewTraces()

	// Add a resource span
	rs := traces.ResourceSpans().AppendEmpty()

	// Add resource attributes
	rs.Resource().Attributes().PutStr("service.name", "service1")
	rs.Resource().Attributes().PutInt("resource.id", 1001)

	// Add a scope span
	ss := rs.ScopeSpans().AppendEmpty()
	ss.Scope().SetName("test_scope")

	// Add first span
	span1 := ss.Spans().AppendEmpty()
	span1.SetName("span1")
	span1.Attributes().PutStr("str_key", "value1")
	span1.Attributes().PutInt("int_key", 41)
	span1.Attributes().PutDouble("double_key", 3.14)

	// Add second span
	span2 := ss.Spans().AppendEmpty()
	span2.SetName("span2")
	span2.Attributes().PutStr("str_key", "value2")
	span2.Attributes().PutInt("int_key", 42)
	span2.Attributes().PutDouble("double_key", 3.24)

	return traces
}

func TestEmptyTraces(t *testing.T) {
	// Test with empty traces
	traces := ptrace.NewTraces()
	rows := buildRows(traces)

	assert.Equal(t, 0, len(rows), "Empty traces should produce no rows")
}

func TestMultipleResourceSpans(t *testing.T) {
	traces := ptrace.NewTraces()

	// Add first resource span
	rs1 := traces.ResourceSpans().AppendEmpty()
	rs1.Resource().Attributes().PutStr("service.name", "service1")
	ss1 := rs1.ScopeSpans().AppendEmpty()
	span1 := ss1.Spans().AppendEmpty()
	span1.SetName("span1")

	// Add second resource span
	rs2 := traces.ResourceSpans().AppendEmpty()
	rs2.Resource().Attributes().PutStr("service.name", "service2")
	ss2 := rs2.ScopeSpans().AppendEmpty()
	span2 := ss2.Spans().AppendEmpty()
	span2.SetName("span2")

	rows := buildRows(traces)

	assert.Equal(t, 2, len(rows), "Should have 2 rows")
	assert.Equal(t, "service1", rows[0]["service_name"], "First row should have service1")
	assert.Equal(t, "service2", rows[1]["service_name"], "Second row should have service2")
}

func TestMultipleScopeSpans(t *testing.T) {
	traces := ptrace.NewTraces()

	// Add a resource span with multiple scope spans
	rs := traces.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "service1")

	// Add first scope span
	ss1 := rs.ScopeSpans().AppendEmpty()
	ss1.Scope().SetName("scope1")
	span1 := ss1.Spans().AppendEmpty()
	span1.SetName("span1")

	// Add second scope span
	ss2 := rs.ScopeSpans().AppendEmpty()
	ss2.Scope().SetName("scope2")
	span2 := ss2.Spans().AppendEmpty()
	span2.SetName("span2")

	rows := buildRows(traces)

	assert.Equal(t, 2, len(rows), "Should have 2 rows")
	assert.Equal(t, "span1", rows[0]["name"], "First row should be span1")
	assert.Equal(t, "span2", rows[1]["name"], "Second row should be span2")
	assert.Equal(t, "service1", rows[0]["service_name"], "Both rows should have the same resource attributes")
	assert.Equal(t, "service1", rows[1]["service_name"], "Both rows should have the same resource attributes")
}
