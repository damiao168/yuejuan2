package mathunderstanding

import (
	"context"
	"crypto/sha256"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

const (
	verificationTaskNotFound = "math verification task was not found"
)

type Handler struct {
	artifacts   Store
	corrections CorrectionStore
	pilotGates  PilotGateStore
	reviews     reviewAssignmentStore
	audit       auth.AuditRecorder
	runtime     workerruntime.Store
	rubrics     FrozenRubricSource
	crops       ActiveMathCropSource
}

type reviewAssignmentStore interface {
	HasActiveAssignment(ctx context.Context, tenantID string, reviewerID string, answerSegmentID string) (bool, error)
}

func NewHandler(artifacts Store, corrections CorrectionStore, pilotGates PilotGateStore, reviews reviewAssignmentStore, audit auth.AuditRecorder) *Handler {
	h := &Handler{artifacts: artifacts, corrections: corrections, pilotGates: pilotGates, reviews: reviews, audit: audit}
	if source, ok := artifacts.(FrozenRubricSource); ok {
		h.rubrics = source
	}
	if source, ok := artifacts.(ActiveMathCropSource); ok {
		h.crops = source
	}
	return h
}

func (h *Handler) WithRuntime(runtime workerruntime.Store) *Handler {
	h.runtime = runtime
	return h
}

// RegisterRoutes 接入调用方提供的评阅、管理权限中间件；题块分配权限在各处理器内进一步检查。
func RegisterRoutes(mux *http.ServeMux, handler *Handler, requireWork, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/math-answer-segments/{segmentId}/understanding", requireWork(handler.GetLatest))
	mux.Handle("GET /api/v1/math-answer-segments/{segmentId}/rubric-score", requireWork(handler.GetRubricScore))
	mux.Handle("GET /api/v1/math-understanding/{artifactId}/corrections", requireWork(handler.ListCorrections))
	mux.Handle("POST /api/v1/math-understanding/{artifactId}/corrections", requireWork(handler.CreateCorrection))
	mux.Handle("GET /api/v1/math-understanding/training-export", requireManage(handler.ExportTraining))
	mux.Handle("GET /api/v1/math-pilot-gates", requireManage(handler.ListPilotGates))
	mux.Handle("POST /api/v1/math-pilot-gates/evaluate", requireManage(handler.EvaluatePilotGate))
}

type evaluatePilotGateRequest struct {
	SubjectCode  string       `json:"subject_code"`
	BenchmarkRef string       `json:"benchmark_ref"`
	Metrics      PilotMetrics `json:"metrics"`
	Policy       PilotPolicy  `json:"policy"`
}

func (h *Handler) EvaluatePilotGate(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	var input evaluatePilotGateRequest
	if !decodeMathJSON(w, r, &input) {
		return
	}
	item, err := h.pilotGates.CreatePilotGate(r.Context(), user.TenantID, input.SubjectCode, input.BenchmarkRef, input.Metrics, input.Policy, user.ID)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	h.auditEvent(r, user, "math_understanding.pilot_gate_evaluated", item.ID, map[string]any{"subject_code": item.SubjectCode, "passed": item.Decision.Passed, "scope": item.Decision.Scope})
	httpx.JSON(w, http.StatusCreated, map[string]any{"evaluation": item})
}

func (h *Handler) ListPilotGates(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	subject := r.URL.Query().Get("subject")
	if subject != "" && !set("mathematics", "physics", "chemistry")[subject] {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_subject", "math pilot gates require an enabled subject")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.pilotGates.ListPilotGates(r.Context(), user.TenantID, subject, limit)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"evaluations": items})
}

func (h *Handler) GetLatest(w http.ResponseWriter, r *http.Request) {
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
	effective, err := resolveEffectiveContract(r.Context(), h.corrections, user.TenantID, item)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	corrections, err := h.corrections.ListCorrections(r.Context(), user.TenantID, item.ID)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"artifact":            item,
		"effective_artifact":  effective.EffectiveContract,
		"correction_revision": effective.CorrectionRevision,
		"corrected":           effective.Corrected,
		"corrections":         corrections,
	})
}

