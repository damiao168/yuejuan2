package questionbank

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type Handler struct{ store Store }

func NewHandler(store Store) *Handler { return &Handler{store: store} }
func scopeFor(w http.ResponseWriter, r *http.Request) (auth.AccessScope, bool) {
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok || !validScope(scope) {
		httpx.Error(w, r, http.StatusForbidden, "question_bank_access_denied", "question bank access is not allowed")
		return scope, false
	}
	return scope, true
}
func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	// 写操作必须有命令 ID，用于重试时返回原结果；纯校验和预览走下方不要求命令 ID 的解码器。
	if commandreceipt.ID(r.Context()) == "" {
		httpx.Error(w, r, http.StatusBadRequest, "idempotency_key_required", "a command identity is required")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "question_bank_invalid_input", "invalid question bank request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "question_bank_invalid_input", "request must contain exactly one JSON value")
		return false
	}
	return true
}
func decodeValidationBody(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "question_bank_invalid_input", "invalid question bank request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "question_bank_invalid_input", "request must contain exactly one JSON value")
		return false
	}
	return true
}
func requestFilter(w http.ResponseWriter, r *http.Request) (Filter, bool) {
	f := Filter{Query: strings.TrimSpace(r.URL.Query().Get("q")), Limit: 50}
	if !validText(f.Query, 128) {
		writeError(w, r, ErrInvalidInput)
		return f, false
	}
	for _, entry := range []struct {
		name   string
		target *int
		max    int
	}{{"limit", &f.Limit, 100}, {"offset", &f.Offset, 1000000}} {
		if raw := r.URL.Query().Get(entry.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 || value > entry.max || (entry.name == "limit" && value == 0) {
				writeError(w, r, ErrInvalidInput)
				return f, false
			}
			*entry.target = value
		}
	}
	return f, true
}
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var metadataErr *MetadataValidationError
	if errors.As(err, &metadataErr) {
		w.Header().Set(httpx.ErrorCodeHeader, "question_bank_metadata_invalid")
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "question_bank_metadata_invalid", "message": "question bank metadata is invalid"}, "field_errors": metadataErr.FieldErrors})
		return
	}
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, 404, "question_bank_resource_not_found", "question bank resource was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, 400, "question_bank_invalid_input", "question bank content or metadata is invalid")
	case errors.Is(err, ErrConflict), errors.Is(err, commandreceipt.ErrConflict):
		httpx.Error(w, r, 409, "question_bank_revision_conflict", "reload the current version before retrying")
	case errors.Is(err, ErrLocked):
		httpx.Error(w, r, 409, "question_bank_content_locked", "question bank content cannot be edited")
	case errors.Is(err, ErrSourceUnavailable):
		httpx.Error(w, r, 409, "question_bank_import_source_unavailable", "a complete frozen readiness and assessment source is required")
	case errors.Is(err, auth.ErrForbidden):
		httpx.Error(w, r, 403, "question_bank_access_denied", "question bank access is not allowed")
	default:
		httpx.Error(w, r, 503, "question_bank_unavailable", "question bank is unavailable")
	}
}

func (h *Handler) GetMetadataSchema(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	version := 0
	if raw := r.URL.Query().Get("version"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, r, ErrInvalidInput)
			return
		}
		version = parsed
	}
	value, err := h.store.GetMetadataSchema(r.Context(), scope, r.PathValue("bankId"), version)
	respond(w, r, 200, "schema", value, err)
}
func (h *Handler) UpdateMetadataSchema(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in UpdateMetadataSchemaInput
	if !decodeBody(w, r, &in) {
		return
	}
	value, err := h.store.UpdateMetadataSchema(r.Context(), scope, r.PathValue("bankId"), in)
	respond(w, r, 200, "schema", value, err)
}
func (h *Handler) ValidateMetadata(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in ValidateMetadataInput
	if !decodeValidationBody(w, r, &in) {
		return
	}
	value, err := h.store.ValidateMetadata(r.Context(), scope, r.PathValue("bankId"), in)
	respond(w, r, 200, "", value, err)
}
func (h *Handler) GetACL(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	value, err := h.store.GetACL(r.Context(), scope, r.PathValue("bankId"))
	respond(w, r, 200, "acl", value, err)
}
func (h *Handler) UpdateACL(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in UpdateACLInput
	if !decodeBody(w, r, &in) {
		return
	}
	value, err := h.store.UpdateACL(r.Context(), scope, r.PathValue("bankId"), in)
	respond(w, r, 200, "acl", value, err)
}

