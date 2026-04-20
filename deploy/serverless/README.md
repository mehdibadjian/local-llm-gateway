# CAW Serverless Deployment Guide

This directory contains deployment manifests and configuration for running CAW (Codex Artifact Wrapper) on serverless platforms: **Knative** and **AWS Lambda**.

## Overview

CAW supports two serverless deployment patterns:
- **Knative** (on Kubernetes clusters with Knative Serving)
- **AWS Lambda** (container images on Amazon Lambda)

Both deployments use the same CAW Docker image (`scratch`-based, ~15MB) and set `CAW_SERVERLESS_MODE` to enable graceful connection draining and resource-aware startup.

---

## Knative Deployment

### Prerequisites
- Kubernetes cluster with **Knative Serving 1.x** installed
- `kubectl` configured to access your cluster
- CAW image pushed to a container registry (e.g., `ghcr.io/caw/wrapper:latest`)

### Configuration

**File:** `knative-service.yaml`

Key features:
- **minScale: 0** — scales down to zero when no traffic (cost optimization)
- **maxScale: 10** — limits horizontal scaling to 10 concurrent instances
- **CAW_SERVERLESS_MODE=knative** — signals to CAW that it's running in Knative
- **Resource requests/limits:** 100m CPU, 128Mi memory (request) / 500m CPU, 512Mi (limit)
- **Health checks:** `/healthz` (liveness) and `/readyz` (readiness)

### Deploy

```bash
# Apply the Knative Service
kubectl apply -f deploy/serverless/knative-service.yaml

# Check status
kubectl get ksvc caw-wrapper
kubectl get pods

# View logs (after traffic)
kubectl logs -f -l app=caw-wrapper -c caw-wrapper

# Get the Knative service URL
kubectl get ksvc caw-wrapper -o jsonpath='{.status.url}'
```

### Monitoring Scale Events

Knative autoscaling is driven by requests-per-second metrics (default: 100 RPS per instance). To observe:

```bash
# Watch pod creation/deletion during traffic
kubectl get pods -w

# Inspect Knative autoscaler logs
kubectl logs -l knative.dev/service=caw-wrapper -n knative-serving -c autoscaler
```

### Environment Variables

When deployed to Knative:
- `CAW_SERVERLESS_MODE=knative` (set in manifest)
- All standard CAW env vars (e.g., `REDIS_URL`, `QDRANT_URL`) must be passed separately

---

## AWS Lambda Deployment

### Prerequisites
- AWS account with Lambda and ECR permissions
- AWS CLI configured (`aws --version`)
- Docker installed for building images
- CAW Docker image built locally

### Configuration

**File:** `lambda-function.yaml` (CloudFormation template)

Key features:
- **PackageType: Image** — Lambda function backed by container image
- **Timeout: 300s** — allows long-running inference requests
- **Memory: 512 MB** (configurable via parameter)
- **CAW_SERVERLESS_MODE=lambda** — signals serverless execution context
- **EntryPoint: /caw** — the CAW binary from the container
- **Lambda URL enabled** — HTTP(S) endpoint for direct function invocation

### Deploy

#### 1. Build and Push Image to ECR

```bash
# Create ECR repository (one-time)
aws ecr create-repository --repository-name caw-wrapper --region us-east-1

# Build and push the image
docker build -t caw-wrapper:latest .
aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin 123456789012.dkr.ecr.us-east-1.amazonaws.com
docker tag caw-wrapper:latest 123456789012.dkr.ecr.us-east-1.amazonaws.com/caw-wrapper:latest
docker push 123456789012.dkr.ecr.us-east-1.amazonaws.com/caw-wrapper:latest
```

Replace `123456789012` with your AWS account ID.

#### 2. Deploy CloudFormation Stack

```bash
# Create stack with defaults
aws cloudformation create-stack \
  --stack-name caw-wrapper-stack \
  --template-body file://deploy/serverless/lambda-function.yaml \
  --parameters ParameterKey=ImageUri,ParameterValue=123456789012.dkr.ecr.us-east-1.amazonaws.com/caw-wrapper:latest \
  --capabilities CAPABILITY_NAMED_IAM \
  --region us-east-1

# Check status
aws cloudformation describe-stacks --stack-name caw-wrapper-stack --region us-east-1

# Get outputs (function URL, ARN)
aws cloudformation describe-stacks \
  --stack-name caw-wrapper-stack \
  --query 'Stacks[0].Outputs' \
  --region us-east-1
```

#### 3. Invoke the Lambda Function

```bash
# Get the Lambda function URL
FUNCTION_URL=$(aws cloudformation describe-stacks \
  --stack-name caw-wrapper-stack \
  --query 'Stacks[0].Outputs[?OutputKey==`LambdaFunctionUrl`].OutputValue' \
  --output text \
  --region us-east-1)

# Test the function via HTTP
curl -X GET "${FUNCTION_URL}healthz"

# Invoke directly via AWS Lambda API
aws lambda invoke \
  --function-name caw-wrapper \
  --payload '{"path":"/healthz","httpMethod":"GET"}' \
  response.json \
  --region us-east-1 && cat response.json
```

