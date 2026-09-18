package paper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	papers          PaperRepository
	imports         PaperImportRepository
	questions       QuestionRepository
	rubrics         RubricRepository
	validation      ConfigurationValidationRepository
	templates       TemplateRepository
	templateBinding TemplateBindingRepository
	readiness       ReadinessRepository
	audit           auth.Store
	documentImport  *DocumentImportService
}

func (h *Handler) WithDocumentImport(service *DocumentImportService) *Handler {
	h.documentImport = service
	return h
}

func (h *Handler) WithDocumentModelResolver(resolve func(context.Context, string) (*DocumentModelConfig, error)) *Handler {
	if h.documentImport != nil {
		h.documentImport.WithModelResolver(resolve)
	}
	return h
}

func (h *Handler) CreatePaperImport(w http.ResponseWriter, r *http.Request) {
	if h.documentImport == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "paper_import_unavailable", "试卷解析服务未配置")
		return
	}
	user := mustUser(r)
	var input CreatePaperImportInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.CommandID = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(normalizePaperImportSourceInputs(input)) == 0 || input.Subject == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_paper_import", "至少需要一份考试资料和学科")
		return
	}
	out, err := h.documentImport.Start(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.import_created", "paper_import_job", out.ID, "parse ordered exam material sources")
	httpx.JSON(w, http.StatusCreated, map[string]any{"import": out})
}

func (h *Handler) AddPaperImportSources(w http.ResponseWriter, r *http.Request) {
	if h.documentImport == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "paper_import_unavailable", "考试资料解析服务未配置")
		return
	}
	user := mustUser(r)
	var input AddPaperImportSourcesInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.CommandID = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(input.Sources) == 0 || input.ExpectedGeneration <= 0 {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_paper_import_sources", "请选择要追加的考试资料")
		return
	}
	out, err := h.documentImport.AddSources(r.Context(), user.TenantID, user.ID, r.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.import_sources_added", "paper_import_job", out.ID, "add ordered import sources and rerun reconciliation")
	httpx.JSON(w, http.StatusAccepted, map[string]any{"import": out})
}

func (h *Handler) ReplacePaperImportSources(w http.ResponseWriter, r *http.Request) {
	if h.documentImport == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "paper_import_unavailable", "考试资料解析服务未配置")
		return
	}
	user := mustUser(r)
	var input ReplacePaperImportSourcesInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.CommandID = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if input.ExpectedGeneration <= 0 {
		httpx.Error(w, r, http.StatusBadRequest, "expected_generation_required", "expected_generation is required")
		return
	}
	out, err := h.documentImport.ReplaceSources(r.Context(), user.TenantID, user.ID, r.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.import_sources_replaced", "paper_import_job", out.ID, "reorder, remove, or reclassify import sources and rerun reconciliation")
	httpx.JSON(w, http.StatusAccepted, map[string]any{"import": out})
}

func (h *Handler) SavePaperImportReview(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input ReviewPaperImportInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ExpectedGeneration <= 0 {
		httpx.Error(w, r, http.StatusBadRequest, "expected_generation_required", "expected_generation is required")
		return
	}
	if len(input.Questions) == 0 {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_paper_import_review", "至少需要一道人工核对题目")
		return
	}
	out, err := h.imports.SavePaperImportReview(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.import_review_saved", "paper_import_job", out.ID, "save human-confirmed import fields")
	httpx.JSON(w, http.StatusOK, map[string]any{"import": out})
}

func (h *Handler) ListPaperImports(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.imports.ListPaperImports(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		httpx.Error(w, r, 500, "paper_import_list_failed", "试卷解析记录加载失败")
		return
	}
	httpx.JSON(w, 200, map[string]any{"imports": out})
}

func (h *Handler) GetPaperImport(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.imports.GetPaperImport(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"import": out})
}

