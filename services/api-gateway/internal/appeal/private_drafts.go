package appeal

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/opiniondraft"
	"net/http"
)

func (h *Handler) WithPrivateDrafts(store opiniondraft.Store) *Handler {
	h.privateDrafts = store
	return h
}
func (h *PublishedQuestionAppealHandler) WithPrivateDrafts(store opiniondraft.Store) *PublishedQuestionAppealHandler {
	h.privateDrafts = store
	return h
}

func (h *Handler) PrivateDraft(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	item, err := h.store.GetAppeal(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	// 沿用教职工分派范围；学生和已被改派的教师不能借本人旧草稿读取历史敏感内容。
	if !hasPermission(user, appealManagePermission) && !(hasPermission(user, appealWorkPermission) && item.AssignedTo == user.ID) {
		writeStoreError(w, r, ErrForbidden)
		return
	}
	source := item.ExamID + ":" + item.FinalGradeID
	if item.FinalGradeID == "" {
		source = item.ExamID + ":" + item.SubmissionGradeID
	}
	editable := item.Status == "submitted" || item.Status == "under_review" || item.Status == "pending" || item.Status == "need_more_info"
	opiniondraft.Endpoint(w, r, h.privateDrafts, "legacy-appeal", source, item.Revision, editable)
}
func (h *PublishedQuestionAppealHandler) PrivateDraft(w http.ResponseWriter, r *http.Request) {
	item, err := h.authorizedStaffAppeal(r)
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	user := mustUser(r)
	editable := hasPermission(user, appealManagePermission) && (item.Status == "under_review" || item.Status == "upheld_pending_regrade") || hasPermission(user, appealWorkPermission) && item.AssignedTo == user.ID && item.Status == "under_review" && item.ReviewOpinion == nil
	opiniondraft.Endpoint(w, r, h.privateDrafts, "question-appeal", item.SourceReleaseID, item.Revision, editable)
}

func commitPrivateDraft(r *http.Request, store opiniondraft.Store, kind string, revision int64) {
	user, ok := auth.UserFromContext(r.Context())
	if ok && store != nil {
		_ = store.Commit(r.Context(), user.TenantID, user.ID, kind, r.PathValue("id"), revision)
	}
}
