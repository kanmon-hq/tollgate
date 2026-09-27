# Tollgate — Lightweight API Key Management & Rate Limiting Gateway

[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://golang.org/)
[![Huma v2](https://img.shields.io/badge/Huma-v2.39+-8A2BE2.svg)](https://huma.rocks/)
[![DynamoDB](https://img.shields.io/badge/Storage-DynamoDB%20%7C%20CosmosDB%20%7C%20Firestore%20%7C%20SQLite-4053D6.svg)](https://aws.amazon.com/dynamodb/)
[![Redis / Valkey](https://img.shields.io/badge/RateLimit-Memory%20%7C%20Redis%20%7C%20Valkey%20%7C%20Two--Tier-DC382D.svg)](https://valkey.io/)
[![Prometheus](https://img.shields.io/badge/Prometheus-Metrics-E6522C.svg?logo=prometheus)](https://prometheus.io/)
[![OpenAPI](https://img.shields.io/badge/OpenAPI-3.1-6BA539.svg?logo=openapiinitiative)](https://spec.openapis.org/oas/v3.1.0)
[![License](https://img.shields.io/badge/license-MPL--2.0-blue.svg)](LICENSE)

**Tollgate** は、マルチテナント SaaS・マイクロサービス基盤向けの軽量な API キー管理 & レートリミッティング・リバースプロキシである。  
マルチテナントに対応した API キー発行・ライフサイクル管理、スライディングウィンドウ方式による RPM 流量制御、月間クォータ管理、および動的マルチターゲット・リバースプロキシを単一バイナリ / コンテナで完結させる。

各種マイクロサービスや外部公開 API の手前に配置し、クライアント認証・テナント分離・流量制御を一元管理する認証ゲートウェイとして機能する。

---

## 主な機能

- **マルチストレージ & ゼロ依存起動モード**:
  - **DynamoDB**: AWS 完全マネージド、GSI スパースインデックス対応。
  - **Azure Cosmos DB**: Azure 完全マネージド NoSQL、Point Read (1 RU) 最適化。
  - **Google Cloud Firestore**: GCP ネイティブ NoSQL、アトミックトランザクション対応。
  - **SQLite**: CGO 不要ピュア Go 実装。外部コンテナなし・バイナリ 1 本で即座に起動可能（開発・PoC・シングルノード用途に最適）。
- **柔軟なレートリミットバックエンド**:
  - **In-Memory**: 超低遅延なスライディングウィンドウカウンター（SQLite モード時は自動固定）。
  - **2段キャッシュ (Two-Tier)**: L1 ローカルインメモリ判定 ＋ L2 分散 Redis/Valkey 同期のハイブリッド階層化制御。
  - **Redis / Valkey**: 分散スケールアウト環境向けの共有スライディングウィンドウ。
  - **DynamoDB**: AWS 完全マネージドなアトミックカウンター。
  - **None (なし)**: レートリミットをバイパスし、月間クォータのみを制御する運用モード。
- **動的マルチターゲット・リバースプロキシ**:
  - パスプレフィックス（`/users`, `/billing`, `/analytics` 等）に基づき、各下流サービスへ自動ルーティング。
  - ルーティング単位での Prefix Stripping、スコープ検証（`users:read`, `billing:write` 等）を自動実行。
  - プロキシ専用 HTTP トランスポートチューニングによる高並行・低遅延通信（コネクションプール最適化、TIME_WAIT 枯渇抑止）。
- **堅牢なマルチテナント分離 & コンテキスト注入**:
  - **テナントキー**: `tenant_id` と `service_id` を保持。キーの `tenant_id` と `service_id` を下流へ `X-Tenant-ID` / `X-Service-ID` として確実に注入。クライアント指定値とのコンフリクト時は `403 Forbidden` で即座に遮断（Fail-Fast）。
  - **サービスキー**: `service_id` を保持し、クライアントが指定した動的 `X-Tenant-ID` を透過フォワード（未指定時は `400 Bad Request`）。下流にサービス識別子（`X-Service-ID`）を注入。
- **高スループット・レートリミット & クォータ制御**:
  - **分間レートリミット (RPM)**: スライディングウィンドウカウンターによる超低レイテンシなリアルタイム流量制限。
  - **月間クォータ**: アトミックカウンターによる月間利用回数の確実な集計と上限超過検知。
- **ゼロダウンタイム・キーローテーション**:
  - `POST /v1/admin/keys/{key_id}/rotate` により、旧キーの失効猶予期間（Grace Period）を保ちながら新キーを発行。クライアント側の無停止キー切り替えを支援。
- **クラウドネイティブ・オブザーバビリティ**:
  - **Kubernetes 標準プローブ**: `/livez`（Liveness）、`/readyz`（Readiness / DB 接続確認）、`/healthz`（総合確認）。
  - **Prometheus メトリクス**: `/metrics` で各種リクエスト数・レイテンシを公開。
  - **OpenAPI 3.1 & ドキュメント UI**: Huma v2 による OpenAPI 仕様（`/openapi.json`）および Scalar UI（`/docs`）の内蔵。
- **セキュアな設計原則 (Fail-Fast)**:
  - 平文 API キーは一切保存せず、SHA-256 ダイジェストのみを永続化。平文キーは発行時・ローテーション時に 1 度だけ返却。
  - デフォルトのフォールバックシークレットをコード内にハードコードせず、未設定時は起動時・検証時に即座にエラーとする安全設計。

---

## ストレージ・レートリミット別の役割マトリクス

Tollgate は、キーの永続化・月間クォータ集計・リアルタイム分間レートリミット（RPM）を用途や環境に合わせて柔軟に組み合わせて構成できる。

| バックエンド | キー永続化 | 月間クォータ計数 | 分間レートリミット (RPM) | 分散スケールアウト | 外部コンテナ依存 | 推奨ユースケース |
|:---|:---:|:---:|:---:|:---:|:---:|:---|
| **SQLite** (`modernc.org/sqlite`) | ✅ | ✅ (SQL Atomic) | ❌ | ❌ (単一ノード) | **なし (0個)** | **ローカル開発・PoC・単一バイナリ即起動** |
| **DynamoDB** (AWS SDK v2) | ✅ | ✅ (`ADD` Atomic) | ✅ (Fixed Window) | ✅ | あり (AWS / Local) | **AWS サーバーレス・フルマネージド環境** |
| **Azure Cosmos DB** (Azure SDK) | ✅ | ✅ (Patch/Upsert) | ❌ | ✅ | あり (Azure) | **Azure クラウド・グローバル分散環境** |
| **Google Cloud Firestore** (GCP SDK) | ✅ | ✅ (Tx / Atomic) | ❌ | ✅ | あり (GCP / Local) | **GCP サーバーレス・フルマネージド環境** |
| **Redis / Valkey** (`go-redis/v9`) | ❌ | ❌ | ✅ (Sliding Window) | ✅ | あり (1個) | **分散環境での高精度・低遅延レートリミット** |
| **2段キャッシュ (Two-Tier)** | ❌ | ❌ | ✅ (L1 Memory + L2 Redis) | ✅ | あり (Redis) | **超高トラフィック・Redis 負荷最小化環境** |
| **In-Memory** | ❌ | ❌ | ✅ (Sliding Window) | ❌ (ノードローカル) | **なし (0個)** | **SQLite 起動時・単一インスタンス環境** |
| **None (なし)** | ❌ | ❌ | ❌ (バイパス) | ✅ | **なし (0個)** | **レートリミット無効化・クォータ専有構成** |

### 推奨バックエンド構成

| 構成パターン | `DB_BACKEND` | `RATE_LIMIT_BACKEND` | キャッシュ層 | 特徴・メリット |
|:---|:---|:---|:---:|:---|
| **① ゼロ依存・スタンドアロン** | `sqlite` | `memory` または `none` | なし (ダイレクト) | **外部コンテナ一切不要**。バイナリ 1 本で即時起動。開発・テスト・エッジ用途に最適。 |
| **② AWS フルマネージド構成** | `dynamodb` | `dynamodb`, `redis`, `two-tier` | あり (TTL) | インフラ運用コスト最小化。DynamoDB のみでキー管理・クォータ・RPM を完結。 |
| **③ Azure マネージド構成** | `cosmosdb` | `redis` または `two-tier` | あり (TTL) | Azure Cosmos DB による高速永続化 + 2段キャッシュ/Redis による高スループット制御。 |
| **④ GCP マネージド構成** | `firestore` | `redis` または `two-tier` | あり (TTL) | Google Cloud Firestore 連携 + 分散レートリミット。 |
| **⑤ 超高トラフィック分散構成** | `dynamodb` / `cosmosdb` / `firestore` | `two-tier` | あり (TTL) | L1 ローカルインメモリで Redis 負荷を劇的低減しつつクラスタ全体で流量制御。 |

---

## 分散 RPM レートリミットの精度と耐障害性

### 1. スライディングウィンドウの実装方式（Redis ZSET + Lua）
固定ウィンドウ（Fixed Window）方式では、ウィンドウの切り替わり境界（例: 00:59 と 01:00）の前後で一時的に制限値の最大 2 倍のリクエストが通過してしまう「境界バースト問題」が存在する。

Tollgate の Redis バックエンドでは、**Redis Sorted Set (ZSET) と Lua スクリプト**を用いたミリ秒精度のスライディングログアルゴリズムを採用している。

```mermaid
sequenceDiagram
    autonumber
    actor Client
    participant TG as Tollgate
    participant Redis as Redis (Lua Script)

    Client->>TG: API Request
    TG->>Redis: EVALSHA slidingWindowLuaScript (Key, Window, Now, Limit)
    Note over Redis: 1. ZREMRANGEBYSCORE (過去60秒前の期限切れ削除)<br/>2. ZCARD (現ウィンドウ内のリクエスト数カウント)<br/>3. limit判定 (超過なら 429 判定)<br/>4. ZADD (現在時刻ミリ秒を記録)<br/>5. PEXPIRE (キーの有効期限更新)
    Redis-->>TG: [allowed, remaining, reset_in_ms]
    alt 制限内 (allowed=true)
        TG->>Client: 200 OK (下流サービスへ転送)
    else 超過 (allowed=false)
        TG->>Client: 429 Too Many Requests (Retry-After ヘッダー付与)
    end
```

- **完全アトミック実行**: Redis サーバー内で Lua スクリプトとして一連の判定・記録・TTL 更新がアトミックに実行されるため、分散複数ノードからの並行アクセスでも競合（Race Condition）が発生しない。
- **メモリ効率化**: 古いタイムスタンプは毎回自動削除（`ZREMRANGEBYSCORE`）され、キー自体も 60 秒 + バッファで自動失効（`PEXPIRE`）するためメモリリークが発生しない。

### 2. Redis 障害時のフェイルオープン設計 (Fail-Open)
レートリミット用 Redis に一時的な接続障害やタイムアウトが発生した場合、Tollgate は **フェイルオープン（Fail-Open: `allowed=true`）** として動作する。

- **可用性の最優先**: レートリミッターの一時的な障害によって、正常な正規ユーザーの全リクエストが 500/503 エラーで巻き添え停止（Fail-Closed）することを防止する。
- **監視ログ・メトリクス**: フェイルオープン発生時はエラーログを記録し、Prometheus メトリクス経由でアラート検知が可能。

---

## 類似 OSS との比較

> [!NOTE]
> 下記は各プロジェクトの公開情報に基づく比較です。各プロジェクトの最新仕様は公式ドキュメントを参照してください。

| 比較項目 | **Tollgate** | **Kong Gateway (OSS)** | **Tyk Gateway (OSS)** | **Unkey** |
|:---|:---:|:---:|:---:|:---:|
| **ライセンス** | **MPL-2.0** | Apache 2.0 | MPL 2.0 | Apache 2.0 / BSL |
| **最小外部依存数** | **0 個 (SQLite モード)**<br>1 個 (DynamoDB / Cosmos DB / Firestore) | 1 個〜 (PostgreSQL / DB-less) | 1 個 (Redis 必須) | 1 個〜 (MySQL 互換 DB) |
| **単一バイナリ実行** | **✅ 完全対応** (CGO 不要) | ❌ (OpenResty/Lua 環境) | ❌ (Redis 必須) | ❌ (Docker / Node / DB 必須) |
| **リバースプロキシ機能** | **✅ 内蔵** (動的マルチターゲット) | ✅ 内蔵 | ✅ 内蔵 | △ (検証 API / SDK 主軸) |
| **マルチテナント認可** | **✅ ネイティブ**<br>(テナントキー / サービスキー二段構成) | △ (プラグイン設定で実現) | △ (プラグイン設定で実現) | △ (キー単位での管理) |
| **エディション分割** | **なし (OSS 単一ですべての機能を提供)** | あり (Enterprise 版あり) | あり (Enterprise 版あり) | あり (Cloud / Enterprise) |
| **実装言語** | **Go 1.24+ (ピュア Go)** | Lua / OpenResty / C | Go / C | TypeScript / Next.js / Rust |

---

## アーキテクチャ

```mermaid
flowchart TD
    Client["Client / Frontend / Microservice"]
    
    subgraph TollgateCluster ["Tollgate (API Gateway : 8080)"]
        PROXY["Multi-Target Reverse Proxy"]
        
        subgraph Engine ["Core Engine"]
            VERIFY["API Key Verifier<br/>(SHA-256 Hash Matching)"]
            LIMITER["Rate Limiter<br/>(Memory / Redis / Two-Tier / DynamoDB / None)"]
            CACHE["Key Metadata Cache<br/>(TTL Cache)"]
            HEADER_INJECT["Context Injector<br/>(X-Tenant-ID / X-Service-ID / X-Key-ID)"]
        end

        subgraph ManagementAPI ["Management API (Huma v2 - Protected by ADMIN_API_KEY)"]
            KEY_MGMT["Key Lifecycle API<br/>• POST /v1/admin/keys<br/>• POST /v1/admin/keys/{id}/rotate<br/>• POST /v1/admin/verify"]
            HEALTH["Health & Probe Handlers<br/>• /livez<br/>• /readyz<br/>• /healthz"]
            METRICS["Prometheus<br/>• /metrics"]
        end
    end

    subgraph Storage ["Storage Layer (Selectable)"]
        DB[("Database<br/>• SQLite (Single Binary)<br/>• DynamoDB (AWS)<br/>• Cosmos DB (Azure)<br/>• Firestore (GCP)")]
    end

    subgraph Downstream ["Downstream Services (下流マイクロサービス群)"]
        SVC_A["Users Service<br/>(例: Prefix /users 転送先)"]
        SVC_B["Billing Service<br/>(例: Prefix /billing 転送先)"]
        SVC_N["Any Microservices...<br/>(PROXY_ROUTES 定義先)"]
    end

    %% Client flows
    Client -->|"API Request (Bearer tlge-live-...)"| PROXY
    Client -->|"Admin Request (Bearer ADMIN_API_KEY)"| KEY_MGMT

    %% Internal Tollgate flows
    PROXY --> VERIFY
    VERIFY --> CACHE
    CACHE -.->|"Cache Miss"| DB
    VERIFY --> LIMITER
    LIMITER -.->|"Quota Increment"| DB
    PROXY --> HEADER_INJECT

    %% Forwarding
    HEADER_INJECT -->|"Prefix /users マッチ (X-Tenant-ID 注入 / StripPrefix)"| SVC_A
    HEADER_INJECT -->|"Prefix /billing マッチ (X-Tenant-ID 注入 / StripPrefix)"| SVC_B
    HEADER_INJECT -->|"動的ルーティング転送"| SVC_N

    KEY_MGMT --> DB
    HEALTH -.->|"Ping"| DB
```

---

## ディレクトリ構成

```text
tollgate/
├── cmd/
│   └── server/                     # アプリケーションエントリポイント (main.go)
├── internal/
│   ├── config/                     # 環境変数・ルーティング設定ローダー
│   ├── delivery/
│   │   └── http/                   # HTTP ハンドラー、ルーティング、プロキシ実装
│   │       ├── api.go              # Huma v2 ルーター & 共通ミドルウェア初期化 (ADMIN_API_KEY 認証)
│   │       ├── health_handler.go   # /livez, /readyz, /healthz ハンドラー
│   │       ├── key_handler.go      # /v1/admin/keys CRUD & ローテーション API
│   │       ├── metrics_handler.go  # Prometheus /metrics ハンドラー
│   │       ├── proxy_handler.go    # 動的マルチターゲット・リバースプロキシ
│   │       └── verify_handler.go   # /v1/admin/verify 内部検証 API
│   ├── domain/
│   │   ├── entity/                 # ドメインモデル (APIKey, CreateKeyInput 等)
│   │   └── repository/             # リポジトリインターフェース定義
│   ├── infrastructure/
│   │   ├── cache/                  # キーメタデータ TTL インメモリキャッシュ
│   │   ├── dynamodb/               # DynamoDB SDK クライアント実装 & スパースインデックス
│   │   ├── metrics/                # Prometheus メトリクス定義
│   │   └── ratelimit/              # スライディングウィンドウ・レートリミッター
│   └── usecase/                    # キー管理 & 検証ユースケースロジック
├── docs/                           # 詳細仕様ドキュメント
│   └── backend_integration.md      # 下流サービス連携 & ヘッダー解決規約
├── compose.yaml                    # ローカル開発・検証用 Docker Compose 定義
├── Dockerfile                      # マルチステージビルド Dockerfile
├── CONTRIBUTING.md                 # コントリビューションガイド
├── SECURITY.md                     # セキュリティポリシー
└── README.md                       # 本ドキュメント
```

---

## API エンドポイント一覧

### 1. 管理用 API キー操作 (`/v1/admin/keys`)
> [!IMPORTANT]
> `/v1/admin/*` 配下のエンドポイントはマスター管理者キー（`Authorization: Bearer <ADMIN_API_KEY>`）による認証が必須です。

| メソッド | パス | 説明 |
|:---|:---|:---|
| `POST` | `/v1/admin/keys` | 新規 API キー発行（平文キーは本レスポンスのみ開示） |
| `GET` | `/v1/admin/keys` | 指定テナントの API キー一覧取得 (`?tenant_id=...`) |
| `GET` | `/v1/admin/keys/{key_id}` | API キー詳細メタデータ取得 |
| `PATCH` | `/v1/admin/keys/{key_id}` | API キー設定変更（名称、スコープ、レート上限等） |
| `POST` | `/v1/admin/keys/{key_id}/suspend` | API キーの一時停止（即座にリクエスト拒絶） |
| `POST` | `/v1/admin/keys/{key_id}/resume` | 一時停止中 API キーの再開 |
| `POST` | `/v1/admin/keys/{key_id}/rotate` | ゼロダウンタイム・キーローテーション（新キー発行 & 猶予期間設定） |
| `DELETE` | `/v1/admin/keys/{key_id}` | API キーの物理削除・即時失効 |

### 2. キー検証 API (`/v1/admin/verify`)
| メソッド | パス | 説明 |
|:---|:---|:---|
| `POST` | `/v1/admin/verify` | 内部サービス連携用キー検証 & レートリミット / クォータ判定（ADMIN_API_KEY 認証必須） |

### 3. リバースプロキシ (`/*`)
| メソッド | パス | 説明 |
|:---|:---|:---|
| `ANY` | `/*` | `PROXY_ROUTES` にマッチした下流サービスへ認証・ヘッダー付与して透過転送 |

### 4. 運用 & オブザーバビリティ
| メソッド | パス | 説明 |
|:---|:---|:---|
| `GET` | `/livez` | **Liveness プローブ** (プロセスの死活監視、即座に 200 返却) |
| `GET` | `/readyz` | **Readiness プローブ** (DynamoDB 疎通確認、受付準備完了判定) |
| `GET` | `/healthz` | **総合ヘルスチェック** (プロセス生存 + DynamoDB 疎通状態) |
| `GET` | `/metrics` | **Prometheus メトリクス** (リクエスト数、レイテンシ等) |
| `GET` | `OPENAPI_PATH` | OpenAPI 3.1 仕様 JSON (例: `/openapi.json`, 環境変数指定時のみ有効) |
| `GET` | `DOCS_PATH` | Scalar ドキュメント UI (例: `/docs`, 環境変数指定時のみ有効) |

---

## 環境変数設定

主要な環境変数（詳細は [`.env.example`](.env.example) を参照）：

| 変数名 | デフォルト値 | 必須 | 説明 |
|:---|:---|:---:|:---|
| `ADMIN_API_KEY` | *(空)* | 推奨 | 管理用 WebAPI (`/v1/admin/*`) を保護するマスターキー。未設定時は管理 API が 401 で遮断される (Fail-Fast) |
| `DB_BACKEND` | `dynamodb` | 任意 | DB バックエンド (`dynamodb`, `sqlite`, `cosmosdb`, `firestore`) |
| `SQLITE_PATH` | `./tollgate.db` | 任意 | SQLite データベースファイルパス (`DB_BACKEND=sqlite` 時) |
| `COSMOSDB_ENDPOINT` | *(空)* | 任意 | Azure Cosmos DB エンドポイント URL (`DB_BACKEND=cosmosdb` 時) |
| `COSMOSDB_KEY` | *(空)* | 任意 | Azure Cosmos DB プライマリ/セカンダリ キー (`DB_BACKEND=cosmosdb` 時) |
| `COSMOSDB_DATABASE` | `tollgate` | 任意 | Azure Cosmos DB データベース名 |
| `COSMOSDB_CONTAINER`| `api_keys` | 任意 | Azure Cosmos DB コンテナ名 |
| `FIRESTORE_PROJECT_ID` | *(空)* | 任意 | Google Cloud プロジェクト ID (`DB_BACKEND=firestore` 時) |
| `FIRESTORE_DATABASE_ID`| `(default)` | 任意 | Firestore データベース ID |
| `FIRESTORE_COLLECTION` | `api_keys` | 任意 | Firestore コレクション名 |
| `RATE_LIMIT_BACKEND`| `memory` | 任意 | レートリミットバックエンド (`memory`, `redis` / `valkey`, `two-tier`, `dynamodb`, `none`) |
| `REDIS_ADDR` | `redis:6379` | 任意 | Redis / Valkey ホスト・ポート (`RATE_LIMIT_BACKEND=redis/valkey/two-tier` 時) |
| `REDIS_PASSWORD` | *(空)* | 任意 | Redis / Valkey 認証パスワード |
| `REDIS_DB` | `0` | 任意 | Redis / Valkey DB 番号 |
| `PORT` | `8000` | 任意 | HTTP サーバーのリッスンポート |
| `AWS_REGION` | `ap-northeast-1` | 任意 | DynamoDB 接続リージョン |
| `TABLE_NAME` | `TollgateAPIKeys` | 任意 | API キー管理用 DynamoDB テーブル名 |
| `DYNAMODB_ENDPOINT` | *(AWS デフォルト)* | 任意 | DynamoDB Local 等のエンドポイント URL |
| `KEY_CACHE_TTL` | `10` | 任意 | キー検証メタデータのインメモリキャッシュ保持秒数 (`0` で無効化) |
| `PROXY_ROUTES` | *(空)* | 任意 | 動的ルーティング定義 (JSON 配列文字列) |
| `ROUTES_CONFIG_FILE`| *(空)* | 任意 | ルーティング設定ファイルのローカルパス |
| `FORWARD_TARGET_URL`| *(空)* | 任意 | ルート未マッチ時のフォールバック先 URL (未指定時は 404) |
| `OPENAPI_PATH` | *(空)* | 任意 | OpenAPI 3.1 スキーマ公開パス (未指定時は非公開) |
| `DOCS_PATH` | *(空)* | 任意 | Scalar ドキュメント UI 公開パス (未指定時は非公開) |

---

## クイックスタート

### 前提条件
- Docker / nerdctl & Docker Compose
- （ローカル実行時）Go 1.24+

### 1. Docker Compose での一括起動 (推奨)

DynamoDB Local、初期テーブル作成、DynamoDB Admin、および Tollgate をワンコマンドで起動する。

```bash
# 1. 環境変数の準備
cp .env.example .env

# 2. コンテナ起動 (DynamoDB + Admin + Tollgate)
nerdctl compose --profile database up -d --build

# 3. ログ確認
nerdctl compose --profile database logs -f tollgate

# 4. ヘルスチェック確認
curl -i http://localhost:8002/livez
curl -i http://localhost:8002/readyz
```

- **Tollgate ゲートウェイ**: `http://localhost:8080`
- **Scalar ドキュメント**: `http://localhost:8080/docs`
- **DynamoDB Admin UI**: `http://localhost:8001`

#### Prometheus & Grafana メトリクス監視付きで起動する場合
```bash
# database + monitor プロファイルを指定して起動
docker compose --profile database --profile monitor up -d
```
- **Grafana ダッシュボード**: `http://localhost:3000` (ログイン不要・Tollgate Overview ダッシュボード自動読み込み)
- **Prometheus Web UI**: `http://localhost:9090`

### 2. ローカル環境での起動 (Go)

```bash
# 依存解決
go mod download

# サーバー起動
go run cmd/server/main.go
```

---

## API 利用例

### ① テナントキーの発行 (`POST /v1/admin/keys`)

```bash
curl -X POST http://localhost:8080/v1/admin/keys \
  -H "Authorization: Bearer admin-secret-key-for-local-dev" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Billing Service Client",
    "tenant_id": "tenant-corp-01",
    "service_id": "billing-service",
    "scopes": ["billing:read", "billing:write"],
    "rate_limit_rpm": 600,
    "monthly_quota": 100000
  }'
```

**レスポンス**:
```json
{
  "key_id": "55d20ba0-d2e7-495b-a1c9-af33bfdf8f55",
  "key_prefix": "tlge-live-ca20",
  "name": "Billing Service Client",
  "tenant_id": "tenant-corp-01",
  "service_id": "billing-service",
  "scopes": ["billing:read", "billing:write"],
  "rate_limit_rpm": 600,
  "monthly_quota": 100000,
  "status": "active",
  "raw_key": "tlge-live-ca20ceeb4561aef616268e2716ebb264"
}
```
> [!IMPORTANT]
> `raw_key` はキー生成時に 1 度だけ返却されます。安全なシークレットストアへ保管してください。

### ② ゲートウェイ経由のリクエスト転送

発行した API キーを `Authorization: Bearer <raw_key>` に指定してリクエストを送信する。

```bash
curl -X POST http://localhost:8080/billing/v1/invoices \
  -H "Authorization: Bearer tlge-live-ca20ceeb4561aef616268e2716ebb264" \
  -H "Content-Type: application/json" \
  -d '{"amount": 50000, "currency": "JPY"}'
```

Tollgate がキー検証・RPM レートリミット消費・クォータ加算・スコープ（`billing:write` 等）認可を実行し、Prefix (`/billing`) を除去した上で `http://billing-service:8000/v1/invoices` へ転送。下流サービスへ `X-Tenant-ID: tenant-corp-01` および `X-Service-ID: billing-service` を自動注入する。

---

## テスト・ベンチマーク実行

### 単体・結合テスト

```bash
# 全テスト実行
go test -v ./...

# キャッシュを無効化して実行
go test -v -count=1 ./...

# レースコンディション検出付きテスト
go test -race ./...
```

### 高負荷テスト・ベンチマーク (`cmd/bench`)

Tollgate には、100 万リクエスト規模の負荷試験・レイテンシ集計・リソース統計取得を行える組み込みベンチマークツールが付属しています。

```bash
# 1. 最速インプロセス構成（SQLite :memory: + メモリ内リミッター）で 100 万リクエスト実行 (推奨 50〜200 並行)
go run ./cmd/bench/main.go -n 1000000 -c 50

# 2. 起動中の Tollgate 外部インスタンス（Docker Compose 等）に対して実行
go run ./cmd/bench/main.go -url http://localhost:8080/billing/test -key <API_KEY> -n 100000 -c 100
```

#### 主なオプションフラグ

| フラグ | デフォルト値 | 説明 |
| :--- | :--- | :--- |
| `-n` | `1000000` | 総リクエスト送信数 |
| `-c` | `200` | 同時ワーカー並行数 |
| `-url` | `""` (未指定) | 外部ターゲット URL。未指定時は最速インプロセスサーバーを自動起動して測定 |
| `-key` | `""` | 外部ターゲット URL に送信する Bearer API キー |

#### 測定項目
- **スループット**: 秒間リクエスト処理数 (RPS)
- **レイテンシ分布**: Min / p50 / p90 / p99 / Max (ミリ秒)
- **成否ステータス内訳**: 200 OK / 429 Too Many Requests / 5xx エラー / コネクション切断数
- **リソース消費統計**: テスト前後のメモリ使用量 (`Alloc`, `Sys`, `NumGC`) およびアクティブ Goroutine 数


---

## 詳細仕様書 & ガイド

- [下流サービス連携・テナント解決・セキュリティ仕様 (docs/backend_integration.md)](docs/backend_integration.md)
- [コントリビューションガイド (CONTRIBUTING.md)](CONTRIBUTING.md)
- [セキュリティポリシー (SECURITY.md)](SECURITY.md)

---

## ライセンス

本プロジェクトは [Mozilla Public License 2.0 (MPL-2.0)](LICENSE) の下で公開されています。
