# Backend Integration Guide & Specification

**English** | [日本語](ja/backend_integration.md)

This document specifies the HTTP header contracts, authentication context propagation, and integration requirements for downstream backend microservices (e.g. `users-service`, `billing-service`, `analytics-service`) operating behind the Tollgate reverse proxy.

---

## 1. Architectural Overview

```
[ Client ]
    │  Authorization: Bearer <API_KEY> (or X-API-Key)
    ▼
[ Tollgate (Reverse Proxy / API Gateway) ]
    │  1. API Key Verification & Invalidation Check
    │  2. Scope Authorization (e.g. billing:write)
    │  3. Sliding Window Rate Limiting (RPM) & Monthly Quota Check
    │  4. Security Context Header Injection (X-Tenant-ID, X-Service-ID, etc.)
    │  5. Header Filtering (Strip/Forward context based on key type)
    ▼
[ Backend Service (users-service / billing-service / etc.) ]
    Access control & tenant isolation enforced based on X-Tenant-ID
```

---

## 2. Injected Request Header Specification

Tollgate intercepts client requests, authenticates credentials, and injects validated security context headers before proxying upstream:

| Header | Required | Type / Format | Description |
|:---|:---:|:---|:---|
| `X-Tenant-ID` | **Yes** | String (UUID / identifier) | **Tenant ID**. Identifies tenant boundary for data isolation.<br>• *Tenant Keys*: Automatically injected from key metadata. Mismatch with client header yields `403 Forbidden`.<br>• *Service Keys*: Forwarded from client header if supplied; missing yields `400 Bad Request`. |
| `X-Key-ID` | **Yes** | String (UUID) | Unique identifier of the authenticated API key. Used for audit logging and usage tracking. |
| `X-Key-Prefix` | **Yes** | String (e.g. `tlge-live-8f9c`) | Prefix identifier of the API key for troubleshooting and log correlation. |
| `X-Service-ID` | **Yes** | String | Service identifier configured for the key. Mismatch with client header yields `403 Forbidden`. |
| `X-Request-Id` | **Yes** | String (UUID) | Distributed request tracing ID. Preserves incoming client header or generates a UUID if omitted. |

---

## 3. User Context Header Filtering (Requirement T-2)

To prevent client spoofing, headers containing user context are conditionally filtered based on key type:

| Header Name | Service Key (BFF / Internal) | Tenant Key (External Client) |
|:---|:---:|:---:|
| `X-User-Id`<br>`X-User-Role`<br>`X-User-Email`<br>`X-Signature` | **Forwarded**<br>Preserved as trusted context from authenticated BFF. | **Stripped**<br>Completely removed to prevent external header spoofing. |

---

## 4. Conflict Rejection & Fail-Fast Policy

1. **Tenant Keys (Keys with `tenant_id` configured)**:
   - Client specifies **no** `X-Tenant-ID`: Key's `tenant_id` is automatically injected.
   - Client specifies `X-Tenant-ID` matching key metadata: Allowed.
   - Client specifies `X-Tenant-ID` **mismatching** key metadata: **`403 Forbidden` (`tenant_mismatch`)** Fail-Fast.
   - Client specifies `X-Service-ID` mismatching key metadata: **`403 Forbidden` (`service_mismatch`)** Fail-Fast.
2. **Service Keys (Keys without `tenant_id` configured, e.g. BFF)**:
   - Client specifies `X-Tenant-ID`: Forwarded directly.
   - Client specifies **no** `X-Tenant-ID`: **`400 Bad Request` (`missing_tenant_id`)** Fail-Fast.

---

## 5. Tenant Bulk Revocation & Offboarding (Requirement T-4, C-4)

When a tenant cancels or churns, calling the administrative endpoint:

```http
POST /v1/admin/tenants/{tenant_id}/revoke
Authorization: Bearer <ADMIN_API_KEY>
```

immediately revokes all associated API keys and evicts in-memory caches. Subsequent requests using any revoked key immediately return `403 Forbidden`.
