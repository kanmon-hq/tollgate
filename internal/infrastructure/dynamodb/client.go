package dynamodb

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/domain/repository"
)

type DynamoDBRepository struct {
	client    *dynamodb.Client
	tableName string
}

func NewDynamoDBRepository(client *dynamodb.Client, tableName string) repository.KeyRepository {
	return &DynamoDBRepository{
		client:    client,
		tableName: cmp.Or(tableName, "TollgateAPIKeys"),
	}
}

func (r *DynamoDBRepository) PutKey(ctx context.Context, key *entity.APIKey) error {
	if key.ItemType == "" {
		key.ItemType = "KEY"
	}
	item, err := attributevalue.MarshalMap(key)
	if err != nil {
		return fmt.Errorf("marshal key error: %w", err)
	}

	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(r.tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("put item error: %w", err)
	}
	return nil
}

func (r *DynamoDBRepository) GetKeyByHash(ctx context.Context, keyHash string) (*entity.APIKey, error) {
	pk := "KEY#" + keyHash
	res, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get item error: %w", err)
	}
	if res.Item == nil {
		return nil, nil
	}

	var key entity.APIKey
	if err := attributevalue.UnmarshalMap(res.Item, &key); err != nil {
		return nil, fmt.Errorf("unmarshal key error: %w", err)
	}
	return &key, nil
}

func (r *DynamoDBRepository) GetKeyByID(ctx context.Context, keyID string) (*entity.APIKey, error) {
	var lastKey map[string]types.AttributeValue
	for {
		out, err := r.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:        aws.String(r.tableName),
			FilterExpression: aws.String("key_id = :kid"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":kid": &types.AttributeValueMemberS{Value: keyID},
			},
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("scan key_id error: %w", err)
		}
		if len(out.Items) > 0 {
			var key entity.APIKey
			if err := attributevalue.UnmarshalMap(out.Items[0], &key); err != nil {
				return nil, fmt.Errorf("unmarshal key error: %w", err)
			}
			return &key, nil
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		lastKey = out.LastEvaluatedKey
	}
	return nil, nil
}

func encodeCursor(key map[string]types.AttributeValue) (string, error) {
	if len(key) == 0 {
		return "", nil
	}
	var rawMap map[string]interface{}
	if err := attributevalue.UnmarshalMap(key, &rawMap); err != nil {
		return "", err
	}
	b, err := json.Marshal(rawMap)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func decodeCursor(cursor string) (map[string]types.AttributeValue, error) {
	if strings.TrimSpace(cursor) == "" {
		return nil, nil
	}
	b, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor encoding: %w", err)
	}
	var rawMap map[string]interface{}
	if err := json.Unmarshal(b, &rawMap); err != nil {
		return nil, fmt.Errorf("invalid cursor json: %w", err)
	}
	av, err := attributevalue.MarshalMap(rawMap)
	if err != nil {
		return nil, fmt.Errorf("marshal cursor map: %w", err)
	}
	return av, nil
}

