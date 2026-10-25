# Tollgate — Lightweight API Key Management & Rate Limiting Gateway

**English** | [日本語](README.ja.md)

[![Go](https://img.shields.io/badge/Go-1.27+-00ADD8?style=flat&logo=go)](https://golang.org/)
[![Huma v2](https://img.shields.io/badge/Huma-v2.39+-8A2BE2.svg)](https://huma.rocks/)
[![DynamoDB](https://img.shields.io/badge/Storage-DynamoDB%20%7C%20CosmosDB%20%7C%20Firestore%20%7C%20SQLite-4053D6.svg)](https://aws.amazon.com/dynamodb/)
[![Redis / Valkey](https://img.shields.io/badge/RateLimit-Memory%20%7C%20Redis%20%7C%20Valkey%20%7C%20Two--Tier-DC382D.svg)](https://valkey.io/)
[![Prometheus](https://img.shields.io/badge/Prometheus-Metrics-E6522C.svg?logo=prometheus)](https://prometheus.io/)
[![OpenAPI](https://img.shields.io/badge/OpenAPI-3.1-6BA539.svg?logo=openapiinitiative)](https://spec.openapis.org/oas/v3.1.0)
[![License](https://img.shields.io/badge/license-MPL--2.0-blue.svg)](LICENSE)

**Tollgate** is a high-performance, lightweight API key management and rate-limiting reverse proxy gateway designed for multi-tenant SaaS and microservices architectures.  
It provides end-to-end API key lifecycle management, sliding-window RPM rate limiting, monthly quotas, and dynamic multi-target reverse proxy forwarding within a single Go binary and container.

Positioned in front of microservices and public APIs, Tollgate centralizes client authentication, multi-tenant isolation, and traffic shaping.

---

## Key Features

- **Multi-Storage & Zero-Dependency Modes**:
  - **Amazon DynamoDB**: Fully managed on AWS with sparse GSI indexing.
  - **Azure Cosmos DB**: Fully managed NoSQL on Azure, optimized for 1 RU Point Reads.
  - **Google Cloud Firestore**: GCP native NoSQL with atomic transactions.
  - **SQLite**: Pure Go (CGO-free). Runs immediately as a standalone single binary with zero external containers (ideal for local development, PoC, and edge deployments).
- **Flexible Rate Limiting Engines**:
  - **In-Memory**: Ultra-low-latency sliding window counters (automatically selected in SQLite mode).
  - **Two-Tier (Hybrid)**: L1 local in-memory gatekeeping + L2 distributed Redis/Valkey synchronization.
  - **Redis / Valkey**: Distributed shared sliding window for horizontal scale-out.
  - **DynamoDB**: Managed atomic counters on AWS.
- **Dynamic Multi-Target Reverse Proxy**:
  - Forwards requests to target microservices based on prefix matching (`PROXY_ROUTES` JSON or `ROUTES_CONFIG_FILE`).
  - Verifies key validity, checks scopes (e.g. `billing:*`), consumes rate limits, and strips/injects required headers.
- **Strict Multi-Tenant Security & Context Preservation**:
  - Automatically injects validated `X-Tenant-ID`, `X-Service-ID`, `X-Key-ID`, and `X-Key-Prefix` into downstream requests.
  - Rejects header conflicts (client-supplied `X-Tenant-Id` mismatch with key metadata) with `403 Forbidden` (Fail-Fast).
  - Preserves user context (`X-User-Id`, etc.) for internal service keys while stripping them for external tenant keys to prevent spoofing.
  - Preserves incoming `X-Request-Id` or generates a UUID automatically if missing.
- **Zero-Downtime Key Rotation**:
  - Built-in dual-validity grace periods allow seamless rotation with zero service disruption.
- **Instant Tenant Revocation**:
  - Idempotent administrative API (`POST /v1/admin/tenants/{tenant_id}/revoke`) invalidates all keys belonging to an offboarded tenant in a single operation.
- **High Performance & Observability**:
  - Built on [Huma v2](https://huma.rocks/) (OpenAPI 3.1 & interactive Scalar UI auto-generation).
  - Low GC overhead, connection pooling (`MaxIdleConns: 1024`), and native Prometheus `/metrics`.
  - Kubernetes & ECS readiness (`/readyz`) and liveness (`/livez`) probes.

---

## Architecture

```
[ Client / Web Browser / Mobile App / External Service ]
                      │
                      │  Authorization: Bearer tlge-live-...
                      ▼
┌─────────────────────────────────────────────────────────────┐
│                      Tollgate (Gateway)                     │
│                                                             │
│   1. API Key Verification & In-Memory Cache (TTL: 10s)      │
│   2. Scope Authorization (e.g., billing:read)               │
│   3. Sliding Window Rate Limiting (RPM Check)               │
│   4. Monthly Quota Check & Atomic Increment                 │
│   5. Security Context Injection (X-Tenant-ID, etc.)         │
│   6. User Context Filtering & X-Request-Id Tracing          │
└──────────────┬──────────────────────────────┬───────────────┘
               │                              │
     Path: /users/*                 Path: /billing/*
               ▼                              ▼
  ┌─────────────────────────┐    ┌─────────────────────────┐
  │      users-service      │    │     billing-service     │
  │     (:8000/v1/users)    │    │    (:8000/v1/invoices)  │
  └─────────────────────────┘    └─────────────────────────┘
```

---

## API Reference

### 1. Key Management API (`/v1/admin/keys`)
Requires master administrative secret in `Authorization: Bearer <ADMIN_API_KEY>` or `X-Admin-Key`.

| Method | Endpoint | Description |
|:---|:---|:---|
| `POST` | `/v1/admin/keys` | Create a new API key (returns plain-text key once) |
| `GET` | `/v1/admin/keys` | List API keys (supports `tenant_id` and `service_id` filters) |
| `GET` | `/v1/admin/keys/{key_id}` | Retrieve key metadata |
| `PATCH` | `/v1/admin/keys/{key_id}` | Update key properties (`name`, `rate_limit_rpm`, `monthly_quota`) |
| `POST` | `/v1/admin/keys/{key_id}/suspend` | Suspend API key (temporary deactivation) |
| `POST` | `/v1/admin/keys/{key_id}/resume` | Resume suspended API key |
| `POST` | `/v1/admin/keys/{key_id}/rotate` | Zero-downtime key rotation (issues new key with grace period) |
| `DELETE` | `/v1/admin/keys/{key_id}` | Permanently delete and invalidate key |
| `POST` | `/v1/admin/tenants/{tenant_id}/revoke` | Bulk revoke all keys for a tenant (idempotent offboarding) |

### 2. Key Verification API (`/v1/admin/verify`)
| Method | Endpoint | Description |
|:---|:---|:---|
| `POST` | `/v1/admin/verify` | Direct verification and quota consumption endpoint for backend services |

### 3. Reverse Proxy (`/*`)
| Method | Endpoint | Description |
|:---|:---|:---|
| `ANY` | `/*` | Transparent proxy forwarding to routes defined in `PROXY_ROUTES` |

### 4. Operations & Observability
| Method | Endpoint | Description |
|:---|:---|:---|
| `GET` | `/livez` | **Liveness probe** (instant 200 OK process health) |
| `GET` | `/readyz` | **Readiness probe** (database connectivity health) |
| `GET` | `/healthz` | Overall health status |
| `GET` | `/metrics` | Prometheus metrics endpoint |
| `GET` | `OPENAPI_PATH` | OpenAPI 3.1 specification JSON (e.g. `/openapi.json`) |
| `GET` | `DOCS_PATH` | Interactive Scalar API documentation UI (e.g. `/docs`) |

---

## Configuration

Key environment variables (see [`.env.example`](.env.example) for details):

| Variable | Default | Required | Description |
|:---|:---|:---:|:---|
| `ADMIN_API_KEY` | *(empty)* | Recommended | Master secret protecting `/v1/admin/*`. Rejects with 401 if unset (Fail-Fast) |
| `DB_BACKEND` | `dynamodb` | Optional | Storage engine: `dynamodb`, `sqlite`, `cosmosdb`, `firestore` |
| `SQLITE_PATH` | `./tollgate.db` | Optional | SQLite database file path (when `DB_BACKEND=sqlite`) |
| `RATE_LIMIT_BACKEND` | `memory` | Optional | Rate limit engine: `memory`, `redis` / `valkey`, `two-tier`, `dynamodb` |
| `REDIS_ADDR` | `redis:6379` | Optional | Redis / Valkey host & port |
| `VALKEY_URL` | *(empty)* | Optional | Full Redis / Valkey URL connection string |
| `PORT` | `8000` | Optional | HTTP listen port |
| `PROXY_ROUTES` | *(empty)* | Optional | Dynamic routing rules JSON array string |
| `ROUTES_CONFIG_FILE` | *(empty)* | Optional | Path to routing configuration file |
| `KEY_CACHE_TTL` | `10` | Optional | In-memory key verification cache TTL in seconds (`0` to disable) |
| `OPENAPI_PATH` | *(empty)* | Optional | Path to expose OpenAPI 3.1 schema (e.g. `/openapi.json`) |
| `DOCS_PATH` | *(empty)* | Optional | Path to expose Scalar documentation UI (e.g. `/docs`) |

---

## Quick Start

### 1. Run with SQLite (Zero External Dependencies)

```bash
# Set admin key and use embedded SQLite
export ADMIN_API_KEY="dev-secret-key"
export DB_BACKEND="sqlite"
export RATE_LIMIT_BACKEND="memory"

go run ./cmd/server/main.go
```

### 2. Run with Docker Compose (DynamoDB + Mock Upstream)

```bash
# Start all containers
docker compose --profile dynamodb up -d --build tollgate mock-server dynamodb dynamodb-init
# (Or using nerdctl: nerdctl compose --profile dynamodb up -d --build tollgate mock-server dynamodb dynamodb-init)
```

### 3. Issue API Key & Send Traffic

```bash
# 1. Create an API key
curl -X POST http://localhost:8080/v1/admin/keys \
  -H "Authorization: Bearer admin-secret-key-for-local-dev" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Billing Client Key",
    "tenant_id": "tenant-corp-01",
    "service_id": "billing-service",
    "scopes": ["billing:*"],
    "rate_limit_rpm": 600
  }'

# 2. Call upstream service through Tollgate
curl -X GET http://localhost:8080/billing/test \
  -H "Authorization: Bearer <RAW_KEY>"
```

---

## Testing & Benchmarks

### Unit, Integration, & Contract Tests

```bash
# Run all tests (unit tests + in-memory contract tests)
go test -v ./...

# Run without cache
go test -v -count=1 ./...

# Run with race detector
go test -race ./...

# Run in-memory contract test suite
go test -v -run TestContract_SecurityAndMultiTenant ./tests

# Run black-box E2E contract tests against live container
E2E_BASE_URL="http://localhost:8080" go test -v -run TestE2E_ContractVerification ./tests
```

### High-Load Benchmarking (`cmd/bench`)

Tollgate includes an embedded benchmark tool capable of running 1,000,000+ requests:

```bash
# In-process benchmark (SQLite memory + in-memory limiter)
go run ./cmd/bench/main.go -n 1000000 -c 50

# Benchmark live instance
go run ./cmd/bench/main.go -url http://localhost:8080/billing/test -key <RAW_KEY> -n 100000 -c 100
```

---

## Documentation & Guides

- [Backend Integration Specification (docs/backend_integration.md)](docs/backend_integration.md) ([日本語版](docs/ja/backend_integration.md))
- [Contract Testing & CI Guide (docs/contract_testing_and_ci.md)](docs/contract_testing_and_ci.md) ([日本語版](docs/ja/contract_testing_and_ci.md))
- [AWS ECS Fargate Deployment Guide (docs/fargate_deployment_guide.md)](docs/fargate_deployment_guide.md) ([日本語版](docs/ja/fargate_deployment_guide.md))
- [Versioning & Compatibility Policy (docs/versioning_and_compatibility.md)](docs/versioning_and_compatibility.md) ([日本語版](docs/ja/versioning_and_compatibility.md))
- [Shared Secret Rotation Guide (docs/secret_rotation_guide.md)](docs/secret_rotation_guide.md) ([日本語版](docs/ja/secret_rotation_guide.md))
- [Storage & Rate Limiting Architecture (docs/storage_and_ratelimit_architecture.md)](docs/storage_and_ratelimit_architecture.md) ([日本語版](docs/ja/storage_and_ratelimit_architecture.md))
- [Contribution Guidelines (CONTRIBUTING.md)](CONTRIBUTING.md)
- [Security Policy (SECURITY.md)](SECURITY.md)

---

## License

This project is licensed under the [Mozilla Public License 2.0 (MPL-2.0)](LICENSE).
