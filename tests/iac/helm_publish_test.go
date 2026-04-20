package iac_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// US-44: Public Helm chart registry publication via GitHub Actions

// Acceptance Criterion 1 & 6: Workflow file exists and has helm-v* tag trigger
func TestHelmPublish_WorkflowExists(t *testing.T) {
	workflowPath := filepath.Join(projectRoot(), ".github", "workflows", "helm-publish.yml")
	_, err := os.Stat(workflowPath)
	require.NoError(t, err, "helm-publish.yml workflow must exist at .github/workflows/helm-publish.yml")
}

// Acceptance Criterion 6: Workflow YAML has on.push.tags trigger matching helm-v*
func TestHelmPublish_WorkflowHasTagTrigger(t *testing.T) {
	workflowPath := filepath.Join(projectRoot(), ".github", "workflows", "helm-publish.yml")
	data, err := os.ReadFile(workflowPath)
	require.NoError(t, err)

	var workflow struct {
		On struct {
			Push struct {
				Tags []string `yaml:"tags"`
			} `yaml:"push"`
		} `yaml:"on"`
	}
	require.NoError(t, yaml.Unmarshal(data, &workflow))

	require.NotEmpty(t, workflow.On.Push.Tags, "workflow must have push.tags trigger")
	assert.Contains(t, workflow.On.Push.Tags, "helm-v*",
		"workflow must have tag trigger matching 'helm-v*'")
}

// Acceptance Criterion 4: Workflow uses GITHUB_TOKEN not hardcoded credentials
func TestHelmPublish_WorkflowUsesGithubToken(t *testing.T) {
	workflowPath := filepath.Join(projectRoot(), ".github", "workflows", "helm-publish.yml")
	data, err := os.ReadFile(workflowPath)
	require.NoError(t, err)

	workflowYAML := string(data)

	// Must reference secrets.GITHUB_TOKEN or github.token
	require.True(t,
		contains(workflowYAML, "secrets.GITHUB_TOKEN") || contains(workflowYAML, "github.token"),
		"workflow must use ${{ secrets.GITHUB_TOKEN }} or ${{ github.token }}, not hardcoded credentials")

	// Must NOT contain hardcoded credentials
	assert.NotContains(t, workflowYAML, "password:", "must not hardcode passwords in workflow")
}

// Acceptance Criterion 2: Workflow uses helm/chart-releaser-action OR helm package + helm push
func TestHelmPublish_WorkflowHasPushStep(t *testing.T) {
	workflowPath := filepath.Join(projectRoot(), ".github", "workflows", "helm-publish.yml")
	data, err := os.ReadFile(workflowPath)
	require.NoError(t, err)

	workflowYAML := string(data)

	// Check for helm package and helm push commands OR chart-releaser-action
	hasPushLogic := contains(workflowYAML, "helm package") && contains(workflowYAML, "helm push") ||
		contains(workflowYAML, "chart-releaser-action")

	assert.True(t, hasPushLogic,
		"workflow must use 'helm package' + 'helm push' for OCI publication or use chart-releaser-action")
}

// Acceptance Criterion 1: Workflow targets ghcr.io/caw/charts registry
func TestHelmPublish_WorkflowTargetsGHCR(t *testing.T) {
	workflowPath := filepath.Join(projectRoot(), ".github", "workflows", "helm-publish.yml")
	data, err := os.ReadFile(workflowPath)
	require.NoError(t, err)

	workflowYAML := string(data)

	assert.Contains(t, workflowYAML, "ghcr.io/caw/charts",
		"workflow must push charts to ghcr.io/caw/charts OCI registry")
}

