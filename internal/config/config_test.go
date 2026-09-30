package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"/openapi.json", "/openapi.json"},
		{"openapi.json", "/openapi.json"},
		{"/docs", "/docs"},
		{"docs", "/docs"},
	}

	for _, tt := range tests {
		actual := normalizePath(tt.input)
		if actual != tt.expected {
			t.Errorf("normalizePath(%q) = %q, want %q", tt.input, actual, tt.expected)
		}
	}
}

func TestLoad_DefaultAndPath(t *testing.T) {
	os.Setenv("OPENAPI_PATH", "/openapi.json")
	os.Setenv("DOCS_PATH", "docs")
	defer func() {
		os.Unsetenv("OPENAPI_PATH")
		os.Unsetenv("DOCS_PATH")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.OpenAPIPath != "/openapi.json" {
		t.Errorf("expected /openapi.json, got %s", cfg.OpenAPIPath)
	}
	if cfg.DocsPath != "/docs" {
		t.Errorf("expected /docs, got %s", cfg.DocsPath)
	}
}

func TestLoad_CustomConfigurations(t *testing.T) {
	os.Setenv("PORT", "9000")
	os.Setenv("STORAGE_BACKEND", "cosmosdb")
	os.Setenv("COSMOSDB_ENDPOINT", "https://test.documents.azure.com:443/")
	os.Setenv("COSMOSDB_KEY", "primary-key")
	os.Setenv("COSMOSDB_DATABASE", "custom_db")
	os.Setenv("COSMOSDB_CONTAINER", "custom_container")
	os.Setenv("RATE_LIMIT_BACKEND", "two-tier")
	os.Setenv("REDIS_ADDR", "localhost:6380")
	os.Setenv("REDIS_DB", "2")
	os.Setenv("KEY_CACHE_TTL", "30")
	os.Setenv("PROXY_ROUTES", `[{"prefix":"/test","target":"http://test-svc:8080","strip_prefix":true}]`)
	os.Setenv("FORWARD_TARGET_URL", "http://fallback:8080")

	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("STORAGE_BACKEND")
		os.Unsetenv("COSMOSDB_ENDPOINT")
		os.Unsetenv("COSMOSDB_KEY")
		os.Unsetenv("COSMOSDB_DATABASE")
		os.Unsetenv("COSMOSDB_CONTAINER")
		os.Unsetenv("RATE_LIMIT_BACKEND")
		os.Unsetenv("REDIS_ADDR")
		os.Unsetenv("REDIS_DB")
		os.Unsetenv("KEY_CACHE_TTL")
		os.Unsetenv("PROXY_ROUTES")
		os.Unsetenv("FORWARD_TARGET_URL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Port != "9000" {
		t.Errorf("expected Port=9000, got %s", cfg.Port)
	}
	if cfg.DBBackend != "cosmosdb" {
		t.Errorf("expected DBBackend=cosmosdb, got %s", cfg.DBBackend)
	}
	if cfg.CosmosDBEndpoint != "https://test.documents.azure.com:443/" {
		t.Errorf("expected CosmosDBEndpoint match, got %s", cfg.CosmosDBEndpoint)
	}
	if cfg.RateLimitBackend != "two-tier" {
		t.Errorf("expected RateLimitBackend=two-tier, got %s", cfg.RateLimitBackend)
	}
	if cfg.KeyCacheTTL != 30*time.Second {
		t.Errorf("expected KeyCacheTTL=30s, got %v", cfg.KeyCacheTTL)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Prefix != "/test" {
		t.Errorf("expected 1 route with prefix /test")
	}
	if cfg.ForwardTargetURL != "http://fallback:8080" {
		t.Errorf("expected ForwardTargetURL match")
	}
}

func TestLoad_FirestoreAndRoutesFile(t *testing.T) {
	tmpDir := t.TempDir()
	routeFile := filepath.Join(tmpDir, "routes.json")
	_ = os.WriteFile(routeFile, []byte(`[{"prefix":"/file-route","target":"http://file-svc:80"}]`), 0644)

	os.Setenv("STORAGE_BACKEND", "firestore")
	os.Setenv("FIRESTORE_PROJECT_ID", "my-project")
	os.Setenv("RATE_LIMIT_BACKEND", "memory")
	os.Setenv("ROUTES_CONFIG_FILE", routeFile)
	os.Setenv("KEY_CACHE_TTL", "0")

	defer func() {
		os.Unsetenv("STORAGE_BACKEND")
		os.Unsetenv("FIRESTORE_PROJECT_ID")
		os.Unsetenv("RATE_LIMIT_BACKEND")
		os.Unsetenv("ROUTES_CONFIG_FILE")
		os.Unsetenv("KEY_CACHE_TTL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.DBBackend != "firestore" || cfg.FirestoreProjectID != "my-project" {
		t.Errorf("unexpected firestore config: %+v", cfg)
	}
	if cfg.RateLimitBackend != "memory" {
		t.Errorf("expected RateLimitBackend=memory, got %s", cfg.RateLimitBackend)
	}
	if cfg.KeyCacheTTL != 0 {
		t.Errorf("expected KeyCacheTTL=0, got %v", cfg.KeyCacheTTL)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0].Prefix != "/file-route" {
		t.Errorf("expected routes from file, got %+v", cfg.Routes)
	}
}

func TestLoad_DynamoDBTableNameAndValkeyURL(t *testing.T) {
	os.Setenv("DYNAMODB_TABLE_NAME", "CustomKeysTable")
	os.Setenv("VALKEY_URL", "redis://:secretpass@127.0.0.1:6390/3")
	defer func() {
		os.Unsetenv("DYNAMODB_TABLE_NAME")
		os.Unsetenv("VALKEY_URL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.TableName != "CustomKeysTable" {
		t.Errorf("expected TableName=CustomKeysTable, got %s", cfg.TableName)
	}
	if cfg.RedisAddr != "127.0.0.1:6390" {
		t.Errorf("expected RedisAddr=127.0.0.1:6390, got %s", cfg.RedisAddr)
	}
	if cfg.RedisPassword != "secretpass" {
		t.Errorf("expected RedisPassword=secretpass, got %s", cfg.RedisPassword)
	}
	if cfg.RedisDB != 3 {
		t.Errorf("expected RedisDB=3, got %d", cfg.RedisDB)
	}
}

func TestLoadDotEnv(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	content := `
# Comment line
TEST_DOTENV_KEY1=val1
TEST_DOTENV_KEY2="val2"
TEST_DOTENV_KEY3='val3'
`
	_ = os.WriteFile(envPath, []byte(content), 0644)

	loadDotEnv(envPath)
	defer func() {
		os.Unsetenv("TEST_DOTENV_KEY1")
		os.Unsetenv("TEST_DOTENV_KEY2")
		os.Unsetenv("TEST_DOTENV_KEY3")
	}()

	if os.Getenv("TEST_DOTENV_KEY1") != "val1" {
		t.Errorf("expected val1, got %s", os.Getenv("TEST_DOTENV_KEY1"))
	}
	if os.Getenv("TEST_DOTENV_KEY2") != "val2" {
		t.Errorf("expected val2, got %s", os.Getenv("TEST_DOTENV_KEY2"))
	}
	if os.Getenv("TEST_DOTENV_KEY3") != "val3" {
		t.Errorf("expected val3, got %s", os.Getenv("TEST_DOTENV_KEY3"))
	}
}
