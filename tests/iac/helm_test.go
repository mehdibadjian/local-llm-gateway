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

// Qdrant Distributed Mode Tests
// US-40: Qdrant distributed mode migration path for >1M chunks

func TestQdrantDistributedChartExists(t *testing.T) {
	path := filepath.Join(helmDir(), "qdrant-distributed", "Chart.yaml")
	_, err := os.Stat(path)
	require.NoError(t, err, "qdrant-distributed Chart.yaml must exist")
}

func TestQdrantDistributedValues_ReplicationFactor(t *testing.T) {
	path := filepath.Join(helmDir(), "qdrant-distributed", "values.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var values struct {
		DistributedMode struct {
			Enabled             bool `yaml:"enabled"`
			ReplicationFactor   int  `yaml:"replicationFactor"`
			ConsensusThreadsCount int `yaml:"consensusThreadsCount"`
		} `yaml:"distributedMode"`
	}
	require.NoError(t, yaml.Unmarshal(data, &values))

	assert.True(t, values.DistributedMode.Enabled, "distributedMode.enabled must be true")
	assert.Equal(t, 2, values.DistributedMode.ReplicationFactor, "distributedMode.replicationFactor must be 2")
	assert.Equal(t, 4, values.DistributedMode.ConsensusThreadsCount, "distributedMode.consensusThreadsCount must be 4")
}

func TestQdrantDistributedValues_Persistence(t *testing.T) {
	path := filepath.Join(helmDir(), "qdrant-distributed", "values.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var values struct {
		Persistence struct {
			Enabled bool   `yaml:"enabled"`
			Size    string `yaml:"size"`
		} `yaml:"persistence"`
	}
	require.NoError(t, yaml.Unmarshal(data, &values))

	assert.True(t, values.Persistence.Enabled, "persistence.enabled must be true")
	assert.NotEmpty(t, values.Persistence.Size, "persistence.size must be set")
}

func TestQdrantDistributedValues_ResourceLimits(t *testing.T) {
	path := filepath.Join(helmDir(), "qdrant-distributed", "values.yaml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var values struct {
		Resources struct {
			Requests struct {
				Memory string `yaml:"memory"`
				CPU    string `yaml:"cpu"`
			} `yaml:"requests"`
			Limits struct {
				Memory string `yaml:"memory"`
				CPU    string `yaml:"cpu"`
			} `yaml:"limits"`
		} `yaml:"resources"`
	}
	require.NoError(t, yaml.Unmarshal(data, &values))

	assert.Equal(t, "1Gi", values.Resources.Requests.Memory, "resources.requests.memory must be 1Gi")
	assert.Equal(t, "2Gi", values.Resources.Limits.Memory, "resources.limits.memory must be 2Gi")
}

func TestQdrantDistributedStatefulSet_Exists(t *testing.T) {
	path := filepath.Join(helmDir(), "qdrant-distributed", "templates", "statefulset.yaml")
	_, err := os.Stat(path)
	require.NoError(t, err, "qdrant-distributed StatefulSet template must exist")
}

func TestQdrantDistributedService_Exists(t *testing.T) {
	path := filepath.Join(helmDir(), "qdrant-distributed", "templates", "service.yaml")
	_, err := os.Stat(path)
	require.NoError(t, err, "qdrant-distributed Service template must exist")
}
