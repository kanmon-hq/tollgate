# AWS ECS Fargate デプロイメントガイド (要件 C-1)

本ドキュメントは、Tollgate を **AWS ECS Fargate** 上で高可用かつ本番運用するための構成ガイドラインおよびタスク定義の標準仕様を定めたものである。

---

## 1. アーキテクチャ原則

1. **完全ステートレス設計**:
   - Tollgate はインメモリキャッシュを持つものの、すべての永続データ（APIキー、テナント設定、月間利用量）および分散レートリミット状態を外部バックエンド（Amazon DynamoDB / AWS ElastiCache / Valkey）に委譲する。
   - インスタンス障害時の即時破棄・新規起動、および CPU / リクエスト数に応じた自由なオートスケーリング（水平スケール）が可能。
2. **マルチアーキテクチャ (ARM64 / AMD64) サポート**:
   - AWS Graviton プロセッサ（`ARM64`）での稼働を推奨（コストパフォーマンス最大 20%〜40% 向上）。
   - GHCR に公開される公式コンテナイメージ（`ghcr.io/kanmon-hq/tollgate:<version>`）は、`linux/arm64` および `linux/amd64` のマルチアーキテクチャに対応。
3. **ヘルスチェックの明確な分離**:
   - **Liveness プローブ (`/livez`)**: プロセス生存確認。外部依存（DB）を見ずに即座に `200 OK` を返却。ECS コンテナヘルスチェックに最適。
   - **Readiness プローブ (`/readyz`)**: トラフィック受付準備監視。DB/ストレージ疎通が確立されているか確認。ALB ターゲットグループのヘルスチェックに最適。
4. **SIGTERM グレースフルシャットダウン**:
   - ECS サービス更新やローリングデプロイ時、コンテナは `SIGTERM` を受信すると即座に新規リクエストの受付を停止し、処理中のインフライトリクエストを完了させてから正常終了する（最大10秒）。

---

## 2. ECS タスク定義 (Task Definition) テンプレート

以下は AWS Graviton (`ARM64`) を対象とした本番向けタスク定義 JSON サンプルである。

```json
{
  "family": "tollgate-service",
  "cpu": "512",
  "memory": "1024",
  "networkMode": "awsvpc",
  "requiresCompatibilities": ["FARGATE"],
  "runtimePlatform": {
    "cpuArchitecture": "ARM64",
    "operatingSystemFamily": "LINUX"
  },
  "executionRoleArn": "arn:aws:iam::123456789012:role/ecsTaskExecutionRole",
  "taskRoleArn": "arn:aws:iam::123456789012:role/tollgateTaskRole",
  "containerDefinitions": [
    {
      "name": "tollgate",
      "image": "ghcr.io/kanmon-hq/tollgate:v1.0.0",
      "essential": true,
      "stopTimeout": 30,
      "portMappings": [
        {
          "containerPort": 8000,
          "protocol": "tcp"
        }
      ],
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
      "environment": [
        { "name": "PORT", "value": "8000" },
        { "name": "AWS_REGION", "value": "ap-northeast-1" },
        { "name": "DB_BACKEND", "value": "dynamodb" },
        { "name": "TABLE_NAME", "value": "TollgateAPIKeys" },
        { "name": "RATE_LIMIT_BACKEND", "value": "dynamodb" },
        { "name": "KEY_CACHE_TTL", "value": "300s" },
        { "name": "METRICS_ENABLED", "value": "true" },
        { "name": "ROUTES_CONFIG", "value": "/ai-engine=http://ai-engine.internal:8000|scopes:llm:*;/mcp=http://mcp-gateway.internal:8000|scopes:mcp:tools:execute" }
      ],
      "secrets": [
        {
          "name": "ADMIN_KEY",
          "valueFrom": "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:tollgate/admin-key"
        },
        {
          "name": "API_KEY_HASH_SECRET",
          "valueFrom": "arn:aws:secretsmanager:ap-northeast-1:123456789012:secret:tollgate/hash-secret"
        }
      ],
      "logConfiguration": {
        "logDriver": "awslogs",
        "options": {
          "awslogs-group": "/ecs/tollgate",
          "awslogs-region": "ap-northeast-1",
          "awslogs-stream-prefix": "tollgate"
        }
      }
    }
  ]
}
```

---

## 3. ALB (Application Load Balancer) 設定規約

| 項目 | 設定値 | 理由・備考 |
| :--- | :--- | :--- |
| **ヘルスチェックプロトコル** | `HTTP` | リバースプロキシポート |
| **ヘルスチェックパス** | `/readyz` | DB疎通が正常でトラフィック受付可能な場合のみ ALB からルーティングする |
| **成功コード** | `200` | 正常応答コード |
| **間隔 (Interval)** | 10秒〜15秒 | 障害ノードの迅速な切り離し |
| **タイムアウト (Timeout)** | 3秒〜5秒 | 遅延検知 |
| **非正常のしきい値** | 2回 | 早期切り離し |
| **正常のしきい値** | 2回 | 迅速な復旧組み込み |
| **登録解除の遅延 (Deregistration Delay)** | 15秒〜30秒 | 停止時のインフライトリクエストドレイン時間を確保 |

---

## 4. シャットダウン動作とライフサイクル

1. **ECS タスク停止・デプロイトリガー**:
   - ECS が ALB ターゲットグループからの登録解除（Draining）を開始。
   - ECS Agent が Tollgate コンテナへ `SIGTERM` シグナルを送信。
2. **Tollgate のグレースフル停止シーケンス**:
   - `SIGTERM` を検知後、直ちにリスナーをクローズして新規接続受付を停止。
   - 最大 10 秒間のタイムアウト枠内で、処理中のプロキシ通信・APIレスポンスの送信完了を待機。
   - 全接続ドレイン後にプロセスがコード 0 で正常終了。
3. **安全余裕 (`stopTimeout`)**:
   - タスク定義側で `stopTimeout: 30` 秒を設定しておくことで、10 秒の内部ドレイン処理が確実に完了するまで ECS Agent が強制終了（`SIGKILL`）を発行しないよう保護。
