package sqlrepo

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // SQLite ドライバ登録 (CGO 不要)
)

// NewSQLiteRepository は SQLite バックエンドの KeyRepository を生成する。
//
// path にはデータベースファイルのパスを指定する (例: "./tollgate.db")。
//
// # 特性
//   - CGO 不要 (modernc.org/sqlite はピュア Go 実装)
//   - 外部サービス不要。バイナリ 1 本で即起動可能
//   - シングルインスタンス前提。スケールアウト非対応
//
// # 注意事項
//   - SQLite は書き込み競合を防ぐため MaxOpenConns=1 に設定する
//   - レートリミットは memory または none バックエンドと組み合わせること
func NewSQLiteRepository(ctx context.Context, path string) (*SQLRepository, error) {
	// file:path?_journal=WAL&_timeout=5000 で WAL モードを有効化 (読み取り並行性向上)
	dsn := fmt.Sprintf("file:%s?_journal=WAL&_timeout=5000&_foreign_keys=on", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}

	// SQLite は書き込みが直列化されるため MaxOpenConns=1 を設定
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("sqlite ping: %w", err)
	}

	repo := &SQLRepository{db: db}

	if err := AutoMigrate(ctx, db); err != nil {
		return nil, fmt.Errorf("sqlite migrate: %w", err)
	}

	return repo, nil
}
