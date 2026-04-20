package iac_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPgBouncer_ChartYamlExists(t *testing.T) {
	path := filepath.Join(helmDir(), "pgbouncer", "Chart.yaml")
	_, err := os.Stat(path)
	require.NoError(t, err, "pgbouncer Chart.yaml must exist")
}

func TestPgBouncer_ValuesYamlExists(t *testing.T) {
	path := filepath.Join(helmDir(), "pgbouncer", "values.yaml")
	_, err := os.Stat(path)
	require.NoError(t, err, "pgbouncer values.yaml must exist")
}

func TestPgBouncer_ConfigMapTemplateExists(t *testing.T) {
	path := filepath.Join(helmDir(), "pgbouncer", "templates", "configmap.yaml")
	_, err := os.Stat(path)
	require.NoError(t, err, "pgbouncer templates/configmap.yaml must exist")
}

func TestPgBouncer_DeploymentTemplateExists(t *testing.T) {
	path := filepath.Join(helmDir(), "pgbouncer", "templates", "deployment.yaml")
	_, err := os.Stat(path)
	require.NoError(t, err, "pgbouncer templates/deployment.yaml must exist")
}

func TestPgBouncer_ValuesHasPoolMode(t *testing.T) {
	path := filepath.Join(helmDir(), "pgbouncer", "values.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var values struct {
		PgBouncer struct {
			PoolMode string `yaml:"poolMode"`
		} `yaml:"pgbouncer"`
	}
	require.NoError(t, yaml.Unmarshal(data, &values))

	assert.Equal(t, "transaction", values.PgBouncer.PoolMode, "pgbouncer.poolMode must be transaction")
}

func TestPgBouncer_MaxClientConn100(t *testing.T) {
	path := filepath.Join(helmDir(), "pgbouncer", "values.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var values struct {
		PgBouncer struct {
			MaxClientConn int `yaml:"maxClientConn"`
		} `yaml:"pgbouncer"`
	}
	require.NoError(t, yaml.Unmarshal(data, &values))

	assert.Equal(t, 100, values.PgBouncer.MaxClientConn, "pgbouncer.maxClientConn must be 100")
}

func TestPgBouncer_DefaultPoolSize25(t *testing.T) {
	path := filepath.Join(helmDir(), "pgbouncer", "values.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var values struct {
		PgBouncer struct {
			DefaultPoolSize int `yaml:"defaultPoolSize"`
		} `yaml:"pgbouncer"`
	}
	require.NoError(t, yaml.Unmarshal(data, &values))

	assert.Equal(t, 25, values.PgBouncer.DefaultPoolSize, "pgbouncer.defaultPoolSize must be 25")
}

func TestPgBouncer_ThresholdAnnotationPresent(t *testing.T) {
	path := filepath.Join(helmDir(), "pgbouncer", "values.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var values struct {
		Annotations struct {
			Threshold   string `yaml:"pg_stat_activity_threshold"`
			Description string `yaml:"description"`
		} `yaml:"annotations"`
	}
	require.NoError(t, yaml.Unmarshal(data, &values))

	assert.Equal(t, "50", values.Annotations.Threshold, "annotations.pg_stat_activity_threshold must be 50")
	assert.NotEmpty(t, values.Annotations.Description, "annotations.description must be present")
}

func TestPgBouncer_PostgresPortIs5433(t *testing.T) {
	path := filepath.Join(helmDir(), "pgbouncer", "values.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var values struct {
		PgBouncer struct {
			Postgres struct {
				Port int `yaml:"port"`
			} `yaml:"postgres"`
		} `yaml:"pgbouncer"`
	}
	require.NoError(t, yaml.Unmarshal(data, &values))

	assert.Equal(t, 5433, values.PgBouncer.Postgres.Port, "pgbouncer.postgres.port must be 5433 (raw postgres)")
}

func TestPgBouncer_ServicePortIs5432(t *testing.T) {
	path := filepath.Join(helmDir(), "pgbouncer", "values.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var values struct {
		Service struct {
			Port int `yaml:"port"`
		} `yaml:"service"`
	}
	require.NoError(t, yaml.Unmarshal(data, &values))

	assert.Equal(t, 5432, values.Service.Port, "service.port must be 5432 (pgbouncer proxy)")
}