func (h *Handler) ApplyPaperImport(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.imports.ApplyPaperImport(r.Context(), user.TenantID, r.PathValue("id"), user.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.import_applied", "paper_import_job", out.ID, "apply reviewed questions answers solutions and rubrics")
	httpx.JSON(w, 200, map[string]any{"import": out})
}

func (h *Handler) CancelPaperImport(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var out PaperImportJob
	var err error
	expected, parseErr := strconv.ParseInt(r.URL.Query().Get("expected_generation"), 10, 64)
	if parseErr == nil && expected > 0 {
		if versioned, ok := h.imports.(interface {
			CancelPaperImportGeneration(context.Context, string, string, int64) (PaperImportJob, error)
		}); ok {
			out, err = versioned.CancelPaperImportGeneration(r.Context(), user.TenantID, r.PathValue("id"), expected)
		} else {
			err = ErrConflict
		}
	} else {
		err = ErrInvalidInput
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if h.documentImport != nil {
		h.documentImport.Cancel(user.TenantID, out.ID)
	}
	h.auditAction(r, "paper.import_cancelled", "paper_import_job", out.ID, "manually stop paper OCR and AI parsing")
	httpx.JSON(w, http.StatusOK, map[string]any{"import": out})
}

func (h *Handler) RetryPaperImportParse(w http.ResponseWriter, r *http.Request) {
	retryStore, ok := h.imports.(interface {
		RetryPaperImportParseGeneration(context.Context, string, string, int64) (PaperImportJob, error)
	})
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "paper_parse_retry_unavailable", "试卷内容解析重试服务未配置")
		return
	}
	expected, err := strconv.ParseInt(r.URL.Query().Get("expected_generation"), 10, 64)
	if err != nil || expected <= 0 {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	user := mustUser(r)
	out, err := retryStore.RetryPaperImportParseGeneration(r.Context(), user.TenantID, r.PathValue("id"), expected)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.import_parse_retried", "paper_import_job", out.ID, "retry AI parsing with persisted OCR and formula evidence")
	httpx.JSON(w, http.StatusAccepted, map[string]any{"import": out})
}

func (h *Handler) CompletePaperImportDecode(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.imports.(PaperImportRuntime)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "paper_ocr_unavailable", "paper OCR runtime is unavailable")
		return
	}
	user := mustUser(r)
	var input PaperImportDecodeResult
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := runtime.CompletePaperImportDecode(r.Context(), user.TenantID, r.PathValue("id"), input); err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func (h *Handler) CompletePaperImportOCR(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.imports.(PaperImportRuntime)
	if !ok || h.documentImport == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "paper_ocr_unavailable", "paper OCR runtime is unavailable")
		return
	}
	user := mustUser(r)
	var input PaperImportOCRResult
	if !decodeJSON(w, r, &input) {
		return
	}
	job, parseInput, err := h.documentImport.PrepareOCRParse(r.Context(), user.TenantID, r.PathValue("id"), input.Blocks)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if err := runtime.CompletePaperImportOCR(r.Context(), user.TenantID, job.ID, input, parseInput); err != nil {
		writeStoreError(w, r, err)
		return
	}
	formulaQueued := job.Subject == "mathematics"
	h.auditAction(r, "paper.import_ocr_completed", "paper_import_job", job.ID, "complete scanned document OCR and apply the governed subject recognition route")
	httpx.JSON(w, http.StatusAccepted, map[string]any{"import": job, "formula_queued": formulaQueued, "parse_queued": !formulaQueued})
}

func (h *Handler) CompletePaperImportFormula(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.imports.(PaperImportRuntime)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "paper_formula_unavailable", "paper formula runtime is unavailable")
		return
	}
	user := mustUser(r)
	var input PaperImportFormulaResult
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := runtime.CompletePaperImportFormula(r.Context(), user.TenantID, r.PathValue("id"), input); err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.import_formula_completed", "paper_import_job", r.PathValue("id"), "complete governed formula ROI recognition and release paper parsing barrier")
	httpx.JSON(w, http.StatusAccepted, map[string]any{"accepted": true, "parse_queued": true})
}

func (h *Handler) FailPaperImportRuntime(w http.ResponseWriter, r *http.Request) {
	runtime, ok := h.imports.(PaperImportRuntime)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "paper_ocr_unavailable", "paper OCR runtime is unavailable")
		return
	}
	user := mustUser(r)
	var input PaperImportRuntimeFailure
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := runtime.FailPaperImportRuntime(r.Context(), user.TenantID, r.PathValue("id"), input); err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"accepted": true})
}

func NewHandler(store Store, audit auth.Store) *Handler {
	return &Handler{
		papers: store, imports: store, questions: store, rubrics: store,
		validation: store, templates: store, templateBinding: store, readiness: store,
		audit: audit,
	}
}

