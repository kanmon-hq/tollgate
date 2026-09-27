package config

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// loadDotEnv は .env ファイルを読み込み、未セットの環境変数にのみ適用する。
// ファイルが存在しない場合は何もしない（エラーにならない）。
// 環境変数が既にセットされている場合は上書きしない（環境変数優先）。
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // ファイルが存在しない場合はスキップ
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 空行・コメント行をスキップ
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// 値を囲むクォートを除去 ("value" or 'value')
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		// 環境変数が未セットの場合のみ適用（環境変数優先）
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}

// RouteConfig は動的ルーティングのルール定義
type RouteConfig struct {
	Prefix      string `json:"prefix"`                 // マッチさせるパスプレフィックス (例: "/mcp")
	Target      string `json:"target"`                 // 転送先ベースURL (例: "http://mcp-gateway:8000")
	Scope       string `json:"scope,omitempty"`        // 必要な API キースコープ (例: "mcp:*")
	StripPrefix bool   `json:"strip_prefix,omitempty"` // true の場合、転送時に Prefix を除去する
}

// Config は Tollgate アプリケーション全体の環境設定
type Config struct {
	Port             string
	DynamoDBEndpoint string
	AWSRegion        string
	TableName        string
	OpenAPIPath      string
	DocsPath         string
	KeyCacheTTL      time.Duration
	AdminAPIKey      string
	Routes           []*RouteConfig
	ForwardTargetURL string
	DBBackend        string // "dynamodb" (default) | "sqlite" | "cosmosdb" | "firestore"
	SQLitePath       string // default: "./tollgate.db"

	// Cosmos DB 接続設定 (DB_BACKEND=cosmosdb 時)
	CosmosDBEndpoint  string // 例: "https://myaccount.documents.azure.com:443/"
	CosmosDBKey       string // Azure Cosmos DB Primary/Secondary Key
	CosmosDBDatabase  string // default: "tollgate"
	CosmosDBContainer string // default: "api_keys"

	// Firestore 接続設定 (DB_BACKEND=firestore 時)
	FirestoreProjectID  string // Google Cloud Project ID
	FirestoreDatabaseID string // default: "(default)"
	FirestoreCollection string // default: "api_keys"

	RateLimitBackend string // "memory" (default) | "redis" | "valkey" | "two-tier" | "dynamodb" | "none"

	// Redis / Valkey 接続設定 (RATE_LIMIT_BACKEND=redis / valkey / two-tier 時)
	RedisAddr     string // 例: "redis:6379"
	RedisPassword string // 認証パスワード (不要な場合は空文字)
	RedisDB       int    // 使用する DB 番号 (0-15, デフォルト: 0)
}

