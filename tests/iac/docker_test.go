package iac_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func projectRoot() string {
	_, file, _, _ := runtime.Caller(0)
	// tests/iac/docker_test.go → go up 2 dirs
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func TestDockerfileExists(t *testing.T) {
	path := filepath.Join(projectRoot(), "Dockerfile")
	_, err := os.Stat(path)
	require.NoError(t, err, "Dockerfile must exist at project root")
}

func TestDockerComposeExists(t *testing.T) {
	path := filepath.Join(projectRoot(), "docker-compose.yml")
	_, err := os.Stat(path)
	require.NoError(t, err, "docker-compose.yml must exist at project root")
}

func TestDockerComposeServices(t *testing.T) {
	path := filepath.Join(projectRoot(), "docker-compose.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var compose struct {
		Services map[string]interface{} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(data, &compose))

	required := []string{"wrapper", "redis", "postgres", "qdrant", "ollama"}
	for _, svc := range required {
		assert.Contains(t, compose.Services, svc, "service %q must be defined", svc)
	}
}

func TestDockerComposeHealthChecks(t *testing.T) {
	path := filepath.Join(projectRoot(), "docker-compose.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var compose struct {
		Services map[string]struct {
			Healthcheck map[string]interface{} `yaml:"healthcheck"`
		} `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(data, &compose))

	for _, svc := range []string{"redis", "postgres"} {
		assert.NotNil(t, compose.Services[svc].Healthcheck, "service %q must have healthcheck", svc)
	}
}
