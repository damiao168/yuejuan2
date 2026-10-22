package reviewbookmark

import "net/http"

// RegisterRoutes 单独接入读、写、清理守卫。读取不能进入有响应体缓存的通用幂等链；
// 清理仅要求活跃本人账号，撤销阅卷权后仍可删除自己的个人说明。
func RegisterRoutes(mux *http.ServeMux, h *Handler, readGuard, writeGuard, cleanupGuard func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/me/review-bookmarks", readGuard(h.List))
	mux.Handle("GET /api/v1/me/review-bookmarks/cleanup", cleanupGuard(h.Cleanup))
	// Go ServeMux 会把 by-task/{taskId} 与 {bookmarkId}/context 判为交叉模式。
	// 用同一个两段分派点保留公开 URL，再填入各处理器所需的路径参数。
	mux.Handle("GET /api/v1/me/review-bookmarks/{first}/{second}", readGuard(func(w http.ResponseWriter, r *http.Request) {
		first, second := r.PathValue("first"), r.PathValue("second")
		if first == "by-task" {
			r.SetPathValue("taskId", second)
			h.GetState(w, r)
			return
		}
		r.SetPathValue("bookmarkId", first)
		switch second {
		case "context":
			h.GetContext(w, r)
		case "image":
			h.GetImage(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	mux.Handle("PUT /api/v1/me/review-bookmarks/by-task/{taskId}", writeGuard(h.Put))
	mux.Handle("DELETE /api/v1/me/review-bookmarks/{bookmarkId}", cleanupGuard(h.Delete))
	// Go 的 GET 路由同时覆盖 HEAD；读守卫不能为 HEAD 要求命令键。
}
