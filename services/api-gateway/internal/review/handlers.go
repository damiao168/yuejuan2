package review

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/aidisagreement"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/opiniondraft"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"edugrade-enterprise/services/api-gateway/internal/workflowactors"
	"encoding/json"
	"errors"
	"net/http"
)

const (
	invalidCursorMessage   = "cursor is invalid"
	reviewImageUnavailable = "review image handler is unavailable"
)

type GraderQualificationGate interface {
	RequireQualification(context.Context, string, string, string, string) error
}

// SeedObservationRefresher 采用尽力而为方式。
// 即使聚合监控投影暂时不可用，已完成的盲测质量样本仍已持久化。
type SeedObservationRefresher interface {
	RefreshSeedObservation(context.Context, string, string, string, string) error
}

// AIHumanDisagreementObserver 只在人工成绩持久化提交后运行，并采用尽力而为方式：
// 可观测性绝不能回滚教师的有效评分操作，也不能改变当前或最终成绩。
type AIHumanDisagreementObserver interface {
	ObserveHumanGrade(context.Context, string, string, string) (aidisagreement.Disagreement, bool, error)
}

// AIFailureReader 只返回当前任务、答案和冻结量规版本对应的白名单失败类别。
// 实现不得把供应商正文、请求标识或其他租户的运行状态投影到教师工作台。
type AIFailureReader interface {
	CurrentAIFailure(context.Context, string, string, string, string, string, string) (string, error)
}

type Handler struct {
	privateDrafts      opiniondraft.Store
	referenceMaterials *paper.ReferenceMaterialsService
	store              Store
	audit              auth.Store
	segmentImage       segment.CropImageReader
	goldImages         goldpaper.VersionImageReader
	fileDownload       files.DownloadReader
	qualificationGate  GraderQualificationGate
	seedHook           seedquality.ReviewHook
	seedRefresher      SeedObservationRefresher
	disagreement       AIHumanDisagreementObserver
	aiFailure          AIFailureReader
}

func NewHandler(store Store, audit auth.Store, segmentImage segment.CropImageReader, fileDownload files.DownloadReader) *Handler {
	return &Handler{store: store, audit: audit, segmentImage: segmentImage, fileDownload: fileDownload}
}

func (h *Handler) WithQualificationGate(gate GraderQualificationGate) *Handler {
	h.qualificationGate = gate
	return h
}

// WithSeedHook 将暗样本隐藏在普通复核路由之后。
// 钩子只返回合成标识，绝不暴露 Gold 来源。
func (h *Handler) WithSeedHook(hook seedquality.ReviewHook) *Handler {
	h.seedHook = hook
	return h
}

func (h *Handler) WithGoldImageReader(reader goldpaper.VersionImageReader) *Handler {
	h.goldImages = reader
	return h
}

func (h *Handler) WithSeedObservationRefresher(refresher SeedObservationRefresher) *Handler {
	h.seedRefresher = refresher
	return h
}

func (h *Handler) WithAIHumanDisagreementObserver(observer AIHumanDisagreementObserver) *Handler {
	h.disagreement = observer
	return h
}

func (h *Handler) WithAIFailureReader(reader AIFailureReader) *Handler {
	h.aiFailure = reader
	return h
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, commandreceipt.ErrConflict) {
		httpx.Error(w, r, http.StatusConflict, "command_request_conflict", "command ID is associated with a different request")
		return
	}
	switch {
	case errors.Is(err, ErrQualificationRequired):
		httpx.Error(w, r, http.StatusForbidden, "grader_qualification_required", "complete the current question calibration before submitting R3 work")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "review_resource_not_found", "review resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_review_input", "review input is invalid")
	case errors.Is(err, workflowactors.ErrAssigneeIneligible):
		httpx.Error(w, r, http.StatusForbidden, "review_assignee_ineligible", "请选择本校任教该考试学科的有效阅卷教师（学校管理员除外）。")
	case errors.Is(err, ErrForbidden), errors.Is(err, auth.ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "review_action_forbidden", "review action is forbidden")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_review_transition", "review task transition is invalid")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "resource_version_conflict", "resource was updated in another session; refresh and retry")
	case errors.Is(err, ErrTaskMarked):
		httpx.Error(w, r, http.StatusConflict, "review_task_marked", "该答卷已标记，已暂停自动提交；取消标记后可继续")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "review_operation_failed", "review operation failed")
	}
}

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
