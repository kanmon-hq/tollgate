# Storage & Rate Limiting Architecture Decision Policy (Requirement C-6)

**English** | [日本語](ja/storage_and_ratelimit_architecture.md)

This document details the architectural criteria and transition strategy for Tollgate's data persistence backends and rate limiting engines.

---

## 1. Storage Backend Selection

Tollgate supports multiple database backends to accommodate various hosting models and scalability tiers:

| Backend | Primary Use Case | Performance & Characteristics |
|:---|:---|:---|
| **Amazon DynamoDB** | AWS Production Default | Serverless, zero maintenance overhead, predictable sub-10ms point reads with Global Secondary Indexes (GSIs). |
| **Azure Cosmos DB** | Azure Production Default | Global distribution, Point Read optimized (1 RU latency < 2ms). |
| **Google Cloud Firestore** | GCP Production Default | Serverless document store with atomic transactions. |
| **SQLite (Pure Go)** | Local Dev, PoC, Single-Node | Runs in-process with zero external containers or CGO compilation prerequisites. |

---

## 2. Rate Limiting Strategy: Initial Phase vs. High Scale

### Phase 1: In-Memory / DynamoDB Simplicity
- **Initial Deployment**:
  `RATE_LIMIT_BACKEND=memory` (or `dynamodb`).
- **Rationale**:
  Avoids the operational overhead, VPC peering, and baseline cost of managing an ElastiCache cluster during initial launch. With in-process sliding window tracking, latency is sub-millisecond.

### Phase 2: Scale-Out with Redis / Valkey
- **Horizontal Scaling**:
  When Tollgate scales out across multiple container tasks requiring global rate limit consistency, configure:
  `RATE_LIMIT_BACKEND=redis` (or `two-tier`) and `VALKEY_URL=redis://...`.
- **Zero-Code Migration**:
  Switching the rate limiting engine is entirely configuration-driven via environment variables without code modification or schema migrations.

---

## 3. Two-Tier Hybrid Limiting

For ultra-high-throughput endpoints where querying Redis on every request could saturate cache connection limits, Tollgate provides a **Two-Tier Limiter** (`RATE_LIMIT_BACKEND=two-tier`):
1. **L1 Local Memory**: Rejects obvious spikes locally in memory before hitting network caches.
2. **L2 Valkey / Redis**: Periodically synchronizes global consumption across instances.
