package grading

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	filespkg "edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"errors"
	"net/http"
)

const (
	omrCalibrationStoreRequired = "OMR calibration requires the production grading store"
	scoringRecoveryUnavailable  = "scoring recovery is unavailable"
)

type Handler struct {
	store          Store
	engine         *Engine
	audit          auth.AuditRecorder
	runtime        workerruntime.Store
	files          filespkg.Store
	gradingAIProbe func(context.Context, string, GradingAIModel, string, bool) (GradingVisionProbe, error)
}

func NewHandler(store Store, engine *Engine, audit auth.AuditRecorder) *Handler {
	return &Handler{store: store, engine: engine, audit: audit}
}

func (h *Handler) SetProductionDependencies(runtime workerruntime.Store, files filespkg.Store) {
	h.runtime = runtime
	h.files = files
}

func (h *Handler) RecordAnswer(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input RecordAnswerInput
	if !decodeJSON(w, r, &input) {
		return
	}
	// 这是人工录入入口；来源和置信度是服务端事实，不能由表单伪装成 OCR 或导入结果。
	input.Source, input.Confidence = "manual_entry", nil
	answer, err := h.store.RecordAnswer(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.answer_recorded", "answer_segment", answer.AnswerSegmentID, "record answer for rule grading")
	httpx.JSON(w, http.StatusOK, map[string]any{"answer": answer})
}

func (h *Handler) RuleGrade(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	ctx, err := h.store.LoadContext(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	evaluation, err := h.engine.Grade(ctx)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	// 这里保存规则引擎的建议记录；正式 question_grade 由评分编排中的确认流程写入。
	grade, err := h.store.CreateGrade(r.Context(), user.TenantID, user.ID, evaluation)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.rule_grade_created", "ai_grade", grade.ID, "create rule-based objective grade")
	httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade})
}

func (h *Handler) ListGrades(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	grades, err := h.store.ListGrades(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"grades": grades})
}

func (h *Handler) CreateScoringRule(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CreateScoringRuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	rule, err := h.ruleStore().CreateScoringRule(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.scoring_rule_created", "scoring_rule", rule.ID, "create scoring rule draft")
	httpx.JSON(w, http.StatusCreated, map[string]any{"scoring_rule": rule})
}

func (h *Handler) ListScoringRules(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	rules, err := h.ruleStore().ListScoringRules(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_rules": rules})
}

func (h *Handler) UpdateScoringRule(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input UpdateScoringRuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	rule, err := h.ruleStore().UpdateScoringRule(r.Context(), user.TenantID, r.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.scoring_rule_updated", "scoring_rule", rule.ID, "update scoring rule draft")
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_rule": rule})
}

func (h *Handler) PublishScoringRule(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	rule, err := h.ruleStore().PublishScoringRule(r.Context(), user.TenantID, r.PathValue("id"), user.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.scoring_rule_published", "scoring_rule", rule.ID, "publish immutable scoring rule")
	httpx.JSON(w, http.StatusOK, map[string]any{"scoring_rule": rule})
}

func (h *Handler) ruleStore() RuleStore {
	return h.store.(RuleStore)
}

func (h *Handler) scoringStore() ScoringRunStore { return h.store.(ScoringRunStore) }

func (h *Handler) omrCalibrationStore() (OMRCalibrationStore, bool) {
	store, ok := h.store.(OMRCalibrationStore)
	return store, ok
}

func (h *Handler) scoringRecoveryStore() (ScoringRecoveryStore, bool) {
	store, ok := h.store.(ScoringRecoveryStore)
	return store, ok
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	var readinessErr *ScoringReadinessError
	if errors.As(err, &readinessErr) {
		code, message := "scoring_not_ready", "考试尚未满足评分启动条件"
		for _, check := range readinessErr.Readiness.Checks {
			if !check.Passed && (check.Code == "grading_ai_plan_confirmation_required" || check.Code == "grading_ai_threshold_required" || check.Code == "grading_model_required") {
				code, message = check.Code, check.Message
				break
			}
		}
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"code":    code,
				"message": message,
			},
			"scoring_readiness": readinessErr.Readiness,
		})
		return
	}
	switch {
	case errors.Is(err, ErrGradingAIModelRequired):
		httpx.Error(w, r, http.StatusBadRequest, "grading_model_required", "请学校管理员至少配置一个本校可用的 AI 阅卷模型")
	case errors.Is(err, ErrGradingAIPlanConfirmationRequired):
		httpx.Error(w, r, http.StatusConflict, "grading_ai_plan_confirmation_required", "请学校管理员在阅卷前确认本场大题 AI 评分方案")
	case errors.Is(err, ErrGradingAIThresholdRequired):
		httpx.Error(w, r, http.StatusBadRequest, "grading_ai_threshold_required", "请学校管理员手动填写大题主评分差和教师复核允许分差（单位：分）")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "grading_resource_not_found", "grading resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_grading_input", "grading input is invalid")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_scoring_run_transition", "scoring run operation is not allowed in its current state")
	case errors.Is(err, ErrCommandConflict):
		httpx.Error(w, r, http.StatusConflict, "scoring_command_conflict", "command ID is already associated with another request")
	case errors.Is(err, ErrScoringPlanChanged):
		httpx.Error(w, r, http.StatusConflict, "scoring_plan_changed", "评分计划已变化，请重新检查当前答案、规则和答卷输入")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "grading_action_forbidden", "grading action is not permitted for this user")
	case errors.Is(err, ErrAnswerMissing):
		httpx.Error(w, r, http.StatusConflict, "answer_segment_answer_missing", "answer segment has no recorded answer")
	case errors.Is(err, ErrAnswerKeyMissing):
		httpx.Error(w, r, http.StatusConflict, "question_answer_key_missing", "question has no answer key")
	case errors.Is(err, ErrUnsupportedQuestionType):
		httpx.Error(w, r, http.StatusBadRequest, "unsupported_rule_grading_question_type", "question type is not supported by rule grading")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "scoring_rule_revision_conflict", "scoring rule was changed by another user")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "grading_operation_failed", "grading operation failed")
	}
}

// 路由必须先经过认证和权限中间件；这里仅取上下文身份，不再次认证。
func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func (h *Handler) auditAction(r *http.Request, action string, targetType string, targetID string, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Reason:     reason,
		IPAddress:  httpx.ClientIP(r),
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
}
