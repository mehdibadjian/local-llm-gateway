package iac_test

import (
"os"
"path/filepath"
"testing"

"github.com/stretchr/testify/assert"
"github.com/stretchr/testify/require"
"gopkg.in/yaml.v3"
)

func serverlessDir() string {
return filepath.Join(projectRoot(), "deploy", "serverless")
}

// TestServerless_KnativeServiceExists verifies the Knative Service manifest exists
func TestServerless_KnativeServiceExists(t *testing.T) {
path := filepath.Join(serverlessDir(), "knative-service.yaml")
_, err := os.Stat(path)
require.NoError(t, err, "knative-service.yaml must exist in deploy/serverless")
}

// TestServerless_KnativeMinScaleZero verifies minScale annotation is set to 0
func TestServerless_KnativeMinScaleZero(t *testing.T) {
path := filepath.Join(serverlessDir(), "knative-service.yaml")
data, err := os.ReadFile(path)
require.NoError(t, err)

var ksvc struct {
Metadata struct {
Annotations map[string]string `yaml:"annotations"`
} `yaml:"metadata"`
Spec struct {
Template struct {
Metadata struct {
Annotations map[string]string `yaml:"annotations"`
} `yaml:"metadata"`
} `yaml:"template"`
} `yaml:"spec"`
}
require.NoError(t, yaml.Unmarshal(data, &ksvc))

// Check metadata-level annotations
assert.Equal(t, "0", ksvc.Metadata.Annotations["autoscaling.knative.dev/minScale"],
"metadata.annotations['autoscaling.knative.dev/minScale'] must be '0'")

// Check template-level annotations
assert.Equal(t, "0", ksvc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/minScale"],
"spec.template.metadata.annotations['autoscaling.knative.dev/minScale'] must be '0'")
}

// TestServerless_KnativeMaxScale10 verifies maxScale annotation is set to 10
func TestServerless_KnativeMaxScale10(t *testing.T) {
path := filepath.Join(serverlessDir(), "knative-service.yaml")
data, err := os.ReadFile(path)
require.NoError(t, err)

var ksvc struct {
Metadata struct {
Annotations map[string]string `yaml:"annotations"`
} `yaml:"metadata"`
Spec struct {
Template struct {
Metadata struct {
Annotations map[string]string `yaml:"annotations"`
} `yaml:"metadata"`
} `yaml:"template"`
} `yaml:"spec"`
}
require.NoError(t, yaml.Unmarshal(data, &ksvc))

// Check metadata-level annotations
assert.Equal(t, "10", ksvc.Metadata.Annotations["autoscaling.knative.dev/maxScale"],
"metadata.annotations['autoscaling.knative.dev/maxScale'] must be '10'")

// Check template-level annotations
assert.Equal(t, "10", ksvc.Spec.Template.Metadata.Annotations["autoscaling.knative.dev/maxScale"],
"spec.template.metadata.annotations['autoscaling.knative.dev/maxScale'] must be '10'")
}

// TestServerless_KnativeEnvVarServerlessMode verifies CAW_SERVERLESS_MODE is set to knative
func TestServerless_KnativeEnvVarServerlessMode(t *testing.T) {
path := filepath.Join(serverlessDir(), "knative-service.yaml")
data, err := os.ReadFile(path)
require.NoError(t, err)

var ksvc struct {
Spec struct {
Template struct {
Spec struct {
Containers []struct {
Env []struct {
Name  string `yaml:"name"`
Value string `yaml:"value"`
} `yaml:"env"`
} `yaml:"containers"`
} `yaml:"spec"`
} `yaml:"template"`
} `yaml:"spec"`
}
require.NoError(t, yaml.Unmarshal(data, &ksvc))

require.NotEmpty(t, ksvc.Spec.Template.Spec.Containers, "containers must be defined")

var found bool
for _, env := range ksvc.Spec.Template.Spec.Containers[0].Env {
if env.Name == "CAW_SERVERLESS_MODE" {
found = true
assert.Equal(t, "knative", env.Value, "CAW_SERVERLESS_MODE must be 'knative'")
break
}
}
assert.True(t, found, "CAW_SERVERLESS_MODE env var must be defined")
}

// TestServerless_LambdaFunctionExists verifies the Lambda CloudFormation template exists
func TestServerless_LambdaFunctionExists(t *testing.T) {
path := filepath.Join(serverlessDir(), "lambda-function.yaml")
_, err := os.Stat(path)
require.NoError(t, err, "lambda-function.yaml must exist in deploy/serverless")
}

