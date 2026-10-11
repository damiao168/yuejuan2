package idempotency

import (
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

// CommandIdentity 为事务性命令处理器校验已有键格式，不重放 HTTP 响应缓存；处理器返回持久回执前会重新检查当前私有内容 ACL。
func CommandIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		key := strings.TrimSpace(r.Header.Get(Header))
		if key == "" {
			httpx.Error(w, r, 400, "idempotency_key_required", "this operation requires an Idempotency-Key")
			return
		}
		if !keyPattern.MatchString(key) {
			httpx.Error(w, r, 400, "invalid_idempotency_key", "Idempotency-Key format is invalid")
			return
		}
		next.ServeHTTP(w, r.WithContext(commandreceipt.WithID(r.Context(), key)))
	})
}
