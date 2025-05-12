package bigquery

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testProjectID      = "msyvr"
	testDataset        = "otelex"
	testTable          = "spattex_test"
	testSchemaFlexible = false
)

func createTestConfig() *Config {
	return &Config{
		ProjectID:      testProjectID,
		Dataset:        testDataset,
		Table:          testTable,
		SchemaFlexible: testSchemaFlexible,
	}
}
func TestValidateConfig(t *testing.T) {
	cfg := createTestConfig()
	err := cfg.Validate()
	require.NoError(t, err, "test config validation should not fail")
}