func (h *Handler) SearchItems(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := SearchFilter{Query: strings.TrimSpace(q.Get("q")), BankID: q.Get("bank_id"), SubjectCode: q.Get("subject_code"), KnowledgePoint: q.Get("knowledge_point"), QuestionType: q.Get("question_type"), Archetype: q.Get("archetype"), WorkflowStatus: q.Get("workflow_status"), DifficultyBand: q.Get("difficulty_band"), CognitiveLevel: q.Get("cognitive_level"), Copyright: q.Get("copyright"), IntendedUse: q.Get("intended_use"), UsePolicy: q.Get("use_policy"), MetadataKey: q.Get("metadata_key"), MetadataValue: q.Get("metadata_value"), Mode: q.Get("mode"), Sort: q.Get("sort"), Limit: 50}
	for _, entry := range []struct {
		name   string
		target *int
		max    int
	}{{"limit", &f.Limit, 100}, {"offset", &f.Offset, 1000000}} {
		if raw := q.Get(entry.name); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 0 || v > entry.max || (entry.name == "limit" && v == 0) {
				writeError(w, r, ErrInvalidInput)
				return
			}
			*entry.target = v
		}
	}
	if raw := q.Get("statistics_available"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, r, ErrInvalidInput)
			return
		}
		f.StatisticsAvailable = &v
	}
	value, err := h.store.SearchItems(r.Context(), scope, f)
	respond(w, r, 200, "", value, err)
}
func (h *Handler) RetireItem(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in RetireItemInput
	if !decodeBody(w, r, &in) {
		return
	}
	value, err := h.store.RetireItem(r.Context(), scope, r.PathValue("itemId"), in)
	respond(w, r, 200, "item", value, err)
}
func respond(w http.ResponseWriter, r *http.Request, status int, key string, value any, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	if key != "" {
		value = map[string]any{key: value}
	}
	httpx.JSON(w, status, value)
}
func (h *Handler) ListBanks(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := requestFilter(w, r)
	if !ok {
		return
	}
	page, err := h.store.ListBanks(r.Context(), scope, f)
	respond(w, r, 200, "", page, err)
}
func (h *Handler) CreateBank(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in CreateBankInput
	if !decodeBody(w, r, &in) {
		return
	}
	b, err := h.store.CreateBank(r.Context(), scope, in)
	respond(w, r, 201, "bank", b, err)
}
func (h *Handler) GetBank(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	b, err := h.store.GetBank(r.Context(), scope, r.PathValue("bankId"))
	respond(w, r, 200, "bank", b, err)
}
func (h *Handler) UpdateBank(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in UpdateBankInput
	if !decodeBody(w, r, &in) {
		return
	}
	b, err := h.store.UpdateBank(r.Context(), scope, r.PathValue("bankId"), in)
	respond(w, r, 200, "bank", b, err)
}
func (h *Handler) ListItems(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := requestFilter(w, r)
	if !ok {
		return
	}
	page, err := h.store.ListItems(r.Context(), scope, r.PathValue("bankId"), f)
	respond(w, r, 200, "", page, err)
}
func (h *Handler) CreateItem(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in CreateItemInput
	if !decodeBody(w, r, &in) {
		return
	}
	value, err := h.store.CreateItem(r.Context(), scope, r.PathValue("bankId"), in)
	respond(w, r, 201, "", value, err)
}
func (h *Handler) GetItem(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	i, err := h.store.GetItem(r.Context(), scope, r.PathValue("itemId"))
	respond(w, r, 200, "item", i, err)
}
func (h *Handler) ListVersions(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := requestFilter(w, r)
	if !ok {
		return
	}
	page, err := h.store.ListVersions(r.Context(), scope, r.PathValue("itemId"), f)
	respond(w, r, 200, "", page, err)
}
func (h *Handler) CreateVersion(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in CreateVersionInput
	if !decodeBody(w, r, &in) {
		return
	}
	v, err := h.store.CreateVersion(r.Context(), scope, r.PathValue("itemId"), in)
	respond(w, r, 201, "version", v, err)
}
func (h *Handler) GetVersion(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	v, err := h.store.GetVersion(r.Context(), scope, r.PathValue("versionId"))
	respond(w, r, 200, "version", v, err)
}
func (h *Handler) UpdateVersion(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in UpdateVersionInput
	if !decodeBody(w, r, &in) {
		return
	}
	v, err := h.store.UpdateVersion(r.Context(), scope, r.PathValue("versionId"), in)
	respond(w, r, 200, "version", v, err)
}

