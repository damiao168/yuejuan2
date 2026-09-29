package mathunderstanding

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type FrozenRubricSource interface {
	LoadFrozenRubric(ctx context.Context, tenantID, segmentID, snapshotID string) (FrozenRubric, error)
}

// ActiveMathCropSource closes the worker-lag window after a crop replacement.
// Production stores implement this; hash data remains private to the preview.
type ActiveMathCropSource interface {
	LoadActiveMathCropHash(ctx context.Context, tenantID, segmentID string) (string, error)
}

func (s *PostgresStore) LoadActiveMathCropHash(ctx context.Context, tenantID, segmentID string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(crop_sha256,'') FROM answer_segment
WHERE tenant_id=$1::uuid AND id::text=$2 AND deleted_at IS NULL`, tenantID, segmentID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return hash, err
}

func (h *Handler) WithActiveMathCrops(source ActiveMathCropSource) *Handler {
	h.crops = source
	return h
}

func (h *Handler) checkPreviewCrop(ctx context.Context, tenantID string, item Artifact) error {
	if h.crops == nil { // Legacy in-memory fixtures do not carry physical crops.
		return nil
	}
	hash, err := h.crops.LoadActiveMathCropHash(ctx, tenantID, item.AnswerSegmentID)
	if err != nil {
		return err
	}
	normalize := func(value string) string {
		return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
	}
	input, active := normalize(item.InputHash), normalize(hash)
	decoded, decodeErr := hex.DecodeString(active)
	if decodeErr != nil || len(decoded) != 32 || input != active {
		return ErrRevisionConflict
	}
	return nil
}

func (s *PostgresStore) LoadFrozenRubric(ctx context.Context, tenantID, segmentID, snapshotID string) (FrozenRubric, error) {
	var raw []byte
	out := FrozenRubric{}
	err := s.db.QueryRowContext(ctx, `
SELECT snap.id::text,snap.rubric_snapshot_json
FROM answer_segment seg
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
JOIN exam e ON e.tenant_id=q.tenant_id AND e.id=q.exam_id AND e.deleted_at IS NULL
JOIN exam_question_snapshot snap ON snap.tenant_id=q.tenant_id AND snap.exam_id=q.exam_id AND snap.question_id=q.id
WHERE seg.tenant_id=$1::uuid AND seg.id::text=$2 AND snap.id::text=$3
 AND seg.deleted_at IS NULL AND snap.profile_snapshot_json->>'subject_code'='mathematics'`, tenantID, segmentID, snapshotID).Scan(&out.SnapshotID, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return FrozenRubric{}, ErrNotFound
	}
	if err != nil {
		return FrozenRubric{}, err
	}
	if json.Unmarshal(raw, &out.Rubric) != nil {
		return FrozenRubric{}, ErrInvalidInput
	}
	return out, nil
}

func (h *Handler) WithFrozenRubrics(source FrozenRubricSource) *Handler { h.rubrics = source; return h }

// GetRubricScore 返回教师评分预览，不提交成绩。生产环境还会检查当前裁剪图是否与证据来源一致。
func (h *Handler) GetRubricScore(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	item, err := h.artifacts.GetLatestArtifact(r.Context(), user.TenantID, r.PathValue("segmentId"))
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	if !h.canAccess(r, user, item.AnswerSegmentID) {
		httpx.Error(w, r, http.StatusForbidden, "math_evidence_forbidden", "math evidence is limited to assigned review work")
		return
	}
	if err = h.checkPreviewCrop(r.Context(), user.TenantID, item); err != nil {
		writeMathError(w, r, err)
		return
	}
	if h.rubrics == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "math_frozen_rubric_unavailable", "frozen exam rubric is unavailable")
		return
	}
	effective, err := resolveEffectiveContract(r.Context(), h.corrections, user.TenantID, item)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	frozen, err := h.rubrics.LoadFrozenRubric(r.Context(), user.TenantID, item.AnswerSegmentID, item.ExamQuestionSnapshotID)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	score, err := ScoreFrozenRubric(frozen, effective, nil)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	// 计算期间可能又有教师修订或新识别结果，返回前再检查一次。
	// 这不是锁定承诺；将来若保存该建议，保存时仍须核对返回的版本信息。
	latest, err := ResolveEffectiveArtifact(r.Context(), h.artifacts, h.corrections, user.TenantID, item.AnswerSegmentID)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	if latest.BaseArtifact.ID != item.ID || latest.CorrectionRevision != effective.CorrectionRevision {
		writeMathError(w, r, ErrRevisionConflict)
		return
	}
	if err = h.checkPreviewCrop(r.Context(), user.TenantID, latest.BaseArtifact); err != nil {
		writeMathError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, score)
}
