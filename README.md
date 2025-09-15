# LiveKit Auto-Recorder Service

This is a lightweight Go webhook receiver that automatically starts a recording for every new LiveKit room. It's designed to be deployed as a container in a Kubernetes environment and supports multiple cloud storage providers.

## Overview

The service works by listening for `room_started` webhooks from a LiveKit server. When it receives a valid webhook, it immediately calls the LiveKit Egress API to start a RoomCompositeEgress, which records the room to your configured cloud storage provider.

## Supported Storage Providers

- **Amazon S3** (and S3-compatible services like MinIO)
- **Microsoft Azure Blob Storage**
- **Google Cloud Storage**
- **Alibaba Cloud OSS**

## Configuration

The application supports two configuration methods:

1. **YAML Configuration File** (recommended)
2. **Environment Variables** (for backward compatibility)

### YAML Configuration

Create a `config.yaml` file (see `config.yaml.example` for a complete example):

```yaml
# LiveKit server configuration
livekit_host: "http://livekit-server.svc.cluster.local:7880"
livekit_api_key: "your-api-key"
livekit_api_secret: "your-api-secret"
webhook_api_key: "your-webhook-key"

# Server configuration
listen_port: "8080"
layout: "grid-16"
file_type: "MP4"
file_path: "recordings/{room_name}-{time}"

# Choose one storage provider
storage:
  s3:
    bucket: "my-recordings-bucket"
    region: "us-east-1"
    # access_key and secret can be omitted if using IAM roles
```

You can use environment variables in the YAML file with `${VARIABLE_NAME}` syntax.

### Environment Variables

| Environment Variable | Description | Example Value |
|---------------------|-------------|---------------|
| `CONFIG_FILE` | Path to YAML configuration file | `config.yaml` (default) |
| `LIVEKIT_HOST` | The WebSocket URL of your LiveKit server | `http://livekit-server.svc.cluster.local:7880` |
| `LIVEKIT_API_KEY` | The API key for your LiveKit server | `API...` |
| `LIVEKIT_API_SECRET` | The API secret for your LiveKit server | `secret...` |
| `WEBHOOK_API_KEY` | The api_key you configure in LiveKit's webhook section | `webhook-secret` |
| `PORT` | The port the service will listen on | `8080` (default) |
| `LAYOUT` | Recording layout | `grid-16` (default) |
| `FILE_TYPE` | Output file format | `MP4` (default), `WEBM`, `OGG` |
| `FILE_PATH` | File path template | `recordings/{room_name}-{time}` (default) |

#### Storage Provider Environment Variables

**Amazon S3:**
| Variable | Description | Required |
|----------|-------------|----------|
| `S3_BUCKET` | S3 bucket name | ✅ |
| `S3_ACCESS_KEY` | AWS access key (or use IAM role) | ⚠️ |
| `S3_SECRET` | AWS secret key (or use IAM role) | ⚠️ |
| `S3_SESSION_TOKEN` | AWS session token (for temporary creds) | ❌ |
| `S3_REGION` | AWS region | ❌ |
| `S3_ENDPOINT` | Custom endpoint (for S3-compatible services) | ❌ |

**Microsoft Azure:**
| Variable | Description | Required |
|----------|-------------|----------|
| `AZURE_STORAGE_ACCOUNT` | Azure storage account name | ✅ |
| `AZURE_STORAGE_KEY` | Azure storage account key | ✅ |
| `AZURE_CONTAINER_NAME` | Azure blob container name | ✅ |

**Google Cloud Storage:**
| Variable | Description | Required |
|----------|-------------|----------|
| `GOOGLE_APPLICATION_CREDENTIALS` | Path to JSON credentials file or JSON content | ✅ |
| `GCP_BUCKET` | GCS bucket name | ✅ |

**Alibaba Cloud OSS:**
| Variable | Description | Required |
|----------|-------------|----------|
| `ALIOSS_ACCESS_KEY` | AliOSS access key | ✅ |
| `ALIOSS_SECRET` | AliOSS secret key | ✅ |
| `ALIOSS_REGION` | AliOSS region | ✅ |
| `ALIOSS_BUCKET` | AliOSS bucket name | ✅ |
| `ALIOSS_ENDPOINT` | Custom endpoint | ❌ |

## Advanced Configuration

### File Path Templates

The `file_path` setting supports template variables:
- `{room_name}` - The name of the room being recorded
- `{time}` - Timestamp when recording started