func (r *DynamoDBRepository) ListKeys(ctx context.Context, params entity.ListKeysParams) (*entity.ListKeysResult, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}

	startKey, err := decodeCursor(params.Cursor)
	if err != nil {
		return nil, fmt.Errorf("decode cursor error: %w", err)
	}

	var input *dynamodb.QueryInput
	if params.TenantID != "" {
		input = &dynamodb.QueryInput{
			TableName:              aws.String(r.tableName),
			IndexName:              aws.String("GSI_TenantKeys"),
			KeyConditionExpression: aws.String("tenant_id = :tid"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":tid": &types.AttributeValueMemberS{Value: params.TenantID},
			},
			ScanIndexForward:  aws.Bool(false), // 新しい順
			Limit:             aws.Int32(int32(limit)),
			ExclusiveStartKey: startKey,
		}
		if params.ServiceID != "" {
			input.FilterExpression = aws.String("service_id = :sid")
			input.ExpressionAttributeValues[":sid"] = &types.AttributeValueMemberS{Value: params.ServiceID}
		}
	} else if params.ServiceID != "" {
		input = &dynamodb.QueryInput{
			TableName:              aws.String(r.tableName),
			IndexName:              aws.String("GSI_ServiceKeys"),
			KeyConditionExpression: aws.String("service_id = :sid"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":sid": &types.AttributeValueMemberS{Value: params.ServiceID},
			},
			ScanIndexForward:  aws.Bool(false), // 新しい順
			Limit:             aws.Int32(int32(limit)),
			ExclusiveStartKey: startKey,
		}
	} else {
		input = &dynamodb.QueryInput{
			TableName:              aws.String(r.tableName),
			IndexName:              aws.String("GSI_AllKeys"),
			KeyConditionExpression: aws.String("item_type = :itype"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":itype": &types.AttributeValueMemberS{Value: "KEY"},
			},
			ScanIndexForward:  aws.Bool(false), // 新しい順
			Limit:             aws.Int32(int32(limit)),
			ExclusiveStartKey: startKey,
		}
	}

	out, err := r.client.Query(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("query keys error: %w", err)
	}

	var keys []*entity.APIKey
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &keys); err != nil {
		return nil, fmt.Errorf("unmarshal keys error: %w", err)
	}
	if keys == nil {
		keys = []*entity.APIKey{}
	}

	nextCursor, err := encodeCursor(out.LastEvaluatedKey)
	if err != nil {
		return nil, fmt.Errorf("encode cursor error: %w", err)
	}

	return &entity.ListKeysResult{
		Keys:       keys,
		NextCursor: nextCursor,
	}, nil
}

func (r *DynamoDBRepository) ListKeysByTenant(ctx context.Context, tenantID string) ([]*entity.APIKey, error) {
	res, err := r.ListKeys(ctx, entity.ListKeysParams{TenantID: tenantID, Limit: 100})
	if err != nil {
		return nil, err
	}
	return res.Keys, nil
}

func (r *DynamoDBRepository) UpdateKeyStatus(ctx context.Context, keyHash string, status entity.KeyStatus, isActive bool) error {
	pk := "KEY#" + keyHash
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := r.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
		UpdateExpression: aws.String("SET #st = :st, is_active = :act, updated_at = :now"),
		ExpressionAttributeNames: map[string]string{
			"#st": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":st":  &types.AttributeValueMemberS{Value: string(status)},
			":act": &types.AttributeValueMemberBOOL{Value: isActive},
			":now": &types.AttributeValueMemberS{Value: now},
		},
	})
	if err != nil {
		return fmt.Errorf("update key status error: %w", err)
	}
	return nil
}

func (r *DynamoDBRepository) UpdateKeySettings(ctx context.Context, keyHash string, input entity.UpdateKeyInput) (*entity.APIKey, error) {
	pk := "KEY#" + keyHash
	now := time.Now().UTC().Format(time.RFC3339)

	updates := make([]string, 0, 5)
	updates = append(updates, "updated_at = :now")

	exprVals := map[string]types.AttributeValue{
		":now": &types.AttributeValueMemberS{Value: now},
	}
	exprNames := map[string]string{}

	if input.Name != nil {
		updates = append(updates, "#nm = :name")
		exprNames["#nm"] = "name"
		exprVals[":name"] = &types.AttributeValueMemberS{Value: *input.Name}
	}
	if input.Scopes != nil {
		updates = append(updates, "scopes = :scopes")
		scVals := make([]types.AttributeValue, len(*input.Scopes))
		for i, sc := range *input.Scopes {
			scVals[i] = &types.AttributeValueMemberS{Value: sc}
		}
		exprVals[":scopes"] = &types.AttributeValueMemberL{Value: scVals}
	}
	if input.RateLimitRPM != nil {
		updates = append(updates, "rate_limit_rpm = :rpm")
		exprVals[":rpm"] = &types.AttributeValueMemberN{Value: strconv.Itoa(*input.RateLimitRPM)}
	}
	if input.MonthlyQuota != nil {
		updates = append(updates, "monthly_quota = :quota")
		exprVals[":quota"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(*input.MonthlyQuota, 10)}
	}

	inputParam := &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
		UpdateExpression:          aws.String("SET " + strings.Join(updates, ", ")),
		ExpressionAttributeValues: exprVals,
		ReturnValues:              types.ReturnValueAllNew,
	}
	if len(exprNames) > 0 {
		inputParam.ExpressionAttributeNames = exprNames
	}

	out, err := r.client.UpdateItem(ctx, inputParam)
	if err != nil {
		return nil, fmt.Errorf("update key settings error: %w", err)
	}

	var updated entity.APIKey
	if err := attributevalue.UnmarshalMap(out.Attributes, &updated); err != nil {
		return nil, fmt.Errorf("unmarshal updated key error: %w", err)
	}
	return &updated, nil
}

