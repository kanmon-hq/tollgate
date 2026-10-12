# ストレージおよびレート制限アーキテクチャ選定方針 (Storage & Rate Limiting Architecture)

[English](../storage_and_ratelimit_architecture.md) | **日本語**

本書は、Tollgate のデータストア（DB バックエンド）およびレートリミットエンジンの技術選定基準と、将来的な移行方針を定める（要件 C-6）。

---

## 1. 結論と初期フェーズ採用方針

**初期フェーズ（プロダクト導入〜初期運用）は「DynamoDB + インメモリ（Local Memory）」で開始する。**

### 選定理由
1. **インフラ構成の最小化とコスト削減**:
   - 初期段階で ElastiCache (Valkey / Redis) クラスターを常時起動すると、VPC サブネット、セキュリティグループ、NAT ゲートウェイ、および常時課金ノードの運用・保守コストが発生する。
   - DynamoDB（オンデマンド / PAY_PER_REQUEST）と ECS Fargate のインメモリを利用することで、外部キャッシュクラスター不要の最小構成で即座に運用を開始できる。
2. **十分なスループットと性能**:
   - Tollgate はキー検証結果をインメモリキャッシュ（`KEY_CACHE_TTL=10` 秒、設定可能）で保持するため、DynamoDB への読み取り負荷は劇的に削減される。
   - インメモリレートリミッター（Token Bucket）はマイクロ秒未満で判定が完了し、極めて低レイテンシ。
3. **将来の移行容易性（疎結合設計）**:
   - Tollgate はすでにリポジトリ層・レートリミッター層が Go の interface で完全に抽象化されており、コード変更ゼロ・環境変数の切り替えのみで Valkey / Redis へ移行可能。

---

## 2. フェーズ別アーキテクチャ比較

| 項目 | フェーズ 1: 初期構成 (DynamoDB + Memory) | フェーズ 2: 大規模・分散構成 (DynamoDB + Valkey) |
|---|---|---|
| **DB バックエンド** | Amazon DynamoDB (オンデマンド) | Amazon DynamoDB (オンデマンド / プロビジョンド) |
| **レートリミット** | `RATE_LIMIT_BACKEND=memory` (各タスク内メモリ) | `RATE_LIMIT_BACKEND=valkey` (ElastiCache Serverless) |
| **キャッシュ共有** | タスク個別 (ローカルインメモリ) | 全タスク共有 (分散 Valkey Sliding Window) |
| **常時インフラコスト** | **ほぼゼロ (従量課金のみ)** | ElastiCache 月額費用が発生 |
| **主なユースケース** | ECS タスク数 1〜数台、中規模トラフィック | ECS タスク多数、厳密なクラスター横断レート制限が必要 |

---

## 3. ElastiCache (Valkey) への移行判断基準 (Triggers)

以下のいずれかの条件に達した時点で、ElastiCache Valkey（または Redis）の導入を実施する。

1. **ECS Fargate タスクの水平スケール数**:
   - タスク数が 5 台以上になり、タスク間でレート制限カウンタを厳密に合算・共有する必要性が生じた場合。
   - （※タスク個別レート制限でも許容できる場合は、タスク数 × RPM の設計で継続可能）。
2. **秒間リクエスト数 (RPS) の増大**:
   - ピークトラフィックが数千〜数万 RPS に達し、クラスター全体でのミリ秒以下の厳密な Sliding Window レート制御が求められた場合。
3. **マルチリージョン展開**:
   - キャッシュの同期や分散データ共有が必要となった場合。

---

## 4. Valkey への切り替え手順 (ゼロコード変更)

移行時は、アプリケーションコードの修正やビルドは一切不要。ECS タスク定義の環境変数を更新するだけで完了する。

```bash
# 1. ElastiCache (Valkey) を作成後、環境変数を設定
RATE_LIMIT_BACKEND=valkey
VALKEY_URL=redis://tollgate-valkey.xxxx.cache.amazonaws.com:6379/0

# (または個別指定)
# REDIS_ADDR=tollgate-valkey.xxxx.cache.amazonaws.com:6379
# REDIS_PASSWORD=
# REDIS_DB=0
```

Tollgate の ECS サービスをローリング再デプロイすることで、ダウンタイムゼロで分散レート制限構成へ移行できる。
