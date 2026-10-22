package paper

import (
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

// WithTemplateLibraryExams 为内存仓储提供与正式考试相同的学校归属来源。
// PostgreSQL 直接在领域事务内读取考试行，不使用请求体中的学校 ID。
func (h *Handler) WithTemplateLibraryExams(exams QuestionRubricExamReader) *Handler {
	if memory, ok := h.templates.(*MemoryStore); ok {
		memory.libraryExams = exams
	}
	return h
}

func (h *Handler) WithTemplateLibraryVisualMatcher(matcher TemplateLibraryVisualMatcher) *Handler {
	h.templateLibraryMatcher = matcher
	return h
}

func (h *Handler) templateLibraryRequest(w http.ResponseWriter, r *http.Request) (TemplateLibraryRepository, auth.AccessScope, bool) {
	store, ok := h.templates.(TemplateLibraryRepository)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "template_library_unavailable", "答题卡模板库暂不可用")
		return nil, auth.AccessScope{}, false
	}
	scope, ok := auth.AccessScopeFromContext(r.Context())
	user := mustUser(r)
	if !ok || scope.TenantID != user.TenantID || scope.ActorID != user.ID {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_forbidden", "模板库超出当前访问范围")
		return nil, scope, false
	}
	return store, scope, true
}

func (h *Handler) ListTemplateLibrary(w http.ResponseWriter, r *http.Request) {
	store, scope, ok := h.templateLibraryRequest(w, r)
	if !ok {
		return
	}
	out, err := store.ListTemplateLibrary(r.Context(), scope, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"libraries": out})
}

func (h *Handler) PublishTemplateToLibrary(w http.ResponseWriter, r *http.Request) {
	store, scope, ok := h.templateLibraryRequest(w, r)
	if !ok {
		return
	}
	var input PublishTemplateToLibraryInput
	if !decodeJSON(w, r, &input) {
		return
	}
	out, err := store.PublishTemplateToLibrary(r.Context(), scope, r.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.template_library_published", "answer_sheet_template_library", out.ID, "confirm blank reference and reusable full answer areas")
	httpx.JSON(w, http.StatusOK, map[string]any{"library": out})
}

func (h *Handler) InstantiateTemplateLibrary(w http.ResponseWriter, r *http.Request) {
	store, scope, ok := h.templateLibraryRequest(w, r)
	if !ok {
		return
	}
	out, err := store.InstantiateTemplateLibrary(r.Context(), scope, r.PathValue("examId"), r.PathValue("libraryId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.template_library_instantiated", "answer_sheet_template", out.Template.ID, "create exam draft with current question mapping")
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) ResolveTemplateLibrary(w http.ResponseWriter, r *http.Request) {
	store, scope, ok := h.templateLibraryRequest(w, r)
	if !ok {
		return
	}
	var input ResolveTemplateLibraryInput
	if !decodeJSON(w, r, &input) {
		return
	}
	out, err := store.ResolveTemplateLibrary(r.Context(), scope, r.PathValue("examId"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if out.Status == "unknown" {
		sources, err := store.TemplateLibraryMatchSources(r.Context(), scope, r.PathValue("examId"), input)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		if !sources.Complete {
			httpx.Error(w, r, http.StatusConflict, "template_library_candidates_incomplete", "适用模板较多，请先人工核对模板库，系统未自动新建模板")
			return
		}
		if len(sources.Candidates) > 0 {
			if h.templateLibraryMatcher == nil {
				httpx.Error(w, r, http.StatusServiceUnavailable, "template_library_matching_unavailable", "模板版面匹配未配置，请先核对已有模板")
				return
			}
			matched, err := h.templateLibraryMatcher.MatchTemplateLibrary(r.Context(), sources)
			if err != nil {
				httpx.Error(w, r, http.StatusServiceUnavailable, "template_library_matching_unavailable", "模板版面匹配暂不可用，请稍后重试")
				return
			}
			if !validLibraryVisualMatch(matched, sources) {
				httpx.Error(w, r, http.StatusBadGateway, "template_library_match_invalid", "模板版面匹配返回了无效结果，请人工核对")
				return
			}
			// 推断期间来源或候选库可能变化；结果必须仍覆盖当前同一输入集合。
			latest, err := store.TemplateLibraryMatchSources(r.Context(), scope, r.PathValue("examId"), input)
			if err != nil {
				writeStoreError(w, r, err)
				return
			}
			if stableContentHash(latest) != stableContentHash(sources) {
				writeStoreError(w, r, ErrConflict)
				return
			}
			out.Status = matched.Status
			if matched.Status != "unknown" {
				libraries, err := store.ListTemplateLibrary(r.Context(), scope, r.PathValue("examId"))
				if err != nil {
					writeStoreError(w, r, err)
					return
				}
				out.Candidates = []AnswerSheetTemplateLibraryVersion{}
				for _, id := range matched.LibraryIDs {
					for _, item := range libraries {
						if item.ID == id {
							out.Candidates = append(out.Candidates, item)
						}
					}
				}
				if len(out.Candidates) != len(matched.LibraryIDs) {
					writeStoreError(w, r, ErrConflict)
					return
				}
				if matched.Status == "matched" {
					instance, err := store.InstantiateTemplateLibrary(r.Context(), scope, r.PathValue("examId"), matched.LibraryIDs[0])
					if err != nil {
						writeStoreError(w, r, err)
						return
					}
					out.Instance = &instance
				}
			}
		}
	}
	httpx.JSON(w, http.StatusOK, out)
}

func validLibraryVisualMatch(result TemplateLibraryVisualMatch, sources TemplateLibraryMatchSources) bool {
	if result.Status != "matched" && result.Status != "unknown" && result.Status != "ambiguous" {
		return false
	}
	// 单一候选也可能只有弱配准证据；不够可信时保留冲突状态，不能当成未找到。
	if result.Status == "matched" && len(result.LibraryIDs) != 1 || result.Status == "unknown" && len(result.LibraryIDs) != 0 || result.Status == "ambiguous" && len(result.LibraryIDs) < 1 {
		return false
	}
	allowed := map[string]bool{}
	for _, item := range sources.Candidates {
		allowed[item.LibraryID] = true
	}
	seen := map[string]bool{}
	for _, id := range result.LibraryIDs {
		if !allowed[id] || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