Example: `recordings/{room_name}-{time}` → `recordings/my-room-20240315-143022`

### Recording Layouts

Supported layouts:
- `grid-16` - Grid layout with up to 16 participants
- `speaker-light` - Speaker view with light theme
- `speaker-dark` - Speaker view with dark theme
- Custom layouts (refer to LiveKit documentation)

### Proxy Configuration (S3 and GCP)

For S3 and GCP storage, you can configure proxy settings in the YAML file:

```yaml
storage:
  s3:
    # ... other config
    proxy_config:
      url: "http://proxy.example.com:8080"
      username: "proxy-user"
      password: "proxy-pass"
```

### Retry Configuration (S3 only)

S3 storage supports retry configuration:

```yaml
storage:
  s3:
    # ... other config
    max_retries: 3
    max_retry_delay: "5s"
    min_retry_delay: "500ms"
    aws_log_level: "LogOff"  # LogOff, LogDebug, LogDebugWithRequestRetries
```

## How to Run

### 1. Configure LiveKit Webhooks

In your LiveKit server's configuration (livekit.yaml or Helm values.yaml), add a webhook configuration:

```yaml
webhooks:
  urls:
    - http://auto-recorder.default.svc.cluster.local:8080/webhook-receiver
  api_key: your-webhook-api-key
  events:
    - room_started
```

### 2. Build the Docker Image

From the project's root directory:

```bash
docker build -t your-registry/livekit-auto-recorder:latest .
```

### 3. Deploy to Kubernetes

#### Using YAML Configuration (Recommended)

Create a ConfigMap with your configuration:

```bash
kubectl create configmap auto-recorder-config --from-file=config.yaml
```

Then reference it in your deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: livekit-auto-recorder
spec:
  replicas: 1
  selector:
    matchLabels:
      app: livekit-auto-recorder
  template:
    metadata:
      labels:
        app: livekit-auto-recorder
    spec:
      containers:
      - name: auto-recorder
        image: your-registry/livekit-auto-recorder:latest
        ports:
        - containerPort: 8080
        volumeMounts:
        - name: config
          mountPath: /app/config.yaml
          subPath: config.yaml
        env:
        - name: CONFIG_FILE
          value: "/app/config.yaml"
      volumes:
      - name: config
        configMap:
          name: auto-recorder-config
```

#### Using Environment Variables

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: livekit-auto-recorder
spec:
  replicas: 1
  selector:
    matchLabels:
      app: livekit-auto-recorder
  template:
    metadata:
      labels:
        app: livekit-auto-recorder
    spec:
      containers:
      - name: auto-recorder
        image: your-registry/livekit-auto-recorder:latest
        ports:
        - containerPort: 8080
        env:
        - name: LIVEKIT_HOST
          value: "http://livekit-server.svc.cluster.local:7880"
        - name: LIVEKIT_API_KEY
          valueFrom:
            secretKeyRef:
              name: livekit-secrets
              key: api-key
        - name: LIVEKIT_API_SECRET
          valueFrom:
            secretKeyRef:
              name: livekit-secrets
              key: api-secret
        - name: WEBHOOK_API_KEY
          valueFrom:
            secretKeyRef:
              name: livekit-secrets
              key: webhook-key
        - name: S3_BUCKET
          value: "my-recordings-bucket"
        - name: S3_REGION
          value: "us-east-1"
        # Add other storage-specific variables as needed
```

## Security Considerations

- Store sensitive credentials (API keys, storage credentials) in Kubernetes Secrets
- Use IAM roles when possible instead of hardcoded credentials
- Ensure your webhook endpoint is only accessible from your LiveKit server
- Consider using network policies to restrict traffic

## Troubleshooting

### Common Issues

1. **"No storage provider configured"** - Ensure at least one storage provider is properly configured
2. **"Only one storage provider can be configured"** - Remove configuration for unused storage providers
3. **Authentication failures** - Verify API keys and credentials are correct
4. **Storage upload failures** - Check bucket permissions and network connectivity

### Debugging

Enable debug logging by setting the log level in your configuration or checking container logs:

```bash
kubectl logs deployment/livekit-auto-recorder
```

## Contributing

Contributions are welcome! Please ensure your changes:
- Follow Go best practices
- Include appropriate error handling
- Update documentation as needed
- Add tests for new functionality