package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/kanmon-hq/tollgate/internal/domain/entity"
	"github.com/kanmon-hq/tollgate/internal/usecase"
	"log"
)

type CreateKeyRequest struct {
	Body entity.CreateKeyInput
}

type CreateKeyResponse struct {
	Body entity.CreateKeyOutput
}

type ListKeysRequest struct {
	TenantID string `query:"tenant_id" doc:"対象テナント ID" required:"true" example:"dept-risk-01"`
}

type ListKeysResponse struct {
	Body struct {
		Keys []*entity.APIKey `json:"keys"`
	}
}

type KeyIDParam struct {
	KeyID string `path:"key_id" doc:"API キー ID (UUID)" example:"550e8400-e29b-41d4-a716-446655440000"`
}

type GetKeyResponse struct {
	Body entity.APIKey
}

type UpdateKeyRequest struct {
	KeyIDParam
	Body entity.UpdateKeyInput
}

type RotateKeyRequest struct {
	KeyIDParam
	Body entity.RotateKeyInput
}

type RotateKeyResponse struct {
	Body entity.RotateKeyOutput
}

type DeleteKeyResponse struct {
	Body struct {
		Message string `json:"message" example:"key deleted successfully"`
	}
}

func RegisterKeyHandlers(api huma.API, u *usecase.KeyUsecase) {
	adminSec := []map[string][]string{{"adminBearerAuth": {}}, {"adminApiKeyAuth": {}}}

	// 1. POST /v1/admin/keys
	huma.Register(api, huma.Operation{
		OperationID: "createApiKey",
		Method:      http.MethodPost,
		Path:        "/v1/admin/keys",
		Summary:     "API キー新規発行",
		Description: "指定されたテナントおよびスコープに対応する新しい API キーを発行します。平文キーは本レスポンスで 1 度だけ返却されます。",
		Tags:        []string{"Keys (Admin)"},
		Security:    adminSec,
	}, func(ctx context.Context, input *CreateKeyRequest) (*CreateKeyResponse, error) {
		out, err := u.CreateKey(ctx, input.Body)
		if err != nil {
			if errors.Is(err, entity.ErrServiceIDRequired) || errors.Is(err, entity.ErrTenantOrServiceRequired) {
				log.Printf("[ERROR] 無効な入力パラメータ: %v", err)
				return nil, huma.Error400BadRequest("無効な入力パラメータ")
			}
			log.Printf("[ERROR] %s: %v", "キー発行失敗", err)
			return nil, huma.Error500InternalServerError("キー発行失敗")
		}
		return &CreateKeyResponse{Body: *out}, nil
	})

	// 2. GET /v1/admin/keys
	huma.Register(api, huma.Operation{
		OperationID: "listApiKeys",
		Method:      http.MethodGet,
		Path:        "/v1/admin/keys",
		Summary:     "テナント別キー一覧取得",
		Description: "指定テナントに所属する API キーの一覧を取得します (平文はマスクされます)。",
		Tags:        []string{"Keys (Admin)"},
		Security:    adminSec,
	}, func(ctx context.Context, input *ListKeysRequest) (*ListKeysResponse, error) {
		keys, err := u.ListKeys(ctx, input.TenantID)
		if err != nil {
			log.Printf("[ERROR] %s: %v", "キー一覧取得失敗", err)
			return nil, huma.Error500InternalServerError("キー一覧取得失敗")
		}
		if keys == nil {
			keys = []*entity.APIKey{}
		}
		return &ListKeysResponse{Body: struct {
			Keys []*entity.APIKey `json:"keys"`
		}{Keys: keys}}, nil
	})

	// 3. GET /v1/admin/keys/{key_id}
	huma.Register(api, huma.Operation{
		OperationID: "getApiKey",
		Method:      http.MethodGet,
		Path:        "/v1/admin/keys/{key_id}",
		Summary:     "キー詳細取得",
		Description: "指定された KeyID の設定、ステータス、当月消費量、最終利用日時を取得します。",
		Tags:        []string{"Keys (Admin)"},
		Security:    adminSec,
	}, func(ctx context.Context, input *KeyIDParam) (*GetKeyResponse, error) {
		key, err := u.GetKey(ctx, input.KeyID)
		if err != nil {
			log.Printf("[ERROR] %s: %v", "キーが見つかりません", err)
			return nil, huma.Error404NotFound("キーが見つかりません")
		}
		return &GetKeyResponse{Body: *key}, nil
	})

	// 4. PATCH /v1/admin/keys/{key_id}
	huma.Register(api, huma.Operation{
		OperationID: "updateApiKey",
		Method:      http.MethodPatch,
		Path:        "/v1/admin/keys/{key_id}",
		Summary:     "キー設定変更",
		Description: "名前、スコープ、分間上限 (RPM)、月間クォータを即時更新します。",
		Tags:        []string{"Keys (Admin)"},
		Security:    adminSec,
	}, func(ctx context.Context, input *UpdateKeyRequest) (*GetKeyResponse, error) {
		key, err := u.UpdateKey(ctx, input.KeyID, input.Body)
		if err != nil {
			log.Printf("[ERROR] %s: %v", "設定更新失敗", err)
			return nil, huma.Error500InternalServerError("設定更新失敗")
		}
		return &GetKeyResponse{Body: *key}, nil
	})

	// 5. POST /v1/admin/keys/{key_id}/suspend
	huma.Register(api, huma.Operation{
		OperationID: "suspendApiKey",
		Method:      http.MethodPost,
		Path:        "/v1/admin/keys/{key_id}/suspend",
		Summary:     "キーの一時停止",
		Description: "キーを一時無効化し、以後の API アクセスを即座に遮断します。",
		Tags:        []string{"Keys (Admin)"},
		Security:    adminSec,
	}, func(ctx context.Context, input *KeyIDParam) (*GetKeyResponse, error) {
		key, err := u.SuspendKey(ctx, input.KeyID)
		if err != nil {
			log.Printf("[ERROR] %s: %v", "一時停止失敗", err)
			return nil, huma.Error500InternalServerError("一時停止失敗")
		}
		return &GetKeyResponse{Body: *key}, nil
	})

	// 6. POST /v1/admin/keys/{key_id}/resume
	huma.Register(api, huma.Operation{
		OperationID: "resumeApiKey",
		Method:      http.MethodPost,
		Path:        "/v1/admin/keys/{key_id}/resume",
		Summary:     "キーの再開",
		Description: "一時停止されていたキーを有効化し、API アクセスを再開します。",
		Tags:        []string{"Keys (Admin)"},
		Security:    adminSec,
	}, func(ctx context.Context, input *KeyIDParam) (*GetKeyResponse, error) {
		key, err := u.ResumeKey(ctx, input.KeyID)
		if err != nil {
			log.Printf("[ERROR] %s: %v", "再開失敗", err)
			return nil, huma.Error500InternalServerError("再開失敗")
		}
		return &GetKeyResponse{Body: *key}, nil
	})

	// 7. POST /v1/admin/keys/{key_id}/rotate
	huma.Register(api, huma.Operation{
		OperationID: "rotateApiKey",
		Method:      http.MethodPost,
		Path:        "/v1/admin/keys/{key_id}/rotate",
		Summary:     "ゼロダウンタイム・キーローテーション",
		Description: "新キーを発行し、旧キーを指定猶予期間 (grace_period) が経過するまで有効とします。",
		Tags:        []string{"Keys (Admin)"},
		Security:    adminSec,
	}, func(ctx context.Context, input *RotateKeyRequest) (*RotateKeyResponse, error) {
		out, err := u.RotateKey(ctx, input.KeyID, input.Body)
		if err != nil {
			log.Printf("[ERROR] %s: %v", "ローテーション失敗", err)
			return nil, huma.Error500InternalServerError("ローテーション失敗")
		}
		return &RotateKeyResponse{Body: *out}, nil
	})

	// 8. DELETE /v1/admin/keys/{key_id}
	huma.Register(api, huma.Operation{
		OperationID: "deleteApiKey",
		Method:      http.MethodDelete,
		Path:        "/v1/admin/keys/{key_id}",
		Summary:     "キーの削除",
		Description: "指定された API キーを物理削除・完全失効します。",
		Tags:        []string{"Keys (Admin)"},
		Security:    adminSec,
	}, func(ctx context.Context, input *KeyIDParam) (*DeleteKeyResponse, error) {
		if err := u.DeleteKey(ctx, input.KeyID); err != nil {
			log.Printf("[ERROR] %s: %v", "キー削除失敗", err)
			return nil, huma.Error500InternalServerError("キー削除失敗")
		}
		resp := &DeleteKeyResponse{}
		resp.Body.Message = "key deleted successfully"
		return resp, nil
	})
}