// Acceptance Criterion 3: All Helm charts have version field
func TestHelmCharts_AllHaveVersion(t *testing.T) {
	helmDir := filepath.Join(projectRoot(), "deploy", "helm")
	entries, err := os.ReadDir(helmDir)
	require.NoError(t, err, "deploy/helm directory must exist")

	require.NotEmpty(t, entries, "deploy/helm must contain at least one chart")

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		chartYAMLPath := filepath.Join(helmDir, entry.Name(), "Chart.yaml")
		data, err := os.ReadFile(chartYAMLPath)
		require.NoError(t, err, "Chart.yaml must exist for chart %s", entry.Name())

		var chart struct {
			Version string `yaml:"version"`
		}
		require.NoError(t, yaml.Unmarshal(data, &chart),
			"Chart.yaml for %s must be valid YAML", entry.Name())

		assert.NotEmpty(t, chart.Version,
			"Chart.yaml for %s must have version field", entry.Name())
	}
}

// Acceptance Criterion 3: All Helm charts have appVersion field
func TestHelmCharts_AllHaveAppVersion(t *testing.T) {
	helmDir := filepath.Join(projectRoot(), "deploy", "helm")
	entries, err := os.ReadDir(helmDir)
	require.NoError(t, err, "deploy/helm directory must exist")

	require.NotEmpty(t, entries, "deploy/helm must contain at least one chart")

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		chartYAMLPath := filepath.Join(helmDir, entry.Name(), "Chart.yaml")
		data, err := os.ReadFile(chartYAMLPath)
		require.NoError(t, err, "Chart.yaml must exist for chart %s", entry.Name())

		var chart struct {
			AppVersion string `yaml:"appVersion"`
		}
		require.NoError(t, yaml.Unmarshal(data, &chart),
			"Chart.yaml for %s must be valid YAML", entry.Name())

		assert.NotEmpty(t, chart.AppVersion,
			"Chart.yaml for %s must have appVersion field", entry.Name())
	}
}

// Acceptance Criterion 5: Charts have required metadata (name, version, appVersion)
func TestHelmCharts_AllHaveRequiredMetadata(t *testing.T) {
	helmDir := filepath.Join(projectRoot(), "deploy", "helm")
	entries, err := os.ReadDir(helmDir)
	require.NoError(t, err, "deploy/helm directory must exist")

	require.NotEmpty(t, entries, "deploy/helm must contain at least one chart")

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		chartYAMLPath := filepath.Join(helmDir, entry.Name(), "Chart.yaml")
		data, err := os.ReadFile(chartYAMLPath)
		require.NoError(t, err, "Chart.yaml must exist for chart %s", entry.Name())

		var chart struct {
			Name       string `yaml:"name"`
			Version    string `yaml:"version"`
			AppVersion string `yaml:"appVersion"`
		}
		require.NoError(t, yaml.Unmarshal(data, &chart),
			"Chart.yaml for %s must be valid YAML", entry.Name())

		assert.NotEmpty(t, chart.Name, "Chart %s must have name field", entry.Name())
		assert.NotEmpty(t, chart.Version, "Chart %s must have version field", entry.Name())
		assert.NotEmpty(t, chart.AppVersion, "Chart %s must have appVersion field", entry.Name())
	}
}

// Acceptance Criterion 1: Workflow has publish job with proper permissions
func TestHelmPublish_WorkflowHasPublishJob(t *testing.T) {
	workflowPath := filepath.Join(projectRoot(), ".github", "workflows", "helm-publish.yml")
	data, err := os.ReadFile(workflowPath)
	require.NoError(t, err)

	var workflow struct {
		Jobs map[string]struct {
			Permissions struct {
				Packages string `yaml:"packages"`
				Contents string `yaml:"contents"`
			} `yaml:"permissions"`
			Runs_on string `yaml:"runs-on"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(data, &workflow))

	require.Contains(t, workflow.Jobs, "publish", "workflow must have 'publish' job")

	job := workflow.Jobs["publish"]
	assert.Equal(t, "ubuntu-latest", job.Runs_on, "publish job must run on ubuntu-latest")
	assert.Equal(t, "write", job.Permissions.Packages, "publish job must have packages: write permission")
	assert.Equal(t, "read", job.Permissions.Contents, "publish job must have contents: read permission")
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
