package bigquery

import (
	"errors"
)

type Config struct {
	ProjectID string `mapstructure:"projectID"`
	Dataset   string `mapstructure:"dataset"`
	Table     string `mapstructure:"table"`

	SchemaFlexible bool
}

// The BigQuery API requires these fields. Export will fail otherwise.
func (cfg *Config) Validate() error {
	if cfg.ProjectID == "" {
		return errors.New("projectID required for BigQuery API")
	}

	if cfg.Dataset == "" {
		return errors.New("dataset required for BigQuery API")
	}

	if cfg.Table == "" {
		return errors.New("table required for BigQuery API")
	}
	return nil
}
