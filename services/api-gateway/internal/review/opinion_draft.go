package review

import (
	"edugrade-enterprise/services/api-gateway/internal/opiniondraft"
	"net/http"
	"slices"
)

func (h *Handler) WithPrivateDrafts(store opiniondraft.Store) *Handler {
	h.privateDrafts = store
	return h
}
func (h *Handler) PrivateOpinionDraft(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	task, err := h.store.GetArbitrationTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	// 仲裁意见是本人受派任务的私稿；管理者读取队列不代表可以代写或恢复他人的裁决。
	editable := slices.Contains(user.Permissions, "arbitration:work") && task.AssignedTo == user.ID && task.Status != "submitted"
	opiniondraft.Endpoint(w, r, h.privateDrafts, "arbitration", task.ExamID+":"+task.QuestionID, task.Revision, editable)
}