### Environment Variables (AWS Lambda)

When deployed to Lambda:
- `CAW_SERVERLESS_MODE=lambda` (set in CloudFormation template)
- `AWS_LAMBDA_LOG_TYPE=Tail` (to capture logs)
- Pass additional env vars via CloudFormation `Environment.Variables` or AWS console

### Monitoring & Logs

```bash
# View recent invocations and errors
aws lambda get-function --function-name caw-wrapper --region us-east-1

# Stream CloudWatch logs (live)
aws logs tail /aws/lambda/caw-wrapper --follow --region us-east-1

# Check Lambda metrics (invocations, duration, errors)
aws cloudwatch get-metric-statistics \
  --namespace AWS/Lambda \
  --metric-name Duration \
  --dimensions Name=FunctionName,Value=caw-wrapper \
  --start-time 2024-01-01T00:00:00Z \
  --end-time 2024-01-02T00:00:00Z \
  --period 300 \
  --statistics Average,Maximum \
  --region us-east-1
```

### Update Lambda Code

To redeploy after code changes:

```bash
# Rebuild and push new image
docker build -t caw-wrapper:v2 .
docker tag caw-wrapper:v2 123456789012.dkr.ecr.us-east-1.amazonaws.com/caw-wrapper:v2
docker push 123456789012.dkr.ecr.us-east-1.amazonaws.com/caw-wrapper:v2

# Update the Lambda function
aws lambda update-function-code \
  --function-name caw-wrapper \
  --image-uri 123456789012.dkr.ecr.us-east-1.amazonaws.com/caw-wrapper:v2 \
  --region us-east-1
```

---

## Comparison: Knative vs Lambda

| Feature | Knative | Lambda |
|---------|---------|--------|
| **Platform** | Kubernetes | AWS |
| **Min Scale** | 0 (cold start) | 0 (provisioned mode available) |
| **Pricing** | Per pod/CPU-hour + egress | Per invocation + GB-second |
| **Startup Latency** | ~100-500ms (Knative optimized) | ~1-5s (cold start) |
| **Max Timeout** | Configurable (default 5m) | 15 minutes |
| **Scaling Trigger** | RPS, CPU, custom metrics | Invocation rate |
| **Network** | Direct K8s networking | Lambda VPC (optional) |
| **State** | Ephemeral (no local storage) | Ephemeral (/tmp only) |
| **Container Image** | Same as Knative | Same as Knative |

---

## CAW Serverless Mode Behavior

When `CAW_SERVERLESS_MODE` is set (`knative` or `lambda`), CAW enables:

1. **Graceful Connection Draining** — on shutdown signal, existing connections complete while new requests are rejected
2. **Fast Health Checks** — minimal startup time for `/healthz` and `/readyz` endpoints
3. **Reduced Memory Footprint** — disables expensive startup procedures
4. **Request Timeout Awareness** — respects platform-specific timeout limits

To verify serverless mode is active, check CAW startup logs:
```
CAW wrapper started in serverless mode: knative
```

---

## Troubleshooting

### Knative

**Cold start takes >5 seconds:**
- Check `minScale` is set to 0 (scale-to-zero is active)
- Verify resource requests don't exceed node capacity
- Review Knative autoscaler logs

**Function not scaling:**
- Ensure Knative metrics-server is running
- Check `maxScale` annotation is present on both metadata and spec.template.metadata
- Verify ingress/traffic is reaching the service

### Lambda

**Function times out:**
- Check `Timeout` in CloudFormation template (default 300s)
- Verify image URI is accessible from Lambda execution role
- Review CloudWatch logs for slow operations

**"Unable to pull image" error:**
- Confirm ECR repository exists and image is pushed
- Verify Lambda execution role has `ecr:GetDownloadUrlForLayer` and `ecr:BatchGetImage` permissions
- Check image URI format (must include full registry path)

---

## Next Steps

1. **Production Readiness:**
   - Store image in a persistent registry (ECR, GCR, GHCR)
   - Add monitoring/alerting (CloudWatch, Prometheus)
   - Implement request logging and distributed tracing
   - Use VPC for Lambda (if accessing private resources)

2. **Cost Optimization:**
   - Set `minScale: 0` on Knative to eliminate idle cost
   - Use Lambda Provisioned Concurrency if cold starts impact SLA
   - Monitor and adjust `maxScale` / concurrency limits

3. **Integration:**
   - Wire up API Gateway or ALB for request routing
   - Configure domain/TLS termination
   - Add authentication/authorization middleware

---

**Created:** 2024-04-20  
**Updated:** 2024-04-20  
**Maintained by:** CAW Core Team