func (h *Handler) ListCorrections(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	artifact, err := h.artifacts.GetArtifact(r.Context(), user.TenantID, r.PathValue("artifactId"))
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	if !h.canAccess(r, user, artifact.AnswerSegmentID) {
		httpx.Error(w, r, http.StatusForbidden, "math_evidence_forbidden", "math evidence is limited to assigned review work")
		return
	}
	items, err := h.corrections.ListCorrections(r.Context(), user.TenantID, artifact.ID)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"corrections": items})
}

func (h *Handler) CreateCorrection(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	artifact, err := h.artifacts.GetArtifact(r.Context(), user.TenantID, r.PathValue("artifactId"))
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	if !h.canAccess(r, user, artifact.AnswerSegmentID) {
		httpx.Error(w, r, http.StatusForbidden, "math_correction_forbidden", "only the assigned reviewer or quality manager may correct math evidence")
		return
	}
	var input CreateCorrectionInput
	if !decodeMathJSON(w, r, &input) {
		return
	}
	item, err := h.corrections.CreateCorrection(r.Context(), user.TenantID, artifact.ID, user.ID, input)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	verificationStatus, verificationTaskID := "not_required", ""
	if len(item.CorrectedContract.Formulas) > 0 {
		verificationStatus = "unavailable"
		if h.runtime != nil {
			task, taskErr := h.enqueueVerificationTask(r.Context(), user.ID, artifact, item.Revision)
			verificationStatus = "failed"
			if taskErr == nil {
				verificationStatus, verificationTaskID = "queued", task.ID
			}
		}
	}
	h.auditEvent(r, user, "math_understanding.corrected", artifact.ID, map[string]any{"revision": item.Revision, "operation_count": len(item.Operations)})
	// 保存只追加的更正与安排验证是两种独立结果；队列不存在或失败时，不得报告 "running"。
	response := map[string]any{"correction": item, "verification_status": verificationStatus}
	if verificationTaskID != "" {
		response["verification_task_id"] = verificationTaskID
	}
	httpx.JSON(w, http.StatusCreated, response)
}

func (h *Handler) ExportTraining(w http.ResponseWriter, r *http.Request) {
	user, ok := currentMathUser(w, r)
	if !ok {
		return
	}
	subject := r.URL.Query().Get("subject")
	if !set("mathematics", "physics", "chemistry")[subject] {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_subject", "math training export requires an enabled subject")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.corrections.ExportCorrections(r.Context(), user.TenantID, subject, limit)
	if err != nil {
		writeMathError(w, r, err)
		return
	}
	safe := make([]map[string]any, 0, len(items))
	for _, item := range items {
		contract := cloneInput(item.CorrectedContract)
		contract.AnswerSegmentID = ""
		contract.ExamQuestionSnapshotID = ""
		digest := sha256.Sum256([]byte(user.TenantID + ":" + item.AnswerSegmentID))
		safe = append(safe, map[string]any{"sample_key": hex.EncodeToString(digest[:]), "subject_code": subject, "operations": item.Operations, "corrected_contract": contract, "created_at": item.CreatedAt})
	}
	h.auditEvent(r, user, "math_understanding.training_exported", subject, map[string]any{"count": len(safe)})
	httpx.JSON(w, http.StatusOK, map[string]any{"samples": safe})
}

// canAccess 按精确题块分配判断评阅权限；管理权限可绕过分配，查询失败则关闭访问。
func (h *Handler) canAccess(r *http.Request, user auth.User, segmentID string) bool {
	for _, permission := range user.Permissions {
		if permission == "review:manage" {
			return true
		}
	}
	if h.reviews == nil {
		return false
	}
	allowed, err := h.reviews.HasActiveAssignment(r.Context(), user.TenantID, user.ID, segmentID)
	return err == nil && allowed
}

func (h *Handler) auditEvent(r *http.Request, user auth.User, action, targetID string, after map[string]any) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: "math_understanding", TargetID: targetID, AfterValue: after, Reason: action, IPAddress: httpx.ClientIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}

func currentMathUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return user, ok
}

func decodeMathJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must use the documented math evidence contract")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}

func writeMathError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "math_understanding_not_found", "math understanding artifact was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_math_understanding", "math understanding input is invalid")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "math_understanding_revision_conflict", "math understanding changed; reload before correcting")
	case errors.Is(err, ErrInvalidPilotGate):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_math_pilot_gate", "math pilot gate evidence or policy is invalid")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "math_understanding_failed", "math understanding operation failed")
	}
}