func (r *DynamoDBRepository) RotateKey(ctx context.Context, params entity.RotateKeyParams) (*entity.APIKey, error) {
	// 1. 旧キーを取得
	oldKey, err := r.GetKeyByHash(ctx, params.OldKeyHash)
	if err != nil || oldKey == nil {
		return nil, fmt.Errorf("old key not found")
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	// 2. 旧キーのステータスを rotating に変更し、猶予期限を設定
	oldKeyPK := "KEY#" + params.OldKeyHash
	_, err = r.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: oldKeyPK},
		},
		UpdateExpression: aws.String("SET #st = :st, updated_at = :now, rotation_meta = :rm"),
		ExpressionAttributeNames: map[string]string{
			"#st": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":st":  &types.AttributeValueMemberS{Value: string(entity.StatusRotating)},
			":now": &types.AttributeValueMemberS{Value: nowStr},
			":rm": &types.AttributeValueMemberM{
				Value: map[string]types.AttributeValue{
					"old_key_hash":            &types.AttributeValueMemberS{Value: params.OldKeyHash},
					"grace_period_expires_at": &types.AttributeValueMemberS{Value: params.GracePeriodExpiresAt.Format(time.RFC3339)},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("update old key for rotation error: %w", err)
	}

	// 3. 新キーを同じ KeyID で保存 (PKは新ハッシュ)
	newKey := *oldKey
	newKey.PK = "KEY#" + params.NewKeyHash
	newKey.KeyPrefix = params.NewKeyPrefix
	newKey.Status = entity.StatusActive
	newKey.IsActive = true
	newKey.Rotation = nil
	newKey.UpdatedAt = nowStr

	if err := r.PutKey(ctx, &newKey); err != nil {
		return nil, fmt.Errorf("put new rotated key error: %w", err)
	}

	return &newKey, nil
}

func (r *DynamoDBRepository) DeleteKey(ctx context.Context, keyHash string) error {
	pk := "KEY#" + keyHash
	_, err := r.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
	})
	if err != nil {
		return fmt.Errorf("delete key error: %w", err)
	}
	return nil
}

func (r *DynamoDBRepository) IncrementMonthlyUsage(ctx context.Context, keyHash string, month string, increment int64) (int64, error) {
	pk := "KEY#" + keyHash

	// 月が変わっているか確認してリセット、または Atomic ADD
	out, err := r.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
		UpdateExpression: aws.String("ADD current_month_usage :inc SET current_month = :m"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":inc": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", increment)},
			":m":   &types.AttributeValueMemberS{Value: month},
		},
		ReturnValues: types.ReturnValueUpdatedNew,
	})
	if err != nil {
		return 0, fmt.Errorf("increment usage error: %w", err)
	}

	if val, ok := out.Attributes["current_month_usage"].(*types.AttributeValueMemberN); ok {
		var total int64
		if _, err := fmt.Sscanf(val.Value, "%d", &total); err != nil {
			return 0, fmt.Errorf("parse current_month_usage error: %w", err)
		}
		return total, nil
	}
	return 0, nil
}

func (r *DynamoDBRepository) UpdateLastUsedAt(ctx context.Context, keyHash string, lastUsed time.Time) error {
	pk := "KEY#" + keyHash
	_, err := r.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
		},
		UpdateExpression: aws.String("SET last_used_at = :lu"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":lu": &types.AttributeValueMemberS{Value: lastUsed.UTC().Format(time.RFC3339)},
		},
	})
	return err
}

