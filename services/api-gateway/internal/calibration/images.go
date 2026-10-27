package calibration

import (
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresourcehttp"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func (h *Handler) GetSampleImage(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	session, err := h.service.GetSession(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	// 先核对会话所有者，再从已冻结清单选版本；Gold ID 不能扩大该会话可读的图片集合。
	if session.GraderID != user.ID && !auth.HasPermission(user, reviewManagePermission) {
		httpx.Error(w, r, http.StatusForbidden, "forbidden", "calibration session belongs to another grader")
		return
	}
	if session.Status == SessionInvalidated || h.images == nil {
		writeHandlerError(w, r, ErrConflict)
		return
	}
	for _, sample := range session.Samples {
		if sample.GoldPaperID != r.PathValue("goldId") {
			continue
		}
		resource, err := h.images.ReadVersionImage(r.Context(), user.TenantID, sample.GoldPaperID, sample.GoldVersion)
		if err != nil {
			writeHandlerError(w, r, ErrConflict)
			return
		}
		binaryresourcehttp.Serve(w, r, resource, h.audit)
		return
	}
	writeHandlerError(w, r, ErrSampleNotInSession)
}
