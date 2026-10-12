package http

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/kanmon-hq/tollgate/internal/config"
)

// ScopeItem は設定可能なスコープの定義情報
type ScopeItem struct {
	Scope       string `json:"scope" example:"billing:admin"`
	Prefix      string `json:"prefix,omitempty" example:"/billing"`
	Path        string `json:"path,omitempty" example:"/approve"`
	Method      string `json:"method,omitempty" example:"POST"`
	Description string `json:"description,omitempty" example:"請求書承認エンドポイント"`
	Source      string `json:"source" example:"path_rule"` // "route" | "path_rule"
}

// ListScopesRequest は設定可能スコープ一覧の取得リクエスト
type ListScopesRequest struct{}

// ListScopesResponse は設定可能スコープ一覧のレスポンス
type ListScopesResponse struct {
	Body struct {
		Scopes []ScopeItem `json:"scopes"`
	}
}

// RegisterScopeHandlers はスコープ一覧取得 API を登録する
func RegisterScopeHandlers(api huma.API, routes []*config.RouteConfig) {
	adminSec := []map[string][]string{{"adminBearerAuth": {}}, {"adminApiKeyAuth": {}}}

	huma.Register(api, huma.Operation{
		OperationID: "listAvailableScopes",
		Method:      http.MethodGet,
		Path:        "/v1/admin/scopes",
		Summary:     "設定可能スコープ一覧取得",
		Description: "プロキシのルーティング設定（RouteConfig/PathRules）に定義されている設定可能なスコープ一覧を取得します。",
		Tags:        []string{"Keys (Admin)"},
		Security:    adminSec,
	}, func(ctx context.Context, input *ListScopesRequest) (*ListScopesResponse, error) {
		scopeMap := make(map[string]ScopeItem)

		// ルート設定 (RouteConfig / PathRules) から収集
		for _, r := range routes {
			if r == nil {
				continue
			}
			// ルート全体のスコープ
			if r.Scope != "" {
				scopeMap[r.Scope] = ScopeItem{
					Scope:       r.Scope,
					Prefix:      r.Prefix,
					Description: "ルート全体アクセス (" + r.Prefix + ")",
					Source:      "route",
				}
			}

			// パス・メソッドごとの詳細スコープ
			for _, pr := range r.PathRules {
				if pr == nil || pr.Scope == "" {
					continue
				}
				desc := pr.Description
				if desc == "" {
					desc = "パス制限ルール (" + r.Prefix + pr.Path + ")"
				}
				key := pr.Scope + ":" + pr.Method + ":" + pr.Path
				scopeMap[key] = ScopeItem{
					Scope:       pr.Scope,
					Prefix:      r.Prefix,
					Path:        pr.Path,
					Method:      strings.ToUpper(pr.Method),
					Description: desc,
					Source:      "path_rule",
				}
			}
		}

		// リスト化およびソート
		result := make([]ScopeItem, 0, len(scopeMap))
		for _, item := range scopeMap {
			result = append(result, item)
		}

		sort.Slice(result, func(i, j int) bool {
			if result[i].Scope != result[j].Scope {
				return result[i].Scope < result[j].Scope
			}
			return result[i].Path < result[j].Path
		})

		resp := &ListScopesResponse{}
		resp.Body.Scopes = result
		return resp, nil
	})
}