func (r *DynamoDBRepository) Ping(ctx context.Context) error {
	_, err := r.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(r.tableName),
	})
	if err != nil {
		return fmt.Errorf("dynamodb ping failed: %w", err)
	}
	return nil
}

func (r *DynamoDBRepository) GetStats(ctx context.Context, tenantID, serviceID string) (*entity.KeyStats, error) {
	stats := &entity.KeyStats{
		Services: make([]string, 0),
		Tenants:  make([]string, 0),
	}
	var queryInput *dynamodb.QueryInput

	if tenantID != "" {
		queryInput = &dynamodb.QueryInput{
			TableName:              aws.String(r.tableName),
			IndexName:              aws.String("GSI_TenantKeys"),
			KeyConditionExpression: aws.String("tenant_id = :tid"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":tid": &types.AttributeValueMemberS{Value: tenantID},
			},
			ProjectionExpression: aws.String("#st, current_month_usage, service_id, tenant_id"),
			ExpressionAttributeNames: map[string]string{
				"#st": "status",
			},
		}
		if serviceID != "" {
			queryInput.FilterExpression = aws.String("service_id = :sid")
			queryInput.ExpressionAttributeValues[":sid"] = &types.AttributeValueMemberS{Value: serviceID}
		}
	} else if serviceID != "" {
		queryInput = &dynamodb.QueryInput{
			TableName:              aws.String(r.tableName),
			IndexName:              aws.String("GSI_ServiceKeys"),
			KeyConditionExpression: aws.String("service_id = :sid"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":sid": &types.AttributeValueMemberS{Value: serviceID},
			},
			ProjectionExpression: aws.String("#st, current_month_usage, service_id, tenant_id"),
			ExpressionAttributeNames: map[string]string{
				"#st": "status",
			},
		}
	} else {
		queryInput = &dynamodb.QueryInput{
			TableName:              aws.String(r.tableName),
			IndexName:              aws.String("GSI_AllKeys"),
			KeyConditionExpression: aws.String("item_type = :itype"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":itype": &types.AttributeValueMemberS{Value: "KEY"},
			},
			ProjectionExpression: aws.String("#st, current_month_usage, service_id, tenant_id"),
			ExpressionAttributeNames: map[string]string{
				"#st": "status",
			},
		}
	}

	servicesSet := make(map[string]struct{})
	tenantsSet := make(map[string]struct{})

	for {
		out, err := r.client.Query(ctx, queryInput)
		if err != nil {
			return nil, fmt.Errorf("failed to query stats from dynamodb: %w", err)
		}

		for _, item := range out.Items {
			stats.TotalKeys++

			if stVal, ok := item["status"].(*types.AttributeValueMemberS); ok {
				switch entity.KeyStatus(stVal.Value) {
				case entity.StatusActive:
					stats.ActiveKeys++
				case entity.StatusRotating:
					stats.RotatingKeys++
				}
			}

			if usageVal, ok := item["current_month_usage"].(*types.AttributeValueMemberN); ok {
				var u int64
				if _, err := fmt.Sscanf(usageVal.Value, "%d", &u); err == nil {
					stats.TotalMonthlyUsage += u
				}
			}

			if svcVal, ok := item["service_id"].(*types.AttributeValueMemberS); ok && svcVal.Value != "" {
				servicesSet[svcVal.Value] = struct{}{}
			}

			if tenVal, ok := item["tenant_id"].(*types.AttributeValueMemberS); ok && tenVal.Value != "" {
				tenantsSet[tenVal.Value] = struct{}{}
			}
		}

		if out.LastEvaluatedKey == nil {
			break
		}
		queryInput.ExclusiveStartKey = out.LastEvaluatedKey
	}

	for s := range servicesSet {
		stats.Services = append(stats.Services, s)
	}
	slices.Sort(stats.Services)

	for t := range tenantsSet {
		stats.Tenants = append(stats.Tenants, t)
	}
	slices.Sort(stats.Tenants)

	return stats, nil
}
