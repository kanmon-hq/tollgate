package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/northfieldzz/tollgate/internal/config"
	deliveryHttp "github.com/northfieldzz/tollgate/internal/delivery/http"
	"github.com/northfieldzz/tollgate/internal/domain/repository"
	"github.com/northfieldzz/tollgate/internal/infrastructure/cache"
	infraCosmos "github.com/northfieldzz/tollgate/internal/infrastructure/cosmosdb"
	infraDynamo "github.com/northfieldzz/tollgate/internal/infrastructure/dynamodb"
	infraFirestore "github.com/northfieldzz/tollgate/internal/infrastructure/firestore"
	"github.com/northfieldzz/tollgate/internal/infrastructure/ratelimit"
	"github.com/northfieldzz/tollgate/internal/infrastructure/sqlrepo"
	"github.com/northfieldzz/tollgate/internal/usecase"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. 環境設定の読み込み
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[tollgate] Failed to load configuration: %v", err)
	}

	log.Printf("[tollgate] Initializing with DBBackend=%s, RateLimitBackend=%s",
		cfg.DBBackend, cfg.RateLimitBackend)

	var (
		repo         repository.KeyRepository
		dynamoClient *dynamodb.Client
	)

	// 2. DynamoDB クライアントの初期化 (DBBackend=dynamodb または RateLimitBackend=dynamodb の場合のみ)
	if cfg.DBBackend == "dynamodb" || cfg.RateLimitBackend == "dynamodb" {
		log.Printf("[tollgate] Initializing AWS DynamoDB (Region=%s, Endpoint=%s, Table=%s)",
			cfg.AWSRegion, cfg.DynamoDBEndpoint, cfg.TableName)

		var optFns []func(*awsConfig.LoadOptions) error
		optFns = append(optFns, awsConfig.WithRegion(cfg.AWSRegion))

		if cfg.DynamoDBEndpoint != "" {
			customResolver := aws.EndpointResolverWithOptionsFunc(func(service, reg string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					PartitionID:   "aws",
					URL:           cfg.DynamoDBEndpoint,
					SigningRegion: cfg.AWSRegion,
				}, nil
			})
			optFns = append(optFns,
				awsConfig.WithEndpointResolverWithOptions(customResolver),
				awsConfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("dummy", "dummy", "")),
			)
		}

		awsCfg, err := awsConfig.LoadDefaultConfig(context.Background(), optFns...)
		if err != nil {
			log.Fatalf("failed to load AWS config: %v", err)
		}
		dynamoClient = dynamodb.NewFromConfig(awsCfg)
	}

	// 3. データベースリポジトリの初期化
	switch cfg.DBBackend {
	case "sqlite":
		log.Printf("[tollgate] DB Backend: SQLite (path=%s, zero-dependency, cache=disabled)", cfg.SQLitePath)
		sqliteRepo, err := sqlrepo.NewSQLiteRepository(context.Background(), cfg.SQLitePath)
		if err != nil {
			log.Fatalf("[tollgate] Failed to initialize SQLite repository: %v", err)
		}
		repo = sqliteRepo
		// SQLite はキャッシュなし (ダイレクトアクセス)

	case "cosmosdb":
		log.Printf("[tollgate] DB Backend: Azure Cosmos DB (Endpoint=%s, DB=%s, Container=%s)",
			cfg.CosmosDBEndpoint, cfg.CosmosDBDatabase, cfg.CosmosDBContainer)
		cosmosRepo, err := infraCosmos.NewCosmosDBRepository(
			cfg.CosmosDBEndpoint,
			cfg.CosmosDBKey,
			cfg.CosmosDBDatabase,
			cfg.CosmosDBContainer,
		)
		if err != nil {
			log.Fatalf("[tollgate] Failed to initialize Azure Cosmos DB repository: %v", err)
		}
		repo = cosmosRepo
		if cfg.KeyCacheTTL > 0 {
			repo = cache.NewCachedKeyRepository(repo, cfg.KeyCacheTTL)
		}

	case "firestore":
		log.Printf("[tollgate] DB Backend: Google Cloud Firestore (Project=%s, DB=%s, Collection=%s)",
			cfg.FirestoreProjectID, cfg.FirestoreDatabaseID, cfg.FirestoreCollection)
		firestoreRepo, err := infraFirestore.NewFirestoreRepository(
			context.Background(),
			cfg.FirestoreProjectID,
			cfg.FirestoreDatabaseID,
			cfg.FirestoreCollection,
		)
		if err != nil {
			log.Fatalf("[tollgate] Failed to initialize Firestore repository: %v", err)
		}
		defer firestoreRepo.Close()
		repo = firestoreRepo
		if cfg.KeyCacheTTL > 0 {
			repo = cache.NewCachedKeyRepository(repo, cfg.KeyCacheTTL)
		}

	case "dynamodb":
		fallthrough
	default:
		log.Printf("[tollgate] DB Backend: DynamoDB (table=%s, CacheTTL=%v)", cfg.TableName, cfg.KeyCacheTTL)
		repo = infraDynamo.NewDynamoDBRepository(dynamoClient, cfg.TableName)
		if cfg.KeyCacheTTL > 0 {
			repo = cache.NewCachedKeyRepository(repo, cfg.KeyCacheTTL)
		}
	}

	// 4. レートリミッター初期化 (RATE_LIMIT_BACKEND に応じてバックエンドを切り替え)
	var limiter repository.RateLimiter
	switch cfg.RateLimitBackend {
	case "none":
		log.Printf("[tollgate] Rate limiter backend: None (Rate limiting disabled)")
		limiter = ratelimit.NewNoopRateLimiter()

	case "dynamodb":
		if dynamoClient == nil {
			log.Fatalf("[tollgate] DynamoDB client is not available for rate limiter")
		}
		log.Printf("[tollgate] Rate limiter backend: DynamoDB (Fixed Window, table=%s)", cfg.TableName)
		limiter = ratelimit.NewDynamoDBRateLimiter(dynamoClient, cfg.TableName)

	case "redis", "valkey":
		log.Printf("[tollgate] Rate limiter backend: Redis / Valkey (Sliding Window, addr=%s, db=%d)", cfg.RedisAddr, cfg.RedisDB)
		redisClient := redis.NewClient(&redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       cfg.RedisDB,
		})
		// 起動時に接続確認 (未接続の場合は Fail-Fast)
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			log.Fatalf("[tollgate] Failed to connect to Redis/Valkey (%s): %v", cfg.RedisAddr, err)
		}
		limiter = ratelimit.NewRedisRateLimiter(redisClient, time.Minute)

	case "two-tier":
		log.Printf("[tollgate] Rate limiter backend: Two-Tier Cache (L1: In-Memory + L2: Redis/Valkey @ %s)", cfg.RedisAddr)
		redisClient := redis.NewClient(&redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       cfg.RedisDB,
		})
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			log.Fatalf("[tollgate] Failed to connect to Redis/Valkey for Two-Tier limiter (%s): %v", cfg.RedisAddr, err)
		}
		l1 := ratelimit.NewInMemoryRateLimiter(time.Minute)
		l2 := ratelimit.NewRedisRateLimiter(redisClient, time.Minute)
		twoTierLimiter := ratelimit.NewTwoTierRateLimiter(l1, l2)
		defer twoTierLimiter.Stop()
		limiter = twoTierLimiter

	case "memory":
		fallthrough
	default:
		log.Printf("[tollgate] Rate limiter backend: InMemory (Sliding Window)")
		inMemoryLimiter := ratelimit.NewInMemoryRateLimiter(time.Minute)
		defer inMemoryLimiter.Stop()
		limiter = inMemoryLimiter
	}

	keyUsecase := usecase.NewKeyUsecase(repo)
	verifyUsecase := usecase.NewVerifyUsecase(repo, limiter)

	// 5. 動的ルート定義 & リバースプロキシ構築
	var proxyHandler http.Handler
	if len(cfg.Routes) > 0 || cfg.ForwardTargetURL != "" {
		proxy, err := deliveryHttp.NewMultiTargetProxy(cfg.Routes, cfg.ForwardTargetURL, verifyUsecase)
		if err != nil {
			log.Fatalf("failed to initialize proxy: %v", err)
		}
		proxyHandler = proxy
	}

	// 6. ルーター・HTTP サーバー構築
	router := deliveryHttp.NewRouter(cfg, keyUsecase, verifyUsecase, repo, proxyHandler)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second, // Slowloris 対策
		IdleTimeout:       120 * time.Second,
		// SSE (Server-Sent Events) および WebSocket の常時接続を維持するため Read/WriteTimeout は無制限 (0)
	}

	// 7. サーバー起動
	go func() {
		if proxyHandler != nil {
			log.Printf("[tollgate] Reverse Proxy mode active with %d route(s):", len(cfg.Routes))
			for _, r := range cfg.Routes {
				log.Printf("[tollgate]   -> Prefix: %-8s => Target: %-30s (Scope: %-10s, StripPrefix: %v)", r.Prefix, r.Target, r.Scope, r.StripPrefix)
			}
			if cfg.ForwardTargetURL != "" {
				log.Printf("[tollgate]   -> Default fallback => %s", cfg.ForwardTargetURL)
			}
		} else {
			log.Println("[tollgate] Running in standalone API mode (No proxy routes configured)")
		}

		var infoMsgs []string
		if cfg.OpenAPIPath != "" {
			infoMsgs = append(infoMsgs, fmt.Sprintf("OpenAPI at http://localhost:%s%s", cfg.Port, cfg.OpenAPIPath))
		}
		if cfg.DocsPath != "" {
			infoMsgs = append(infoMsgs, fmt.Sprintf("Docs at http://localhost:%s%s", cfg.Port, cfg.DocsPath))
		}

		infoStr := ""
		if len(infoMsgs) > 0 {
			infoStr = " (" + strings.Join(infoMsgs, ", ") + ")"
		}
		log.Printf("[tollgate] Server listening on :%s%s", cfg.Port, infoStr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	// 8. Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[tollgate] Shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("[tollgate] Forced shutdown: %v", err)
	}
	log.Println("[tollgate] Server stopped.")
}
