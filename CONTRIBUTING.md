# Tollgate コントリビューションガイド (Contributing Guide)

Tollgate への貢献をご検討いただきありがとうございます！  
バグ報告、機能提案、ドキュメントの改善、プルリクエストなど、あらゆるコントリビューションを歓迎します。

本ガイドラインでは、Tollgate プロジェクトへの参加および開発をスムーズに進めるための手順とルールを定めています。

---

## 目次

1. [行動規範 (Code of Conduct)](#行動規範-code-of-conduct)
2. [開発ワークフロー](#開発ワークフロー)
3. [ローカル開発環境のセットアップ](#ローカル開発環境のセットアップ)
4. [アーキテクチャと設計原則](#アーキテクチャと設計原則)
5. [コーディング規約](#コーディング規約)
6. [テスト規約](#テスト規約)
7. [プルリクエスト (PR) チェックリスト](#プルリクエスト-pr-チェックリスト)

---

## 行動規範 (Code of Conduct)

すべての参加者が安全かつ敬意を持ってコラボレーションできるよう、オープンで歓迎されるコミュニティの維持に努めてください。相手を尊重した建設的なフィードバックと対話を心がけてください。

---

## 開発ワークフロー

### 1. Issue の作成
- バグの報告や新機能の提案を行う場合は、まず [GitHub Issues](https://github.com/kanmon-hq/tollgate/issues) を作成してください。
- 既存の Issue や PR で類似の議論がないか事前に確認してください。

### 2. ブランチ戦略
- `main` ブランチから作業用ブランチを作成してください。
- ブランチ名は以下のプレフィックスを使用してください：
  - `feature/<機能名>`: 新機能の追加
  - `fix/<バグ内容>`: バグ修正
  - `docs/<ドキュメント名>`: ドキュメント修正
  - `refactor/<リファクタ内容>`: 振る舞いを変えないコード整理

```bash
git checkout -b feature/dynamic-route-reload
```

### 3. コミットメッセージ規約
コミットメッセージには [Conventional Commits](https://www.conventionalcommits.org/) 形式を採用しています。

形式: `<type>(<scope>): <description>`

| Type | 説明 | 例 |
|---|---|---|
| `feat` | 新機能の追加 | `feat(proxy): add support for websocket upgrade` |
| `fix` | バグ修正 | `fix(dynamodb): omit empty tenant_id to prevent gsi validation error` |
| `docs` | ドキュメントの変更 | `docs(readme): update health check endpoints to livez/readyz` |
| `test` | テストの追加・修正 | `test(ratelimit): add concurrent request benchmark` |
| `refactor`| リファクタリング | `refactor(usecase): streamline key verification logic` |
| `chore` | ビルド設定や補助ツールの変更 | `chore(deps): update aws-sdk-go-v2 to v1.36.3` |

---

## ローカル開発環境のセットアップ

### 前提ツール
- **Go**: 1.24+ (推奨)
- **Docker** または **nerdctl** (コンテナランタイム)
- **Git**

### 手順

```bash
# 1. リポジトリのクローン
git clone https://github.com/kanmon-hq/tollgate.git
cd tollgate

# 2. 依存関係のダウンロード
go mod download

# 3. 開発用インフラ (DynamoDB Local & Admin) の起動
nerdctl compose --profile database up -d dynamodb dynamodb-init dynamodb-admin
# (Docker Compose の場合: docker compose --profile database up -d dynamodb dynamodb-init dynamodb-admin)

# 4. 環境変数の設定
cp .env.example .env

# 5. ローカルサーバー起動
go run cmd/server/main.go
```

サーバーが起動したら、別ターミナルでヘルスチェックを確認します：
```bash
curl -i http://localhost:8000/livez
curl -i http://localhost:8000/readyz
```

---

## アーキテクチャと設計原則

Tollgate は Clean Architecture に着想を得たレイヤード構造を採用しています。依存関係は常に内側（Domain）に向かって単一方向に保ってください。

```text
internal/
├── domain/            # ビジネスルール・エンティティ・リポジトリ IF (外部依存なし)
├── usecase/           # アプリケーションユースケース (ビジネスフロー)
├── infrastructure/    # 外部通信 (DynamoDB SDK, Prometheus, メモリキャッシュ)
└── delivery/http/     # HTTP ハンドラー, Huma v2 ルーティング, リバースプロキシ
```

### コア設計・セキュリティ方針

開発時は以下のセキュリティおよび設計方針を厳守してください：

1. **Fail-Fast 原則（コンフリクト即時遮断）**:
   - リバースプロキシでのテナント解決において、クライアントが指定した `X-Tenant-ID` と API キーに設定された `tenant_id` に不一致（コンフリクト）がある場合、**暗黙的に上書きしてはならない**。なりすまし防止および設定ミスの即時検知のため、`403 Forbidden` で即座にリクエストを拒絶すること。
2. **ハードコードされたシークレットの排除**:
   - API キー、認証トークン、マスター管理者シークレット等の認証情報において、コード内にデフォルト値をフォールバックとしてハードコードしてはならない。環境変数未設定時は明示的な認証無効化または起動時エラー（Fail-Fast）とすること。
3. **DynamoDB スパースインデックスの遵守**:
   - DynamoDB の GSI キー属性には空文字列（`""`）を格納できない。サービスキーのようにオプショナルなキー属性は、構造体タグに `dynamodbav:",omitempty"` を指定し、属性そのものを省略すること。
4. **平文 API キーの非保持**:
   - データベースには SHA-256 ダイジェスト（`KEY#<hash>`）のみを保存する。平文キーは生成時・ローテーション時のレスポンス以外で永続化・ログ出力してはならない。
5. **クラウドネイティブ・プローブ体系**:
   - プロセスの死活監視には `/livez`、外部依存（DynamoDB）を含めた準備状態監視には `/readyz`、総合疎通には `/healthz` を使用すること。

---

## コーディング規約

- **フォーマット**: すべての Go コードは `gofmt` (または `goimports`) でフォーマットしてください。
- **静的解析**: コミット前に `go vet ./...` を実行し、静的解析警告がないことを確認してください。
- **エラーハンドリング**:
  - エラーを無視せず、呼び出し元にラップして返却してください（`fmt.Errorf("...: %w", err)`）。
  - ログ出力時は過剰なスタックトレースや機密情報（API キー、認証情報等）を出力しないよう注意してください。
- **コンテキスト**: I/O 操作を伴う関数には必ず第 1 引数に `context.Context` を渡し、タイムアウトやキャンセレーションを適切にハンドリングしてください。

---

## テスト規約

Tollgate では高い品質と信頼性を保つため、機能追加・バグ修正には必ず対応する単体テスト（ユニットテスト）を同梱してください。

```bash
# 全テスト実行
go test -v ./...

# キャッシュを無視して全テスト実行
go test -v -count=1 ./...

# データ競合 (Race Condition) の検出
go test -race ./...
```

- インターフェースを利用したモック（`repository.KeyRepository` 等）を活用し、外部 DB なしで単体テストが高速に実行できるように設計してください。
- カバレッジの低下を防ぎ、エッジケース（境界値、空文字、無効な入力、認証失敗、レート制限超過等）のテストケースを網羅してください。

---

## プルリクエスト (PR) チェックリスト

PR を提出する前に、以下の項目を確認してください：

- [ ] 最新の `main` ブランチを取り込んでいるか（`git rebase main` または `git merge main`）
- [ ] すべてのテストが成功するか（`go test -v ./...`）
- [ ] レースコンディション検出を通過するか（`go test -race ./...`）
- [ ] `go vet ./...` でエラーや警告が出ないか
- [ ] `gofmt` でコードがフォーマットされているか
- [ ] 新機能・修正内容に対するテストコードが含まれているか
- [ ] コミットメッセージが Conventional Commits 規約に準拠しているか
- [ ] 必要に応じて `README.md` や `docs/` のドキュメントを更新しているか

PR が作成されると、メンテナーによるコードレビューが行われます。建設的なディスカッションを通じて、より良いコードに仕上げていきましょう！
