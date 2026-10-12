package http

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kanmon-hq/tollgate/internal/config"
	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/usecase"
)

// DefaultUserContextHeaders は外部テナントキー経由のアクセス時にスプーフィング防止のため除去すべきヘッダー群 (要件 T-2)
var DefaultUserContextHeaders = []string{
	"X-User-Id",
	"X-User-ID",
	"X-User-Role",
	"X-User-Email",
	"X-User-Context",
	"X-Signature",
	"X-End-User-Id",
}

func getUserContextHeaders() []string {
	custom := os.Getenv("USER_CONTEXT_HEADERS")
	if custom == "" {
		return DefaultUserContextHeaders
	}
	parts := strings.Split(custom, ",")
	var res []string
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			res = append(res, trimmed)
		}
	}
	if len(res) == 0 {
		return DefaultUserContextHeaders
	}
	return res
}

// RouteConfig は動的ルーティングのルール定義 (config.RouteConfig のエイリアス)
type RouteConfig = config.RouteConfig

// PathRule はパスごとの詳細スコープルール定義 (config.PathRule のエイリアス)
type PathRule = config.PathRule

// routeEntry は初期化済みのルート情報
type routeEntry struct {
	config      *RouteConfig
	prefixSlash string
	target      *url.URL
	proxy       *httputil.ReverseProxy
}

// MultiTargetProxy はパスプレフィックスに基づいて振り分けるリバースプロキシ
type MultiTargetProxy struct {
	routes        []*routeEntry
	defaultProxy  *httputil.ReverseProxy
	defaultTarget *url.URL
	verifyUsecase *usecase.VerifyUsecase
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Reason  string `json:"reason,omitempty"`
}

func writeJSONError(w http.ResponseWriter, status int, errCode, message, reason string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Error:   errCode,
		Message: message,
		Reason:  reason,
	})
}

// gatewayTransport はリバースプロキシ専用にチューニングされた HTTP トランスポート
// アイドルコネクションプールを拡大し、高並行リクエスト時の TIME_WAIT 頻発とコネクション枯渇を防止する
var gatewayTransport = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          1024,
	MaxIdleConnsPerHost:   100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	ResponseHeaderTimeout: 60 * time.Second,
}

// createSingleProxy は単一ターゲット向けのリバースプロキシインスタンスを構築する
func createSingleProxy(target *url.URL, prefix string, stripPrefix bool) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = gatewayTransport
	originalDirector := proxy.Director

	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host

		// StripPrefix の処理
		if stripPrefix && prefix != "" {
			oldPath := req.URL.Path
			if strings.HasPrefix(oldPath, prefix) {
				newPath := strings.TrimPrefix(oldPath, prefix)
				if newPath == "" || !strings.HasPrefix(newPath, "/") {
					newPath = "/" + newPath
				}
				req.URL.Path = newPath
			}
		}
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[proxy] Error proxying to %s%q: %v", target.String(), r.URL.Path, err)
		writeJSONError(w, http.StatusBadGateway, "bad_gateway", "Backend service is unreachable", "")
	}

	// SSE 即時フラッシュ
	proxy.FlushInterval = -1

	return proxy
}

// NewMultiTargetProxy は設定リストから MultiTargetProxy を構築する
func NewMultiTargetProxy(routes []*RouteConfig, defaultTargetURL string, verifyUsecase *usecase.VerifyUsecase) (*MultiTargetProxy, error) {
	var entries []*routeEntry

	for _, cfg := range routes {
		if cfg.Prefix == "" || cfg.Target == "" {
			continue
		}
		// prefix を "/path" の形式に正規化
		prefix := cfg.Prefix
		if !strings.HasPrefix(prefix, "/") {
			prefix = "/" + prefix
		}
		prefix = strings.TrimSuffix(prefix, "/")

		target, err := url.Parse(cfg.Target)
		if err != nil {
			return nil, fmt.Errorf("invalid target URL %q for prefix %q: %w", cfg.Target, prefix, err)
		}

		proxy := createSingleProxy(target, prefix, cfg.StripPrefix)

		entries = append(entries, &routeEntry{
			config: &RouteConfig{
				Prefix:      prefix,
				Target:      cfg.Target,
				Scope:       cfg.Scope,
				StripPrefix: cfg.StripPrefix,
				PathRules:   cfg.PathRules,
			},
			prefixSlash: prefix + "/",
			target:      target,
			proxy:       proxy,
		})
	}

	var defProxy *httputil.ReverseProxy
	var defTarget *url.URL
	if defaultTargetURL != "" {
		dt, err := url.Parse(defaultTargetURL)
		if err != nil {
			return nil, fmt.Errorf("invalid default target URL %q: %w", defaultTargetURL, err)
		}
		defTarget = dt
		defProxy = createSingleProxy(dt, "", false)
	}

	return &MultiTargetProxy{
		routes:        entries,
		defaultProxy:  defProxy,
		defaultTarget: defTarget,
		verifyUsecase: verifyUsecase,
	}, nil
}

