package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/kanmon-hq/tollgate/internal/domain/repository"
)

// DynamoDBRateLimiter は Fixed Window アルゴリズムで DynamoDB をバックエンドとするレートリミッター。
//
// # アルゴリズム
//
// 1 分単位の固定ウィンドウ (Fixed Window) を採用する。
// PK に分単位のタイムスタンプを含めることで、ウィンドウごとに独立したアイテムが自動生成される。
// ウィンドウをまたいだリセット処理・CAS が不要なため実装がシンプルになる。
//
// # データモデル (既存テーブル TollgateAPIKeys に相乗り)
//
//	PK:    "RATE#<keyID>#<window>"  例: "RATE#key-uuid-xxxx#2026-09-24T00:24"
//	count: Number (Atomic ADD)
//	ttl:   Number (Unix timestamp, 2 分後) — DynamoDB TTL で自動削除
//
// # 注意事項
//
//   - DynamoDB の TTL を有効化するには `ttl` 属性をテーブルの TTL 属性として設定すること。
//     未設定でもレートリミット動作自体は正常。古いアイテムが蓄積されるだけ。
//   - Sliding Window と異なり、ウィンドウ境界付近で最大 2×limitRPM の通過を許容する。
//     より厳密なレートリミットが必要な場合は Redis + Lua スクリプトに切り替えること。
//   - DynamoDB が一時的に利用不可の場合はフェイルオープン (許可) とする。
//     可用性を優先するため。厳密な制限が必要な場合はフェイルクローズに変更すること。
type DynamoDBRateLimiter struct {
	client    *dynamodb.Client
	tableName string
}

var _ repository.RateLimiter = (*DynamoDBRateLimiter)(nil)

// NewDynamoDBRateLimiter は DynamoDB バックエンドのレートリミッターを生成する。
// tableName には既存の TollgateAPIKeys テーブル名を渡す。
func NewDynamoDBRateLimiter(client *dynamodb.Client, tableName string) *DynamoDBRateLimiter {
	return &DynamoDBRateLimiter{
		client:    client,
		tableName: tableName,
	}
}

// Allow は指定 keyID の現在ウィンドウ内消費数を Atomic にインクリメントし、
// limitRPM を超えている場合は false を返す。
// DynamoDB エラー時はフェイルオープン (allowed=true, err=非nil) とする。
func (r *DynamoDBRateLimiter) Allow(ctx context.Context, id string, limitRPM int) (bool, int, time.Duration, error) {
	if limitRPM <= 0 {
		// 上限 0 以下は無制限
		return true, 999999, 0, nil
	}

	now := time.Now().UTC()

	// 分単位ウィンドウキー (例: "2026-09-24T00:24")
	window := now.Format("2006-01-02T15:04")
	pk := "RATE#" + id + "#" + window

	// TTL: 現在ウィンドウ終端 + 1 分のバッファ (= 現在分の切り捨て + 2 分)
	ttl := now.Truncate(time.Minute).Add(2 * time.Minute).Unix()

	out, err := r.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
		// ADD: アイテム未存在時は 0 から加算 (DynamoDB ADD のデフォルト動作)
		// SET ttl: 初回作成時のみセット (if_not_exists でウィンドウ中の TTL 変更を防ぐ)
		UpdateExpression: aws.String("ADD #cnt :one SET #ttl = if_not_exists(#ttl, :ttl)"),
		ExpressionAttributeNames: map[string]string{
			"#cnt": "count",
			"#ttl": "ttl",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one": &types.AttributeValueMemberN{Value: "1"},
			":ttl": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", ttl)},
		},
		ReturnValues: types.ReturnValueUpdatedNew,
	})
	if err != nil {
		// フェイルオープン: DynamoDB 障害時はリクエストを通過させる
		return true, 0, 0, fmt.Errorf("dynamodb rate limiter error (fail-open): %w", err)
	}

	var count int
	if v, ok := out.Attributes["count"].(*types.AttributeValueMemberN); ok {
		if _, scanErr := fmt.Sscanf(v.Value, "%d", &count); scanErr != nil {
			// パース失敗時もフェイルオープン
			return true, 0, 0, fmt.Errorf("dynamodb rate limiter parse error (fail-open): %w", scanErr)
		}
	}

	// 次のウィンドウ開始までの残り時間
	nextWindow := now.Truncate(time.Minute).Add(time.Minute)
	resetIn := nextWindow.Sub(now)

	if count > limitRPM {
		return false, 0, resetIn, nil
	}

	remaining := limitRPM - count
	return true, remaining, resetIn, nil
}
