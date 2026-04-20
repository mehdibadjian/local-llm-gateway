package iac_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func helmDir() string {
	return filepath.Join(projectRoot(), "deploy", "helm")
}

func kedaDir() string {
	return filepath.Join(projectRoot(), "deploy", "keda")
}

func TestHelmChartExists_CawWrapper(t *testing.T) {
	path := filepath.Join(helmDir(), "caw-wrapper", "Chart.yaml")
	_, err := os.Stat(path)
	require.NoError(t, err, "caw-wrapper Chart.yaml must exist")
}

func TestHelmValues_EmbedServiceMemory(t *testing.T) {
	path := filepath.Join(helmDir(), "embed-service", "values.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var values struct {
		Resources struct {
			Requests struct {
				Memory string `yaml:"memory"`
			} `yaml:"requests"`
			Limits struct {
				Memory string `yaml:"memory"`
			} `yaml:"limits"`
		} `yaml:"resources"`
	}
	require.NoError(t, yaml.Unmarshal(data, &values))

	assert.Equal(t, "200Mi", values.Resources.Requests.Memory, "embed-service requests.memory must be 200Mi")
	assert.Equal(t, "512Mi", values.Resources.Limits.Memory, "embed-service limits.memory must be 512Mi")
}

func TestKEDAScaledObjectsExist(t *testing.T) {
	files := []string{
		"wrapper-scaledobject.yaml",
		"ingest-worker-scaledobject.yaml",
		"inference-backend-scaledobject.yaml",
	}
	for _, f := range files {
		path := filepath.Join(kedaDir(), f)
		_, err := os.Stat(path)
		assert.NoError(t, err, "KEDA file %q must exist", f)
	}
}

func TestKEDAWrapperFallback(t *testing.T) {
	path := filepath.Join(kedaDir(), "wrapper-scaledobject.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var so struct {
		Spec struct {
			Fallback struct {
				Replicas         int `yaml:"replicas"`
				FailureThreshold int `yaml:"failureThreshold"`
			} `yaml:"fallback"`
		} `yaml:"spec"`
	}
	require.NoError(t, yaml.Unmarshal(data, &so))

	assert.Equal(t, 3, so.Spec.Fallback.Replicas, "wrapper ScaledObject fallback.replicas must be 3")
	assert.Equal(t, 3, so.Spec.Fallback.FailureThreshold, "wrapper ScaledObject fallback.failureThreshold must be 3")
}

func TestKEDAIngestPendingEntries(t *testing.T) {
	path := filepath.Join(kedaDir(), "ingest-worker-scaledobject.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var so struct {
		Spec struct {
			Triggers []struct {
				Metadata map[string]string `yaml:"metadata"`
			} `yaml:"triggers"`
		} `yaml:"spec"`
	}
	require.NoError(t, yaml.Unmarshal(data, &so))

	require.NotEmpty(t, so.Spec.Triggers)
	assert.Equal(t, "50", so.Spec.Triggers[0].Metadata["pendingEntriesCount"],
		"ingest ScaledObject pendingEntriesCount must be \"50\"")
}

func TestKEDAInferenceMinReplica(t *testing.T) {
	path := filepath.Join(kedaDir(), "inference-backend-scaledobject.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var so struct {
		Spec struct {
			MinReplicaCount int `yaml:"minReplicaCount"`
		} `yaml:"spec"`
	}
	require.NoError(t, yaml.Unmarshal(data, &so))

	assert.Equal(t, 1, so.Spec.MinReplicaCount, "inference-backend ScaledObject minReplicaCount must be 1")
}