// LoadRoutesFromEnvOrConfig は環境変数や設定ファイルからルート定義を読み込む
func LoadRoutesFromEnvOrConfig() ([]*RouteConfig, string) {
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

	return routes, defaultTarget
}

// extractAPIKey はヘッダーから API キーを取得する
func extractAPIKey(r *http.Request) string {
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

// GetRoutes は設定されているルート一覧を返す
func (m *MultiTargetProxy) GetRoutes() []*RouteConfig {
	routes := make([]*RouteConfig, 0, len(m.routes))
	for _, entry := range m.routes {
		if entry.config != nil {
			routes = append(routes, entry.config)
		}
	}
	return routes
}

// matchPathPattern はパスパターンとの一致判定を行う
func matchPathPattern(pattern, targetPath string) bool {
	if pattern == "" || pattern == "*" || pattern == "/*" {
		return true
	}
	if pattern == targetPath {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		if strings.HasPrefix(targetPath, prefix) {
			return true
		}
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		if strings.HasPrefix(targetPath, prefix) {
			return true
		}
	}
	matched, err := path.Match(pattern, targetPath)
	return err == nil && matched
}

// resolveRequiredScope はパスルールに基づいてリクエストに必要なスコープを解決する
func (m *MultiTargetProxy) resolveRequiredScope(route *routeEntry, reqPath string, method string) string {
	if route == nil || route.config == nil {
		return ""
	}
	if len(route.config.PathRules) > 0 {
		subPath := reqPath
		if strings.HasPrefix(reqPath, route.config.Prefix) {
			subPath = strings.TrimPrefix(reqPath, route.config.Prefix)
			if !strings.HasPrefix(subPath, "/") {
				subPath = "/" + subPath
			}
		}

		for _, rule := range route.config.PathRules {
			if rule == nil {
				continue
			}
			if rule.Method != "" && !strings.EqualFold(rule.Method, method) {
				continue
			}
			if matchPathPattern(rule.Path, subPath) || matchPathPattern(rule.Path, reqPath) {
				return rule.Scope
			}
		}
	}
	return route.config.Scope
}

// matchRoute はリクエストパスにマッチする最長のルートを検索する
func (m *MultiTargetProxy) matchRoute(path string) *routeEntry {
	var bestMatch *routeEntry
	longestPrefixLen := -1

	for _, entry := range m.routes {
		prefix := entry.config.Prefix
		// 完全一致 または "/prefix/..." のプレフィックス一致
		if path == prefix || strings.HasPrefix(path, entry.prefixSlash) {
			if len(prefix) > longestPrefixLen {
				longestPrefixLen = len(prefix)
				bestMatch = entry
			}
		}
	}

	return bestMatch
}

func (m *MultiTargetProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// リクエスト ID 解決 (要件 C-3: 既存にあれば保持、未指定なら新規採番)
	reqID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
	if reqID == "" {
		reqID = uuid.New().String()
	}
	r.Header.Set("X-Request-Id", reqID)
	w.Header().Set("X-Request-Id", reqID)

	// 1. ルートマッチ判定
	route := m.matchRoute(r.URL.Path)
	if route == nil && m.defaultProxy == nil {
		writeJSONError(w, http.StatusNotFound, "not_found", "No matching route found for path", "")
		return
	}

	// 2. 認証キー抽出
	rawKey := extractAPIKey(r)
	if rawKey == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "API key is required in X-API-Key or Authorization header", "missing_api_key")
		return
	}

	// 3. 要求スコープの決定 (パスルールがあれば優先、なければルートのスコープを要求)
	requiredScope := m.resolveRequiredScope(route, r.URL.Path, r.Method)

	// 4. API キー検証 & レートリミット & スコープ判定
	verifyOut, err := m.verifyUsecase.VerifyKey(r.Context(), entity.VerifyKeyInput{
		RawKey:        rawKey,
		RequiredScope: requiredScope,
	})
	if err != nil {
		log.Printf("[proxy] Internal error verifying key: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal_error", "Failed to verify API key", "")
		return
	}

	if !verifyOut.Valid {
		switch verifyOut.Reason {
		case "rate_limit_exceeded":
			w.Header().Set("Retry-After", "60")
			writeJSONError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "Rate limit exceeded. Please retry later.", verifyOut.Reason)
		case "quota_exceeded":
			writeJSONError(w, http.StatusTooManyRequests, "quota_exceeded", "Monthly quota exceeded.", verifyOut.Reason)
		case "scope_mismatch":
			writeJSONError(w, http.StatusForbidden, "forbidden", fmt.Sprintf("API key does not have required scope %q for this service", requiredScope), verifyOut.Reason)
		case "expired", "rotation_expired":
			writeJSONError(w, http.StatusForbidden, "key_expired", "API key has expired.", verifyOut.Reason)
		case "suspended", "revoked":
			writeJSONError(w, http.StatusForbidden, "key_disabled", "API key is suspended or revoked.", verifyOut.Reason)
		default:
			writeJSONError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API key.", verifyOut.Reason)
		}
		return
	}

	// 5. レートリミットヘッダー付与
	w.Header().Set("X-RateLimit-Limit-RPM", strconv.Itoa(verifyOut.LimitRPM))
	w.Header().Set("X-RateLimit-Remaining-RPM", strconv.Itoa(verifyOut.RemainingRPM))
	if verifyOut.MonthlyQuota > 0 {
		w.Header().Set("X-Quota-Limit", strconv.FormatInt(verifyOut.MonthlyQuota, 10))
		w.Header().Set("X-Quota-Remaining", strconv.FormatInt(verifyOut.RemainingQuota, 10))
	}

	// 6. テナント & サービス解決とコンフリクト検証
	clientTenantID := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
	clientServiceID := strings.TrimSpace(r.Header.Get("X-Service-ID"))

	// クライアントからのヘッダースプーフィングを防ぐため、事前に削除
	r.Header.Del("X-Tenant-ID")
	r.Header.Del("X-Key-ID")
	r.Header.Del("X-Key-Prefix")
	r.Header.Del("X-Service-ID")

	// ServiceID コンフリクト検証
	var resolvedServiceID string
	if verifyOut.ServiceID != "" {
		if clientServiceID != "" && clientServiceID != verifyOut.ServiceID {
			writeJSONError(w, http.StatusForbidden, "service_mismatch",
				fmt.Sprintf("API key is bound to service %q, but request specified %q", verifyOut.ServiceID, clientServiceID),
				"service_conflict")
			return
		}
		resolvedServiceID = verifyOut.ServiceID
	} else if clientServiceID != "" {
		resolvedServiceID = clientServiceID
	}

	var resolvedTenantID string
	if verifyOut.TenantID != "" {
		// 【テナントキー (固定テナント)】
		if clientTenantID != "" && clientTenantID != verifyOut.TenantID {
			writeJSONError(w, http.StatusForbidden, "tenant_mismatch",
				fmt.Sprintf("API key is bound to tenant %q, but request specified %q", verifyOut.TenantID, clientTenantID),
				"tenant_conflict")
			return
		}
		resolvedTenantID = verifyOut.TenantID

		// 要件 T-2: テナントキー経由の場合、なりすまし防止のためユーザーコンテキストヘッダーを強制除去 (Strip)
		for _, h := range getUserContextHeaders() {
			r.Header.Del(h)
		}
	} else {
		// 【サービスキー (動的マルチテナント)】
		if clientTenantID == "" {
			writeJSONError(w, http.StatusBadRequest, "missing_tenant_id",
				"X-Tenant-ID header is required when using a service API key without a bound tenant",
				"tenant_required")
			return
		}
		resolvedTenantID = clientTenantID

		// 要件 T-2: サービスキー (BFF 等) 経由の場合はユーザーコンテキストヘッダーを除去せず透過フォワード
	}

	r.Header.Set("X-Tenant-ID", resolvedTenantID)
	r.Header.Set("X-Key-ID", verifyOut.KeyID)
	r.Header.Set("X-Key-Prefix", verifyOut.KeyPrefix)
	if resolvedServiceID != "" {
		r.Header.Set("X-Service-ID", resolvedServiceID)
	}

	// 7. 適切なプロキシに転送
	if route != nil {
		route.proxy.ServeHTTP(w, r)
	} else {
		m.defaultProxy.ServeHTTP(w, r)
	}
}