func (h *Handler) CreatePaper(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	examID := r.PathValue("examId")
	var input CreatePaperInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.FileAssetID == "" && (input.File.OriginalName == "" || input.File.ContentType == "" || input.File.HashSHA256 == "" || input.File.StorageKey == "" || input.File.SizeBytes <= 0) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_paper_file", "file metadata is incomplete")
		return
	}
	out, err := h.papers.CreatePaper(r.Context(), user.TenantID, examID, user.ID, input)
	if err != nil {
		if errors.Is(err, ErrInvalidInput) {
			httpx.Error(w, r, http.StatusBadRequest, "paper_file_scope_mismatch", "file asset does not belong to this exam")
			return
		}
		httpx.Error(w, r, http.StatusInternalServerError, "paper_create_failed", "failed to register paper metadata")
		return
	}
	h.auditAction(r, "paper.created", "exam_paper", out.ID, "register exam paper metadata")
	note := "file metadata registered; binary upload is handled by file upload story"
	if input.FileAssetID != "" {
		note = "uploaded file asset linked to exam paper"
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"paper": out, "note": note})
}

func (h *Handler) ListPapers(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.papers.ListPapers(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "paper_list_failed", "failed to list papers")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"papers": out})
}

func (h *Handler) CreateQuestion(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CreateQuestionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateQuestionInput(input.QuestionNo, input.QuestionType, input.Score); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_question", err.Error())
		return
	}
	out, err := h.questions.CreateQuestion(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.question_created", "question", out.ID, "create question")
	httpx.JSON(w, http.StatusCreated, map[string]any{"question": out})
}

func (h *Handler) ListQuestions(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.questions.ListQuestions(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "question_list_failed", "failed to list questions")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"questions": out})
}

func (h *Handler) UpdateQuestion(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input UpdateQuestionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.QuestionType != nil && !IsValidQuestionType(*input.QuestionType) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_question_type", "unsupported question_type")
		return
	}
	if input.Score != nil && *input.Score <= 0 {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_score", "score must be greater than 0")
		return
	}
	out, err := h.questions.UpdateQuestion(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.question_updated", "question", out.ID, "update question")
	httpx.JSON(w, http.StatusOK, map[string]any{"question": out})
}

func (h *Handler) DeleteQuestion(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	id := r.PathValue("id")
	if err := h.questions.DeleteQuestion(r.Context(), user.TenantID, id); err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.question_deleted", "question", id, "delete question")
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func (h *Handler) CreateRubric(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input RubricInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Status != "" && !IsValidRubricStatus(input.Status) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_rubric_status", "unsupported rubric status")
		return
	}
	out, err := h.rubrics.CreateRubric(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.rubric_created", "question", out.QuestionID, "create rubric version")
	httpx.JSON(w, http.StatusCreated, map[string]any{"rubric": out})
}

func (h *Handler) ValidatePaperConfig(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	result, err := h.validation.ValidateConfig(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "paper_validation_failed", "failed to validate paper config")
		return
	}
	h.auditAction(r, "paper.config_validated", "exam", r.PathValue("examId"), "validate paper config")
	httpx.JSON(w, http.StatusOK, map[string]any{"result": result})
}

func validateQuestionInput(questionNo string, questionType string, score float64) error {
	if questionNo == "" {
		return errors.New("question_no is required")
	}
	if !IsValidQuestionType(questionType) {
		return errors.New("unsupported question_type")
	}
	if score <= 0 {
		return errors.New("score must be greater than 0")
	}
	return nil
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "paper_resource_not_found", "paper resource not found")
	case errors.Is(err, ErrRubricMismatch):
		httpx.Error(w, r, http.StatusBadRequest, "rubric_score_mismatch", "rubric score must equal question score")
	case errors.Is(err, ErrRubricLocked):
		httpx.Error(w, r, http.StatusConflict, "rubric_locked", "locked rubric cannot be modified")
	case errors.Is(err, ErrTemplateLocked):
		httpx.Error(w, r, http.StatusConflict, "template_locked", "locked template cannot be modified; clone a new version")
	case errors.Is(err, ErrTemplateNotLocked):
		httpx.Error(w, r, http.StatusConflict, "template_not_locked", "template must be locked before it can be used for an exam")
	case errors.Is(err, ErrExamFrozen):
		httpx.Error(w, r, http.StatusConflict, "exam_frozen", "exam paper configuration is frozen after readiness confirmation")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "configuration_conflict", "configuration changed; refresh before saving")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_configuration", "configuration references an invalid exam resource")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "paper_operation_failed", "paper operation failed")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "request body must contain one json object")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) { httpx.JSON(w, status, payload) }

func writeConfigurationError(w http.ResponseWriter, r *http.Request, status int, code string, message string) {
	httpx.Error(w, r, status, code, message)
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
		IPAddress:  r.RemoteAddr,
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
}
