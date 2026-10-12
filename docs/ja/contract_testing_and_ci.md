# Tollgate 契約テスト & CI 自動化ガイド (Contract Testing & CI Guide)

[English](../contract_testing_and_ci.md) | **日本語**

本ドキュメントは、要件 **C-5（契約テスト CI）** およびマルチテナント / プロキシセキュリティ仕様の継続的検証を保証するための契約テスト方針と CI 自動化パイプラインの構成をまとめたものである。

---

## 1. 契約テストの目的と検証範囲

Tollgate は認証・認可・レート制限・リバースプロキシを担うため、バックエンドサービスおよび外部クライアントとの間で以下の**厳密な振る舞い契約**を常に満たす必要がある。

| シナリオ | 検証内容 | 準拠要件 | 期待される挙動 |
|:---|:---|:---:|:---|
| **1. サービスキー透過** | BFF などの内部サービスキーからのリクエスト | T-2 | `X-User-Id`, `X-User-Role`, `X-User-Context` 等のユーザー属性ヘッダーを改変せずバックエンドへ透過転送する。 |
| **2. テナントキー偽装防止** | 外部テナントキーからのリクエスト | T-2 | クライアントが自称した `X-User-Id` 等のコンテキストヘッダーを強制除去（Strip）し、スプーフィングを防止する。 |
| **3. テナント越境拒否** | キー属性の `tenant_id` とリクエストの `X-Tenant-Id` 不一致 | セキュリティ方針 | 暗黙の上書きを行わず、`403 Forbidden` (`tenant_mismatch`) で即座に拒絶（Fail-Fast）する。 |
| **4. 共有シークレット不一致** | 不正な API キー、または不正な `ADMIN_API_KEY` | C-5 | プロキシ宛て・管理 API 宛てともに即座に `401 Unauthorized` で拒絶する。 |
| **5. ゼロダウンタイム・ローテーション** | キーローテーション猶予期間中の新旧キー並行稼働 | T-1, C-2 | Grace Period 中は新旧両方のキーで 200 OK となり、ダウンタイムゼロで安全に切り替え可能。 |
| **6. テナント解約一括失効** | `POST /v1/admin/tenants/{tenant_id}/revoke` 実行 | T-4, C-4 | 対象テナントの全キーが即時 `403 Forbidden` となり、他テナントのキーには一切影響を与えない（完全隔離）。 |
| **7. リクエスト ID トレース** | `X-Request-Id` の透過および自動採番 | C-3 | クライアント指定値はそのまま透過し、未指定時は UUID を自動採番してアップストリームおよびレスポンスヘッダーへ付与する。 |

---

## 2. 契約テストの 2 層アーキテクチャ

Tollgate では高速なフィードバックと実環境での信頼性を両立するため、契約テストを 2 層で実装している。

```
[Layer 1: In-Memory Contract Test (tests/contract_test.go)]
  ├── 実行環境: go test (依存なし・SQLite In-Memory + httptest Server)
  ├── 特徴: ミリ秒単位で高速実行、データ競合検出 (-race) 対応
  └── 対象: PR作成時、ローカル開発時の高速フィードバック

[Layer 2: Real Container E2E Contract Test (tests/e2e_contract_test.go)]
  ├── 実行環境: Docker Compose (tollgate + mock-server + dynamodb)
  ├── 特徴: 実際のネットワーク、コンテナ環境、ヘルスチェックを含めたブラックボックス検証
  └── 対象: GitHub Actions CI、ステージング環境デプロイ前
```

### Layer 1: インメモリ契約テスト (`tests/contract_test.go`)
外部ミドルウェア（PostgreSQL や DynamoDB）の起動を待つことなく、SQLite メモリモードと Go の `httptest.Server` を用いて、プロキシエンジン・認証ハンドラー・リポジトリ全体の結合シナリオを完全に自己完結でテストする。

```bash
# インメモリ契約テストの実行
go test -v -run TestContract_SecurityAndMultiTenant ./tests
```

### Layer 2: 実コンテナ E2E 契約テスト (`tests/e2e_contract_test.go`)
実コンテナ（Docker Compose または nerdctl compose）を起動し、公開ポート経由で HTTP リクエストを送信してエンドツーエンドの契約を検証する。環境変数 `E2E_BASE_URL` が指定されている場合のみ実行される。

```bash
# 1. コンテナ環境を起動
docker compose --profile dynamodb up -d --build tollgate mock-server dynamodb dynamodb-init
# (nerdctl を利用する場合: nerdctl compose --profile dynamodb up -d --build tollgate mock-server dynamodb dynamodb-init)

# 2. E2E 契約テストを実行
E2E_BASE_URL="http://localhost:8080" E2E_ADMIN_KEY="dev-secret-key-change-in-production" go test -v -run TestE2E_ContractVerification ./tests

# 3. コンテナを停止
docker compose --profile dynamodb down -v
```

---

## 3. GitHub Actions CI パイプライン構成

`.github/workflows/test.yml` により、プルリクエストおよび `main` ブランチへの push 時に以下のジョブが自動実行される。

```mermaid
graph TD
    A[Push / PR Trigger] --> B[Job 1: test]
    A --> C[Job 2: e2e-compose-contract]
    A --> D[Job 3: docker-build-check]

    subgraph Job 1: Unit & In-Memory Contract
        B1[go vet 静的解析] --> B2[go test -race -cover ./...]
    end

    subgraph Job 2: Docker Compose E2E (C-5)
        C1[docker compose up -d] --> C2[Wait /readyz]
        C2 --> C3[go test TestE2E_ContractVerification]
        C3 --> C4[docker compose down]
    end

    subgraph Job 3: Multi-Arch Build Check (C-1)
        D1[QEMU & Buildx Setup] --> D2[Build linux/amd64, linux/arm64]
    end
```

1. **`test` (単体 & インメモリ契約テスト)**:
   - Go の静的解析 (`go vet`)。
   - `-race` フラグ付きの全テスト実行およびカバレッジ計測。
2. **`e2e-compose-contract` (Docker Compose E2E 契約テスト - 要件 C-5)**:
   - `docker compose` で実コンテナ群を自動起動。
   - `/readyz` のポーリングによる起動待機。
   - `TestE2E_ContractVerification` による実コンテナに対する HTTP 契約検証。
3. **`docker-build-check` (マルチアーキテクチャビルド検証 - 要件 C-1)**:
   - AWS Graviton (`linux/arm64`) および `linux/amd64` 双方の Docker イメージビルドが壊れていないかをプッシュなし（ドライラン）で自動検証。

---

## 4. バージョニングとリリース自動化 (C-7)

- バージョンルールおよび後方互換性方針については [docs/versioning_and_compatibility.md](file:///d:/Sources/tollgate/docs/versioning_and_compatibility.md) に準拠。
- GitHub 上でセマンティックバージョニングタグ（例: `v1.0.0`）がプッシュされると、`.github/workflows/release.yml` が自動起動し、GoReleaser による各 OS 向けバイナリ配布と GHCR へのマルチアーキテクチャ Docker イメージの push が実行される。
