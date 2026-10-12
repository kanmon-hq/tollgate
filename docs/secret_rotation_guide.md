# Shared Secret & Master Key Rotation Guide (Requirement C-2)

**English** | [日本語](ja/secret_rotation_guide.md)

This document establishes operational procedures for rotating Tollgate administrative master keys (`ADMIN_API_KEY`) and internal shared credentials with zero downtime.

---

## 1. Overview & Rotation Principle

Tollgate uses `ADMIN_API_KEY` to authenticate administrative and configuration operations (`/v1/admin/*`). To prevent service interruption during rotation:
- AWS Secrets Manager version staging labels (`AWSCURRENT` and `AWSPREVIOUS`) are leveraged.
- Rolling deployments across ECS task instances allow callers and receivers to switch keys smoothly without dropping requests.

---

## 2. Zero-Downtime Rotation Lifecycle

```
[ Step 1: Update Secret ]
Secrets Manager: Add new secret value. Both AWSCURRENT (New) and AWSPREVIOUS (Old) are tracked.
       │
       ▼
[ Step 2: Deploy Callers (e.g. Backend Management Services) ]
Update management service to accept or present the new key.
       │
       ▼
[ Step 3: Rolling Update Tollgate Service ]
Trigger ECS rolling update (`aws ecs update-service --force-new-deployment`).
New tasks launch with the new secret injected into container environment variables.
ALB shifts traffic gradually while draining old tasks cleanly.
       │
       ▼
[ Step 4: Verification & Deprecation ]
Verify health checks and management endpoints with new secret.
Retire old credentials from Secrets Manager.
```

---

## 3. Step-by-Step Execution

### Step 1: Put Secret Value in AWS Secrets Manager

```bash
aws secretsmanager put-secret-value \
  --secret-id "tollgate/admin-api-key" \
  --secret-string "new-secure-random-admin-secret-string" \
  --version-stages "AWSCURRENT"
```

### Step 2: Update Calling Backend Services
Update any internal services that call Tollgate's `/v1/admin/*` APIs (e.g., tenant provisioning service, billing admin) with the new credentials.

### Step 3: Force Rolling Deployment in ECS Fargate

```bash
aws ecs update-service \
  --cluster "production-cluster" \
  --service "tollgate-service" \
  --force-new-deployment
```

ECS launches replacement tasks pulling the updated `ADMIN_API_KEY` from Secrets Manager. ALB target groups execute health checks (`/readyz`) before directing production traffic to the new instances.

### Step 4: Verification
Confirm management API responses with the new credential:

```bash
curl -f -X GET http://<TOLLGATE_URL>/v1/admin/keys \
  -H "Authorization: Bearer new-secure-random-admin-secret-string"
```

Confirm that requests with the deprecated secret return `401 Unauthorized` Fail-Fast once all old tasks are fully terminated.