func (h *Handler) UpdateScoring(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in UpdateScoringInput
	if !decodeBody(w, r, &in) {
		return
	}
	v, err := h.store.UpdateScoring(r.Context(), scope, r.PathValue("versionId"), in)
	respond(w, r, 200, "version", v, err)
}
func (h *Handler) Transition(decision string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, ok := scopeFor(w, r)
		if !ok {
			return
		}
		var in ReviewInput
		if !decodeBody(w, r, &in) {
			return
		}
		v, err := h.store.Transition(r.Context(), scope, r.PathValue("versionId"), decision, in)
		respond(w, r, 200, "version", v, err)
	}
}
func (h *Handler) ListReviews(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	reviews, err := h.store.ListReviews(r.Context(), scope, r.PathValue("versionId"))
	respond(w, r, 200, "reviews", reviews, err)
}
func (h *Handler) BindReviewers(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in ReviewerBinding
	if !decodeBody(w, r, &in) {
		return
	}
	b, err := h.store.BindReviewers(r.Context(), scope, r.PathValue("bankId"), in)
	respond(w, r, 200, "bank", b, err)
}
func (h *Handler) Materialize(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in MaterializeInput
	if !decodeBody(w, r, &in) {
		return
	}
	out, err := h.store.Materialize(r.Context(), scope, r.PathValue("examId"), in)
	respond(w, r, 201, "", out, err)
}

func (h *Handler) PreviewImports(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in ImportPreviewInput
	if !decodeValidationBody(w, r, &in) {
		return
	}
	out, err := h.store.PreviewImports(r.Context(), scope, in)
	respond(w, r, 200, "", out, err)
}

func (h *Handler) ImportQuestion(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in ImportQuestionInput
	if !decodeBody(w, r, &in) {
		return
	}
	out, err := h.store.ImportQuestion(r.Context(), scope, r.PathValue("questionId"), in)
	respond(w, r, 201, "", out, err)
}

func (h *Handler) ConfirmImportBatch(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in BatchImportInput
	if !decodeValidationBody(w, r, &in) {
		return
	}
	if len(in.Items) == 0 || len(in.Items) > 50 {
		writeError(w, r, ErrInvalidInput)
		return
	}
	out := BatchImportResult{Items: make([]BatchImportResultItem, 0, len(in.Items))}
	// 每题使用自己的命令 ID 和事务；单题失败不回滚已导入的题，客户端按逐题结果重试。
	for _, requested := range in.Items {
		item := BatchImportResultItem{QuestionID: requested.QuestionID, CommandID: requested.CommandID, Status: "failed"}
		ctx := commandreceipt.WithID(r.Context(), requested.CommandID)
		result, err := h.store.ImportQuestion(ctx, scope, requested.QuestionID, requested.ImportQuestionInput)
		if err != nil {
			item.ErrorCode, item.Retryable = importErrorCode(err)
			if errors.Is(err, commandreceipt.ErrConflict) {
				item.ErrorCode, item.Retryable = "command_payload_conflict", false
			}
		} else {
			item.Status, item.Result = "succeeded", &result
		}
		out.Items = append(out.Items, item)
	}
	httpx.JSON(w, http.StatusMultiStatus, out)
}
func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	f, ok := requestFilter(w, r)
	if !ok {
		return
	}
	f.Kind = "rubric_template"
	page, err := h.store.ListItems(r.Context(), scope, r.URL.Query().Get("bank_id"), f)
	respond(w, r, 200, "", page, err)
}

type CreateTemplateInput struct {
	BankID string `json:"bank_id"`
	CreateItemInput
}

func (h *Handler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in CreateTemplateInput
	if !decodeBody(w, r, &in) {
		return
	}
	in.Kind = "rubric_template"
	out, err := h.store.CreateItem(r.Context(), scope, in.BankID, in.CreateItemInput)
	respond(w, r, 201, "", out, err)
}