// TestServerless_LambdaPackageTypeImage verifies Lambda PackageType is set to Image
func TestServerless_LambdaPackageTypeImage(t *testing.T) {
path := filepath.Join(serverlessDir(), "lambda-function.yaml")
data, err := os.ReadFile(path)
require.NoError(t, err)

var cf struct {
Resources struct {
CAWLambdaFunction struct {
Properties struct {
PackageType string `yaml:"PackageType"`
} `yaml:"Properties"`
} `yaml:"CAWLambdaFunction"`
} `yaml:"Resources"`
}
require.NoError(t, yaml.Unmarshal(data, &cf))

assert.Equal(t, "Image", cf.Resources.CAWLambdaFunction.Properties.PackageType,
"CAWLambdaFunction.Properties.PackageType must be 'Image'")
}

// TestServerless_LambdaImageUriParameter verifies Lambda has ImageUri parameter
func TestServerless_LambdaImageUriParameter(t *testing.T) {
path := filepath.Join(serverlessDir(), "lambda-function.yaml")
data, err := os.ReadFile(path)
require.NoError(t, err)

var cf struct {
Parameters map[string]interface{} `yaml:"Parameters"`
}
require.NoError(t, yaml.Unmarshal(data, &cf))

_, exists := cf.Parameters["ImageUri"]
assert.True(t, exists, "ImageUri parameter must be defined in CloudFormation template")
}

// TestServerless_LambdaEnvVarServerlessMode verifies CAW_SERVERLESS_MODE is set to lambda
func TestServerless_LambdaEnvVarServerlessMode(t *testing.T) {
path := filepath.Join(serverlessDir(), "lambda-function.yaml")
data, err := os.ReadFile(path)
require.NoError(t, err)

var cf struct {
Resources struct {
CAWLambdaFunction struct {
Properties struct {
Environment struct {
Variables map[string]string `yaml:"Variables"`
} `yaml:"Environment"`
} `yaml:"Properties"`
} `yaml:"CAWLambdaFunction"`
} `yaml:"Resources"`
}
require.NoError(t, yaml.Unmarshal(data, &cf))

mode, exists := cf.Resources.CAWLambdaFunction.Properties.Environment.Variables["CAW_SERVERLESS_MODE"]
assert.True(t, exists, "CAW_SERVERLESS_MODE env var must be defined")
assert.Equal(t, "lambda", mode, "CAW_SERVERLESS_MODE must be 'lambda'")
}

// TestServerless_LambdaEntryPoint verifies Lambda EntryPoint is set to /caw
func TestServerless_LambdaEntryPoint(t *testing.T) {
path := filepath.Join(serverlessDir(), "lambda-function.yaml")
data, err := os.ReadFile(path)
require.NoError(t, err)

var cf struct {
Resources struct {
CAWLambdaFunction struct {
Properties struct {
ImageConfig struct {
EntryPoint []string `yaml:"EntryPoint"`
} `yaml:"ImageConfig"`
} `yaml:"Properties"`
} `yaml:"CAWLambdaFunction"`
} `yaml:"Resources"`
}
require.NoError(t, yaml.Unmarshal(data, &cf))

require.NotEmpty(t, cf.Resources.CAWLambdaFunction.Properties.ImageConfig.EntryPoint,
"EntryPoint must be defined")
assert.Equal(t, "/caw", cf.Resources.CAWLambdaFunction.Properties.ImageConfig.EntryPoint[0],
"EntryPoint must be '/caw'")
}

// TestServerless_ReadmeExists verifies deployment guide README exists
func TestServerless_ReadmeExists(t *testing.T) {
path := filepath.Join(serverlessDir(), "README.md")
_, err := os.Stat(path)
require.NoError(t, err, "README.md must exist in deploy/serverless")
}

// TestServerless_ReadmeHasKnativeSection verifies README contains Knative documentation
func TestServerless_ReadmeHasKnativeSection(t *testing.T) {
path := filepath.Join(serverlessDir(), "README.md")
data, err := os.ReadFile(path)
require.NoError(t, err)

content := string(data)
assert.Contains(t, content, "## Knative Deployment", "README must contain Knative Deployment section")
assert.Contains(t, content, "minScale: 0", "README must mention minScale: 0")
assert.Contains(t, content, "maxScale: 10", "README must mention maxScale: 10")
}

// TestServerless_ReadmeHasLambdaSection verifies README contains Lambda documentation
func TestServerless_ReadmeHasLambdaSection(t *testing.T) {
path := filepath.Join(serverlessDir(), "README.md")
data, err := os.ReadFile(path)
require.NoError(t, err)

content := string(data)
assert.Contains(t, content, "## AWS Lambda Deployment", "README must contain AWS Lambda Deployment section")
assert.Contains(t, content, "PackageType: Image", "README must mention PackageType: Image")
assert.Contains(t, content, "CAW_SERVERLESS_MODE=lambda", "README must mention CAW_SERVERLESS_MODE=lambda")
}
