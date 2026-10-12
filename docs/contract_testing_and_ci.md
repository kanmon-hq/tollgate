# Tollgate Contract Testing & CI Automation Guide

**English** | [日本語](ja/contract_testing_and_ci.md)

This document outlines the contract testing strategy and continuous integration (CI) automation pipeline designed to verify multi-tenant isolation, proxy security boundaries, and zero-downtime operations (Requirement **C-5**).

---

## 1. Objectives & Verification Scope

As an API gateway and reverse proxy, Tollgate must strictly adhere to the following behavioral contracts with backend services and external clients:

| Scenario | Scope | Requirement | Expected Behavior |
|:---|:---|:---:|:---|
| **1. Service Key Pass-Through** | Requests with internal service keys (e.g., BFF) | T-2 | Preserves and forwards user context headers (`X-User-Id`, `X-User-Role`, `X-User-Context`, etc.) to upstream services. |
| **2. Tenant Key Spoofing Prevention** | Requests with external tenant API keys | T-2 | Completely strips user context headers (`X-User-Id`, etc.) supplied by the client to prevent spoofing. |
| **3. Tenant Conflict Rejection** | Client supplies `X-Tenant-Id` that mismatches key metadata | Security Policy | Rejects immediately with `403 Forbidden` (`tenant_mismatch`) Fail-Fast without implicit overwrites. |
| **4. Shared Secret Mismatch** | Invalid API key or wrong `ADMIN_API_KEY` | C-5 | Rejects with `401 Unauthorized` for both proxy traffic and admin API operations. |
| **5. Zero-Downtime Rotation** | Requests during key rotation grace period | T-1, C-2 | Both old and new keys remain valid concurrently (returns `200 OK`), ensuring zero-downtime credential rotation. |
| **6. Tenant Bulk Revocation** | `POST /v1/admin/tenants/{tenant_id}/revoke` | T-4, C-4 | All keys under the target tenant are revoked immediately (`403 Forbidden`), while isolating and preserving other tenants' keys. |
| **7. Request ID Tracing** | Forwarding and auto-generation of `X-Request-Id` | C-3 | Forwards existing client `X-Request-Id` to upstream and response; generates a UUID if omitted. |

---

## 2. Two-Tier Contract Testing Architecture

To balance rapid developer feedback with real-world environment validation, Tollgate implements contract testing across two complementary tiers:

```
[ Tier 1: In-Memory Contract Test (tests/contract_test.go) ]
  ├── Runtime: Pure Go test (SQLite :memory: + httptest.Server)
  ├── Characteristics: Sub-second execution, -race detection enabled
  └── Target: PR verification & local pre-commit checks

[ Tier 2: Real Container E2E Contract Test (tests/e2e_contract_test.go) ]
  ├── Runtime: Docker Compose (tollgate + mock-server + dynamodb)
  ├── Characteristics: Black-box HTTP validation across real networking
  └── Target: GitHub Actions CI & pre-deployment verification
```

### Tier 1: In-Memory Contract Tests (`tests/contract_test.go`)
Runs completely in-process using an in-memory SQLite store and Go's `httptest.Server`. It tests full router, proxy handler, rate limiting, and security rules without requiring external container dependencies.

```bash
# Execute in-memory contract tests
go test -v -run TestContract_SecurityAndMultiTenant ./tests
```

### Tier 2: Live Container E2E Contract Tests (`tests/e2e_contract_test.go`)
Tests live running containers over HTTP via exposed ports. Executed only when the `E2E_BASE_URL` environment variable is specified.

```bash
# 1. Start Docker Compose environment
docker compose --profile dynamodb up -d --build tollgate mock-server dynamodb dynamodb-init

# 2. Run E2E contract verification
E2E_BASE_URL="http://localhost:8080" E2E_ADMIN_KEY="admin-secret-key-for-local-dev" go test -v -run TestE2E_ContractVerification ./tests

# 3. Teardown
docker compose --profile dynamodb down -v
```

---

## 3. GitHub Actions CI Pipeline

The CI workflow ([.github/workflows/test.yml](../.github/workflows/test.yml)) automatically runs the following jobs on pull requests and pushes to `main`:

```mermaid
graph TD
    A[Push / PR Trigger] --> B[Job 1: test]
    A --> C[Job 2: e2e-compose-contract]
    A --> D[Job 3: docker-build-check]
    A --> E[Job 4: check-docs-sync]

    subgraph Job 1: Unit & In-Memory Contract
        B1[go vet Static Analysis] --> B2[go test -race -cover ./...]
    end

    subgraph Job 2: Docker Compose E2E (C-5)
        C1[docker compose up -d] --> C2[Poll /readyz]
        C2 --> C3[go test TestE2E_ContractVerification]
        C3 --> C4[docker compose down]
    end

    subgraph Job 3: Multi-Arch Build Check (C-1)
        D1[QEMU & Buildx Setup] --> D2[Build linux/amd64, linux/arm64]
    end

    subgraph Job 4: Documentation Sync
        E1[Verify EN/JA docs sync as Warning reminder]
    end
```

1. **`test`**: Static analysis with `go vet`, in-memory contract tests, and unit tests with `-race` and code coverage tracking.
2. **`e2e-compose-contract`**: Spins up Compose services, waits for `/readyz` health readiness, and executes live E2E contract tests.
3. **`docker-build-check`**: Validates multi-architecture container builds (`linux/amd64` and `linux/arm64`) for AWS Graviton / ECS Fargate compatibility without pushing.
4. **`check-docs-sync`**: Inspects PR file diffs to detect unsynchronized documentation pairs between English and Japanese, generating non-blocking warning annotations.

---

## 4. Versioning and Release Automation (C-7)

- Follows semantic versioning standards detailed in [Versioning & Compatibility Policy](versioning_and_compatibility.md).
- Pushing a version tag (`v*.*.*`) triggers `.github/workflows/release.yml`, generating GoReleaser multi-platform binary archives and publishing multi-arch OCI images to GitHub Container Registry (GHCR).
