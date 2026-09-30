package http

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/usecase"
	"log"
)

type VerifyKeyRequest struct {
	Body entity.VerifyKeyInput
}

type VerifyKeyResponse struct {
	Body entity.VerifyKeyOutput
}

func RegisterVerifyHandler(api huma.API, u *usecase.VerifyUsecase) {
	handler := func(ctx context.Context, input *VerifyKeyRequest) (*VerifyKeyResponse, error) {
		out, err := u.VerifyKey(ctx, input.Body)
		if err != nil {
			log.Printf("[ERROR] %s: %v", "キー検証処理エラー", err)
			return nil, huma.Error500InternalServerError("キー検証処理エラー")
		}
		return &VerifyKeyResponse{Body: *out}, nil
	}

	huma.Register(api, huma.Operation{
		OperationID: "verifyApiKey",
		Method:      http.MethodPost,
		Path:        "/v1/admin/verify",
		Summary:     "キー検証 & レートリミット消費",
		Description: "各 Web API (ai_engine / mcp_gateway / llm_gateway) がクライアントから受け取った API キーの有効性、スコープ合致、RPM レート消費、月間クォータ残量を検証します。",
		Tags:        []string{"Verification (Admin)"},
		Security:    []map[string][]string{{"adminBearerAuth": {}}, {"adminApiKeyAuth": {}}},
	}, handler)
}
