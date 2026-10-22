package subjective

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	database "edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"github.com/google/uuid"
)

type MaterialExecutionPermit struct {
	RunID              string `json:"run_id"`
	ExecutionAttemptID string `json:"execution_attempt_id"`
	PermitID           string `json:"permit_id"`
	Credential         string `json:"credential"`
}
type MaterialReceiptStore struct {
	db     *sql.DB
	secret string
	admit  func(context.Context, string, string, string) error
}

func NewMaterialReceiptStore(db *sql.DB, secret string) *MaterialReceiptStore {
	if db == nil || len(secret) < 32 {
		return nil
	}
	return &MaterialReceiptStore{db: db, secret: secret}
}
func (s *MaterialReceiptStore) WithImageAdmission(admit func(context.Context, string, string, string) error) *MaterialReceiptStore {
	if s != nil {
		s.admit = admit
	}
	return s
}
func (s *MaterialReceiptStore) credential(tenant, run, id string) string {
	h := hmac.New(sha256.New, []byte(s.secret))
	h.Write([]byte("material-permit-v1:" + tenant + ":" + run + ":" + id))
	return tenant + ":" + hex.EncodeToString(h.Sum(nil))
}
func materialCredentialHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

// Prepare 在领域 run 内建立唯一执行身份。消费过的许可绝不重新生成，
// 重启或租约接管也只能读取原事实，不能把不确定请求自动重发。
func (s *MaterialReceiptStore) Prepare(ctx context.Context, tenant, requestID, digest, wrapper string, media []ReferenceMedia) (MaterialExecutionPermit, error) {
	var p MaterialExecutionPermit
	if s == nil || !validLowerSHA256(digest) || !validLowerSHA256(wrapper) || len(media) == 0 || len(media) > maxReferenceMedia {
		return p, &GradingAgentError{Code: "material_execution_unavailable"}
	}
	safe := make([]map[string]any, 0, len(media))
	for _, m := range media {
		safe = append(safe, map[string]any{"media_id": m.MediaID, "role": m.Role, "ordinal": m.Ordinal, "sha256": m.SHA256, "width_pixels": m.WidthPixels, "height_pixels": m.HeightPixels, "byte_size": m.ByteSize})
	}
	raw, _ := json.Marshal(safe)
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return p, e
	}
	defer tx.Rollback()
	var manifest, hash, status string
	var revision int
	e = tx.QueryRowContext(ctx, `SELECT id::text,COALESCE(reference_manifest_id::text,''),COALESCE(reference_manifest_hash,''),COALESCE(reference_authorization_revision,0),status FROM subjective_grading_run WHERE tenant_id=$1 AND request_id=$2 AND deleted_at IS NULL FOR UPDATE`, tenant, requestID).Scan(&p.RunID, &manifest, &hash, &revision, &status)
	if e != nil {
		return p, e
	}
	if manifest == "" || status != "processing" {
		return p, &GradingAgentError{Code: "material_execution_unavailable"}
	}
	var active bool
	e = tx.QueryRowContext(ctx, `SELECT use_state='active' AND authorization_revision=$3 AND content_hash=$4 FROM question_reference_manifest WHERE tenant_id=$1 AND id=$2 FOR SHARE`, tenant, manifest, revision, hash).Scan(&active)
	if e != nil || !active {
		return p, &GradingAgentError{Code: "material_authorization_changed"}
	}
	var consumed sql.NullTime
	var oldDigest, oldWrapper string
	var sameMedia, expired bool
	e = tx.QueryRowContext(ctx, `SELECT id::text,execution_attempt_id::text,consumed_at,request_digest,wrapper_hash,ordered_media_json=$3::jsonb,expires_at<=now() FROM subjective_request_material_receipt WHERE tenant_id=$1 AND run_id=$2 AND stage='prepared'`, tenant, p.RunID, raw).Scan(&p.PermitID, &p.ExecutionAttemptID, &consumed, &oldDigest, &oldWrapper, &sameMedia, &expired)
	if e == nil {
		if consumed.Valid || expired {
			return p, &GradingAgentError{Code: "material_dispatch_unknown"}
		}
		if oldDigest != digest || oldWrapper != wrapper || !sameMedia {
			return p, &GradingAgentError{Code: "material_execution_conflict"}
		}
		p.Credential = s.credential(tenant, p.RunID, p.PermitID)
		return p, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return p, e
	}
	p.PermitID = uuid.NewString()
	p.ExecutionAttemptID = uuid.NewString()
	p.Credential = s.credential(tenant, p.RunID, p.PermitID)
	_, e = tx.ExecContext(ctx, `INSERT INTO subjective_request_material_receipt(id,tenant_id,run_id,execution_attempt_id,stage,request_id,request_digest,manifest_id,manifest_hash,authorization_revision,wrapper_hash,ordered_media_json,credential_hash,expires_at) VALUES($1,$2,$3,$4,'prepared',$5,$6,$7,$8,$9,$10,$11,$12,now()+interval '10 minutes')`, p.PermitID, tenant, p.RunID, p.ExecutionAttemptID, requestID, digest, manifest, hash, revision, wrapper, raw, materialCredentialHash(p.Credential))
	if e != nil {
		return p, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE subjective_grading_run SET math_wrapper_hash=$3 WHERE tenant_id=$1 AND id=$2`, tenant, p.RunID, wrapper)
	if e != nil {
		return p, e
	}
	return p, tx.Commit()
}

// Callback 用只授权这一执行尝试的高熵凭据定位服务器已有的租户/run；
// 不信任正文中的租户或任意任务 ID，也不授予通用 worker 权限。
func (s *MaterialReceiptStore) Callback(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		httpx.Error(w, r, 503, "material_dispatch_unavailable", "材料发送记录服务未配置")
		return
	}
	runID, permitID := r.PathValue("runId"), r.PathValue("permitId")
	authorization := r.Header.Get("Authorization")
	token := strings.TrimPrefix(authorization, "Bearer ")
	parts := strings.SplitN(token, ":", 2)
	tenant := ""
	if len(parts) == 2 {
		tenant = parts[0]
	}
	_, tenantErr := uuid.Parse(tenant)
	if !strings.HasPrefix(authorization, "Bearer ") || tenantErr != nil || len(token) != 101 || subtle.ConstantTimeCompare([]byte(s.credential(tenant, runID, permitID)), []byte(token)) != 1 {
		httpx.Error(w, r, 401, "material_permit_invalid", "调用许可无效")
		return
	}
	var input struct {
		Stage              string           `json:"stage"`
		RequestBindingHash string           `json:"request_binding_hash"`
		WirePayloadHash    string           `json:"wire_payload_hash"`
		Usage              map[string]int64 `json:"usage"`
		UsageState         string           `json:"usage_state"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || ensureJSONEOF(decoder) != nil || !validLowerSHA256(input.RequestBindingHash) || !validLowerSHA256(input.WirePayloadHash) || !(input.Stage == "consume" || input.Stage == "provider_request_attempted" || input.Stage == "provider_response_received" || input.Stage == "unknown_after_attempt" || input.Stage == "failed_before_send") {
		httpx.Error(w, r, 400, "material_receipt_invalid", "材料发送记录无效")
		return
	}
	ctx := r.Context()
	// 凭据签名绑定租户、run 和许可；签名通过后才绑定数据库租户，不能由任意 tenant 字段扩大范围。
	ctx = database.WithTenant(ctx, tenant)
	if input.Stage == "consume" {
		var wrapper string
		if s.admit == nil || s.db.QueryRowContext(ctx, `SELECT wrapper_hash FROM subjective_request_material_receipt WHERE tenant_id=$1 AND id::text=$2 AND run_id::text=$3 AND stage='prepared'`, tenant, permitID, runID).Scan(&wrapper) != nil || s.admit(ctx, tenant, runID, wrapper) != nil {
			httpx.Error(w, r, 403, "reference_material_image_not_authorized", "当前部署或图像外发尚未获准")
			return
		}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		httpx.Error(w, r, 503, "material_dispatch_unavailable", "材料发送记录暂不可用")
		return
	}
	defer tx.Rollback()
	var digest string
	var wireHash sql.NullString
	var consumed sql.NullTime
	e = tx.QueryRowContext(ctx, `SELECT p.request_digest,p.consumed_at,p.wire_payload_hash FROM subjective_request_material_receipt p WHERE p.id::text=$1 AND p.run_id::text=$2 AND p.stage='prepared' AND p.credential_hash=$3 AND p.tenant_id=$4`, permitID, runID, materialCredentialHash(token), tenant).Scan(&digest, &consumed, &wireHash)
	if e != nil || digest != input.RequestBindingHash {
		httpx.Error(w, r, 409, "material_permit_invalid", "调用许可与请求不匹配")
		return
	}
	if input.Stage == "consume" {
		// 固定 run→材料授权→资产锁的顺序；领取后发生撤回属于在途，只阻止最终结算。
		var status string
		e = tx.QueryRowContext(ctx, `SELECT status FROM subjective_grading_run WHERE tenant_id=$1 AND id::text=$2 FOR UPDATE`, tenant, runID).Scan(&status)
		if e == nil && status == "processing" {
			var materialID string
			e = tx.QueryRowContext(ctx, `SELECT m.id::text FROM subjective_request_material_receipt p JOIN question_reference_manifest m ON m.tenant_id=p.tenant_id AND m.id=p.manifest_id WHERE p.tenant_id=$1 AND p.id::text=$2 AND p.stage='prepared' AND m.use_state='active' AND m.authorization_revision=p.authorization_revision AND m.content_hash=p.manifest_hash FOR SHARE OF m`, tenant, permitID).Scan(&materialID)
			if e == nil {
				// 文件隔离同样参与领取线性化点；固定 ID 顺序避免两份清单交叉锁文件。
				rows, readErr := tx.QueryContext(ctx, `SELECT f.lifecycle_status,f.deleted_at IS NOT NULL FROM file_asset f WHERE f.tenant_id=$1 AND f.id IN (SELECT unnest(ARRAY[i.source_file_asset_id,i.page_file_asset_id,i.derived_file_asset_id]) FROM question_reference_manifest_item i WHERE i.tenant_id=$1 AND i.manifest_id=$2) ORDER BY f.id FOR SHARE`, tenant, materialID)
				if readErr != nil {
					e = readErr
				} else {
					for rows.Next() {
						var state string
						var deleted bool
						if scanErr := rows.Scan(&state, &deleted); scanErr != nil {
							e = scanErr
							break
						}
						if state != "active" || deleted {
							e = ErrIdempotencyConflict
							break
						}
					}
					if e == nil {
						e = rows.Err()
					}
					rows.Close()
				}
			}
			var result sql.Result
			if e == nil {
				// 实际线上请求摘要与消费时刻一起冻结；后续阶段不能换成另一份请求。
				result, e = tx.ExecContext(ctx, `UPDATE subjective_request_material_receipt p SET consumed_at=now(),wire_payload_hash=$3 FROM question_reference_manifest m WHERE p.tenant_id=$1 AND p.id::text=$2 AND p.stage='prepared' AND p.consumed_at IS NULL AND p.expires_at>now() AND m.tenant_id=p.tenant_id AND m.id=p.manifest_id AND m.use_state='active' AND m.authorization_revision=p.authorization_revision AND m.content_hash=p.manifest_hash AND NOT EXISTS(SELECT 1 FROM question_reference_manifest_item i JOIN file_asset f ON f.tenant_id=i.tenant_id AND f.id IN (i.source_file_asset_id,i.page_file_asset_id,i.derived_file_asset_id) WHERE i.tenant_id=p.tenant_id AND i.manifest_id=p.manifest_id AND (f.lifecycle_status<>'active' OR f.deleted_at IS NOT NULL))`, tenant, permitID, input.WirePayloadHash)
			}
			if e == nil {
				n, _ := result.RowsAffected()
				if n != 1 {
					e = ErrIdempotencyConflict
				}
			}
		} else if e == nil {
			e = ErrIdempotencyConflict
		}
	} else {
		if !consumed.Valid || !wireHash.Valid || wireHash.String != input.WirePayloadHash {
			e = ErrIdempotencyConflict
		} else {
			usage := map[string]int64{}
			for _, key := range []string{"input_tokens", "cached_input_tokens", "output_tokens", "reasoning_tokens", "total_tokens"} {
				if value, ok := input.Usage[key]; ok && value >= 0 {
					usage[key] = value
				}
			}
			if input.UsageState != "reported" && input.UsageState != "partial" {
				input.UsageState = "unavailable"
			}
			raw, _ := json.Marshal(usage)
			// 同一阶段只能重放相同的已登记字段；唯一键冲突不代表新用量也已入账。
			var result sql.Result
			result, e = tx.ExecContext(ctx, `INSERT INTO subjective_request_material_receipt(tenant_id,run_id,execution_attempt_id,stage,request_id,request_digest,manifest_id,manifest_hash,authorization_revision,wrapper_hash,ordered_media_json,credential_hash,expires_at,consumed_at,wire_payload_hash,usage_json,usage_state) SELECT tenant_id,run_id,execution_attempt_id,$3,request_id,request_digest,manifest_id,manifest_hash,authorization_revision,wrapper_hash,ordered_media_json,credential_hash,expires_at,consumed_at,wire_payload_hash,$4,$5 FROM subjective_request_material_receipt WHERE tenant_id=$1 AND id::text=$2 AND stage='prepared' ON CONFLICT(tenant_id,run_id,execution_attempt_id,provider_attempt_no,stage) DO NOTHING`, tenant, permitID, input.Stage, raw, input.UsageState)
			if e == nil {
				var n int64
				n, e = result.RowsAffected()
				if e == nil && n == 0 {
					var identical bool
					e = tx.QueryRowContext(ctx, `SELECT wire_payload_hash=$4 AND usage_json=$5::jsonb AND usage_state=$6 FROM subjective_request_material_receipt WHERE tenant_id=$1 AND run_id::text=$2 AND execution_attempt_id=(SELECT execution_attempt_id FROM subjective_request_material_receipt WHERE tenant_id=$1 AND id::text=$3) AND provider_attempt_no=1 AND stage=$7`, tenant, runID, permitID, input.WirePayloadHash, raw, input.UsageState, input.Stage).Scan(&identical)
					if e == nil && !identical {
						e = ErrIdempotencyConflict
					}
				}
			}
		}
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		httpx.Error(w, r, 409, "material_dispatch_unknown", "调用许可已消费或状态已变化，请核对原记录")
		return
	}
	httpx.JSON(w, 200, map[string]any{"accepted": true})
}

// MaterialReceiptSummary 不暴露凭据、对象路径或供应商原文。
type MaterialReceiptSummary struct {
	Stage        string    `json:"stage"`
	ManifestHash string    `json:"manifest_hash"`
	ImageCount   int       `json:"image_count"`
	Consumed     bool      `json:"permit_consumed"`
	RecordedAt   time.Time `json:"recorded_at"`
}
