package questionbank

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"net/http"
	"slices"
)

func (h *Handler) AssignHandoff(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	var in HandoffInput
	if !decodeBody(w, r, &in) {
		return
	}
	actor, ok := auth.UserFromContext(r.Context())
	if !ok || !slices.Contains(actor.Permissions, "question_bank:read") {
		writeError(w, r, auth.ErrForbidden)
		return
	}
	// 全局管理职责与题库管理ACL必须同时成立；仅有编辑职责时只能调整自己发起的交接。
	in.Manage = slices.Contains(actor.Permissions, "question_bank:manage")
	in.AuthorEdit = slices.Contains(actor.Permissions, "question_bank:edit")
	v, err := h.store.AssignHandoff(r.Context(), scope, r.PathValue("versionId"), in)
	respond(w, r, 200, "version", v, err)
}
func (h *Handler) ClaimHandoff(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, ok := scopeFor(w, r)
		if !ok {
			return
		}
		actor, authenticated := auth.UserFromContext(r.Context())
		if !authenticated || !slices.Contains(actor.Permissions, "question_bank:read") {
			writeError(w, r, auth.ErrForbidden)
			return
		}
		var in HandoffInput
		if !decodeBody(w, r, &in) {
			return
		}
		v, err := h.store.ClaimHandoff(r.Context(), scope, r.PathValue("versionId"), action, in)
		respond(w, r, 200, "version", v, err)
	}
}
func (h *Handler) ListHandoffEvents(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeFor(w, r)
	if !ok {
		return
	}
	events, err := h.store.ListHandoffEvents(r.Context(), scope, r.PathValue("versionId"))
	respond(w, r, 200, "events", events, err)
}