// Load は環境変数および設定ファイルから Config を構築する。
// カレントディレクトリの .env を自動で読み込む（環境変数が既にセットされている場合は上書きしない）。
func Load() (*Config, error) {
	loadDotEnv(".env")
	port := cmp.Or(os.Getenv("PORT"), "8000")
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	region := cmp.Or(os.Getenv("AWS_REGION"), "ap-northeast-1")
	tableName := cmp.Or(os.Getenv("TABLE_NAME"), "TollgateAPIKeys")
	adminAPIKey := os.Getenv("ADMIN_API_KEY")

	// OpenAPI & Docs パス正規化 (空文字でなければ先頭スラッシュ補完)
	openapiPath := normalizePath(os.Getenv("OPENAPI_PATH"))
	docsPath := normalizePath(os.Getenv("DOCS_PATH"))

	// キーキャッシュ TTL (秒単位, 未指定時は 10 秒, 0 で無効化)
	keyCacheTTL := 10 * time.Second
	if ttlStr := os.Getenv("KEY_CACHE_TTL"); ttlStr != "" {
		if sec, err := strconv.Atoi(ttlStr); err == nil {
			if sec <= 0 {
				keyCacheTTL = 0
			} else {
				keyCacheTTL = time.Duration(sec) * time.Second
			}
		}
	}

	// ルート定義の読み込み
	routes, defaultTarget, err := loadRoutes()
	if err != nil {
		return nil, fmt.Errorf("failed to load proxy routes: %w", err)
	}

	// DB バックエンド (dynamodb | sqlite | cosmosdb | firestore, デフォルト: dynamodb)
	dbBackend := strings.ToLower(cmp.Or(os.Getenv("DB_BACKEND"), "dynamodb"))
	sqlitePath := cmp.Or(os.Getenv("SQLITE_PATH"), "./tollgate.db")

	cosmosEndpoint := os.Getenv("COSMOSDB_ENDPOINT")
	cosmosKey := os.Getenv("COSMOSDB_KEY")
	cosmosDB := cmp.Or(os.Getenv("COSMOSDB_DATABASE"), "tollgate")
	cosmosContainer := cmp.Or(os.Getenv("COSMOSDB_CONTAINER"), "api_keys")

	firestoreProjectID := os.Getenv("FIRESTORE_PROJECT_ID")
	firestoreDatabaseID := cmp.Or(os.Getenv("FIRESTORE_DATABASE_ID"), "(default)")
	firestoreCollection := cmp.Or(os.Getenv("FIRESTORE_COLLECTION"), "api_keys")

	// レートリミットバックエンド (memory | redis | valkey | two-tier | dynamodb | none, デフォルト: memory)
	rateLimitBackend := strings.ToLower(cmp.Or(os.Getenv("RATE_LIMIT_BACKEND"), "memory"))
	if rateLimitBackend == "two_tier" || rateLimitBackend == "tiered" {
		rateLimitBackend = "two-tier"
	}

	// SQLite の場合は外部依存ゼロ・キャッシュなし・レートリミット memory / none を強制
	if dbBackend == "sqlite" && (rateLimitBackend != "none") {
		rateLimitBackend = "memory"
	}

	// Redis 接続設定
	redisAddr := cmp.Or(os.Getenv("REDIS_ADDR"), "redis:6379")
	redisPassword := os.Getenv("REDIS_PASSWORD")
	redisDB := 0
	if dbStr := os.Getenv("REDIS_DB"); dbStr != "" {
		if db, err := strconv.Atoi(dbStr); err == nil {
			redisDB = db
		}
	}

	return &Config{
		Port:                port,
		DynamoDBEndpoint:    endpoint,
		AWSRegion:           region,
		TableName:           tableName,
		OpenAPIPath:         openapiPath,
		DocsPath:            docsPath,
		KeyCacheTTL:         keyCacheTTL,
		AdminAPIKey:         adminAPIKey,
		Routes:              routes,
		ForwardTargetURL:    defaultTarget,
		DBBackend:           dbBackend,
		SQLitePath:          sqlitePath,
		CosmosDBEndpoint:    cosmosEndpoint,
		CosmosDBKey:         cosmosKey,
		CosmosDBDatabase:    cosmosDB,
		CosmosDBContainer:   cosmosContainer,
		FirestoreProjectID:  firestoreProjectID,
		FirestoreDatabaseID: firestoreDatabaseID,
		FirestoreCollection: firestoreCollection,
		RateLimitBackend:    rateLimitBackend,
		RedisAddr:           redisAddr,
		RedisPassword:       redisPassword,
		RedisDB:             redisDB,
	}, nil
}

func normalizePath(p string) string {
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

func loadRoutes() ([]*RouteConfig, string, error) {
	var routes []*RouteConfig

	// 1. 設定ファイルからの読み込み (ROUTES_CONFIG_FILE)
	if configFile := os.Getenv("ROUTES_CONFIG_FILE"); configFile != "" {
		if data, err := os.ReadFile(configFile); err == nil {
			var parsed []*RouteConfig
			if err := json.Unmarshal(data, &parsed); err == nil {
				routes = append(routes, parsed...)
			} else {
				log.Printf("[tollgate] Warning: failed to parse ROUTES_CONFIG_FILE: %v", err)
			}
		} else {
			log.Printf("[tollgate] Warning: failed to read ROUTES_CONFIG_FILE: %v", err)
		}
	}

	// 2. PROXY_ROUTES JSON 環境変数の読み込み
	if jsonEnv := os.Getenv("PROXY_ROUTES"); jsonEnv != "" {
		var parsed []*RouteConfig
		if err := json.Unmarshal([]byte(jsonEnv), &parsed); err == nil {
			routes = append(routes, parsed...)
		} else {
			log.Printf("[tollgate] Warning: failed to parse PROXY_ROUTES JSON: %v", err)
		}
	}

	defaultTarget := os.Getenv("FORWARD_TARGET_URL")

	return routes, defaultTarget, nil
}
