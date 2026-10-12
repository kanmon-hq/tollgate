# AWS ECS Fargate Deployment Guide (Requirement C-1)

**English** | [日本語](ja/fargate_deployment_guide.md)

This document provides operational guidelines, health check configurations, and task definition specifications for running Tollgate in production on **AWS ECS Fargate**.

---

## 1. Architectural Principles

- **Stateless Operation**: Tollgate container instances do not store local state; all API key metadata and rate limits are managed via external stores (Amazon DynamoDB, Redis/Valkey, etc.).
- **Multi-Architecture Support**: Supports both AWS Graviton (`linux/arm64`) and x86 (`linux/amd64`). ARM64 is strongly recommended for lower cost and superior performance.
- **Graceful Shutdown**: Traps `SIGTERM` signals with a 30-second drain window to finish in-flight proxy requests before terminating.
- **Probe Separation**: Distinguishes between Liveness (`/livez`) and Readiness (`/readyz`) to prevent premature traffic drops during transient downstream database retries.

---

## 2. Health Checks & Probes

| Probe Endpoint | Purpose | Target / Response | Usage |
|:---|:---|:---|:---|
| `GET /livez` | Liveness | Instant `200 OK` (checks process heartbeat) | ECS Task Definition `healthCheck` |
| `GET /readyz` | Readiness | Checks DynamoDB/storage ping; `200 OK` on success, `503 Service Unavailable` on failure | Application Load Balancer (ALB) Target Group health check |
| `GET /healthz` | Comprehensive | Returns JSON payload with process uptime and database status | Administrative diagnostic monitoring |

### ECS Task Definition Health Check
```json
"healthCheck": {
  "command": [
    "CMD-SHELL",
    "wget -q --spider http://localhost:8000/livez || exit 1"
  ],
  "interval": 10,
  "timeout": 3,
  "retries": 3,
  "startPeriod": 5
}
```

### ALB Target Group Health Check Settings
- **Protocol**: `HTTP`
- **Path**: `/readyz`
- **Port**: `traffic-port` (8000)
- **Healthy Threshold**: `2`
- **Unhealthy Threshold**: `3`
- **Timeout**: `5 seconds`
- **Interval**: `15 seconds`
- **Success Codes**: `200`

---

## 3. Graceful Termination & Drain

When ECS initiates a deployment or scales in, Fargate sends a `SIGTERM` signal to the container:

1. **Stop Timeout Configuration**:
   Set `stopTimeout: 30` in the task definition. This provides ample time for ALB connection draining and in-flight requests to complete.
2. **Server Shutdown Flow**:
   Upon receiving `SIGTERM`, Tollgate closes listen sockets, completes active HTTP proxy connections within the grace timeout, flushes internal metrics, and cleanly exits with status code 0.

---

## 4. Production ECS Task Definition Example (ARM64)

```json
{
  "family": "tollgate-service",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "cpu": "512",
  "memory": "1024",
  "runtimePlatform": {
    "cpuArchitecture": "ARM64",
    "operatingSystemFamily": "LINUX"
  },
  "containerDefinitions": [
    {
      "name": "tollgate",
      "image": "ghcr.io/kanmon-hq/tollgate:v1.1.2",
      "essential": true,
      "portMappings": [
        {
          "containerPort": 8000,
          "hostPort": 8000,
          "protocol": "tcp"
        }
      ],
      "environment": [
        { "name": "PORT", "value": "8000" },
        { "name": "DB_BACKEND", "value": "dynamodb" },
        { "name": "AWS_REGION", "value": "ap-northeast-1" },
        { "name": "TABLE_NAME", "value": "TollgateAPIKeys" },
        { "name": "RATE_LIMIT_BACKEND", "value": "memory" },
        { "name": "KEY_CACHE_TTL", "value": "10" }
      ],
      "secrets": [
        {
          "name": "ADMIN_API_KEY",
          "valueFrom": "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:tollgate/admin-api-key"
        }
      ],
      "stopTimeout": 30,
      "healthCheck": {
        "command": [
          "CMD-SHELL",
          "wget -q --spider http://localhost:8000/livez || exit 1"
        ],
        "interval": 10,
        "timeout": 3,
        "retries": 3,
        "startPeriod": 5
      },
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/ecs/tollgate",
          "awslogs-region": "ap-northeast-1",
          "awslogs-stream-prefix": "ecs"
        }
      }
    }
  ]
}
```
