package workerruntime

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

const (
	TaskIDHeader           = "X-EduGrade-Worker-Task-ID"
	TaskLeaseHeader        = "X-EduGrade-Worker-Lease-Token"
	WorkerServiceHeader    = "X-EduGrade-Worker-Service"
	WorkerInstanceIDHeader = "X-EduGrade-Worker-Instance-ID"
)

type taskCapabilityContextKey struct{}

func WithTaskCapability(ctx context.Context, task Task) context.Context {
	return context.WithValue(ctx, taskCapabilityContextKey{}, task)
}

func TaskCapabilityFromContext(ctx context.Context) (Task, bool) {
	task, ok := ctx.Value(taskCapabilityContextKey{}).(Task)
	return task, ok
}

// RequireTaskSource 将服务请求路径资源绑定到持久任务来源；人工调用仍执行原有 RBAC 和 AccessScope 检查。
func RequireTaskSource(sourceType string, pathParam string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok || !auth.IsServiceUser(user) {
				next.ServeHTTP(w, r)
				return
			}
			task, ok := TaskCapabilityFromContext(r.Context())
			targetID := strings.TrimSpace(r.PathValue(pathParam))
			if !ok || targetID == "" || task.SourceType != sourceType || task.SourceID != targetID {
				httpx.Error(w, r, http.StatusForbidden, "worker_scope_forbidden", "worker task capability does not authorize the requested resource")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireTaskPayloadValue 将路径资源绑定到持久任务 payload 中的明确 ID，适用于答题段等依赖资源。
func RequireTaskPayloadValue(pathParam string, payloadKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok || !auth.IsServiceUser(user) {
				next.ServeHTTP(w, r)
				return
			}
			task, ok := TaskCapabilityFromContext(r.Context())
			targetID := strings.TrimSpace(r.PathValue(pathParam))
			if !ok || targetID == "" || !payloadContains(task.Payload, payloadKey, targetID) {
				httpx.Error(w, r, http.StatusForbidden, "worker_scope_forbidden", "worker task capability does not authorize the requested resource")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireTaskFile 只允许下载持久任务 payload 明确记录的文件 ID；调用方提供的租户头不参与判断。
func RequireTaskFile(pathParam string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok || !auth.IsServiceUser(user) {
				next.ServeHTTP(w, r)
				return
			}
			task, ok := TaskCapabilityFromContext(r.Context())
			targetID := strings.TrimSpace(r.PathValue(pathParam))
			if !ok || targetID == "" || !contains(taskAccessScope(task).FileIDs, targetID) {
				httpx.Error(w, r, http.StatusForbidden, "worker_scope_forbidden", "worker task capability does not authorize the requested file")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TaskScope 从当前租约任务推导目标租户和资源范围；服务账号不能通过请求头选择租户。
func TaskScope(store Store) func(http.Handler) http.Handler {
	return taskScope(store, false)
}

// TaskCompletionScope 仅安装在完成回调。成功任务只允许原 worker 的原租约进入严格结果回放。
// 文件访问、Execute、Fail 和 Heartbeat 仍使用 TaskScope，不因历史成功租约恢复操作权限。
func TaskCompletionScope(store Store) func(http.Handler) http.Handler {
	return taskScope(store, true)
}

func taskScope(store Store, completion bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok {
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
				return
			}
			if !auth.IsServiceUser(user) {
				next.ServeHTTP(w, r)
				return
			}

			task, ok := authorizeTaskRequestForCompletion(w, r, store, completion)
			if !ok {
				return
			}
			if !workerIdentityAllowsTask(user, task) {
				httpx.Error(w, r, http.StatusForbidden, "worker_scope_forbidden", "authenticated worker service does not own this task queue")
				return
			}

			scope := taskAccessScope(task)
			if user.TenantID != task.TenantID {
				// 目标租户来自持久任务；原租户的用户 ID 和代码不能进入目标租户上下文。
				user.ID = ""
				user.TenantCode = ""
			}
			user.TenantID = task.TenantID
			ctx := auth.WithAccessScope(auth.WithUser(r.Context(), user), scope)
			ctx = WithTaskCapability(ctx, task)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// 先核对当前租约；仅完成回调允许检查同一工作节点的持久完成回执。
func authorizeTaskRequestForCompletion(w http.ResponseWriter, r *http.Request, store Store, completion bool) (Task, bool) {
	taskID := strings.TrimSpace(r.Header.Get(TaskIDHeader))
	leaseToken := strings.TrimSpace(r.Header.Get(TaskLeaseHeader))
	workerService := strings.TrimSpace(r.Header.Get(WorkerServiceHeader))
	workerInstanceID := strings.TrimSpace(r.Header.Get(WorkerInstanceIDHeader))
	if taskID == "" || leaseToken == "" || workerService == "" || workerInstanceID == "" {
		httpx.Error(w, r, http.StatusForbidden, "worker_scope_forbidden", "a current worker task capability is required")
		return Task{}, false
	}
	if pathTaskID := strings.TrimSpace(r.PathValue("taskId")); pathTaskID != "" && pathTaskID != taskID {
		httpx.Error(w, r, http.StatusForbidden, "worker_scope_forbidden", "worker task capability does not match the requested task")
		return Task{}, false
	}
	task, err := store.AuthorizeLease(r.Context(), taskID, leaseToken, workerService, workerInstanceID, time.Now().UTC())
	if err != nil && completion {
		if replayStore, ok := store.(interface {
			AuthorizeCompletedLease(context.Context, string, string, string, string) (Task, error)
		}); ok {
			task, err = replayStore.AuthorizeCompletedLease(r.Context(), taskID, leaseToken, workerService, workerInstanceID)
		}
	}
	if err != nil {
		httpx.Error(w, r, http.StatusForbidden, "worker_scope_forbidden", "worker task capability is invalid or expired")
		return Task{}, false
	}
	return task, true
}

func taskAccessScope(task Task) auth.AccessScope {
	scope := auth.AccessScope{TenantID: task.TenantID, AssignedOnly: true}
	collectTaskScope(&scope, task.Payload)
	collectTaskScopeValue(&scope, task.SourceType+"_id", task.SourceID)
	return scope
}

func collectTaskScope(scope *auth.AccessScope, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			collectTaskScopeValue(scope, strings.ToLower(strings.TrimSpace(key)), nested)
			collectTaskScope(scope, nested)
		}
	case []any:
		for _, nested := range typed {
			collectTaskScope(scope, nested)
		}
	}
}

func collectTaskScopeValue(scope *auth.AccessScope, key string, raw any) {
	value, ok := raw.(string)
	if !ok || strings.TrimSpace(value) == "" {
		return
	}
	value = strings.TrimSpace(value)
	switch {
	case key == "school_id":
		scope.SchoolIDs = append(scope.SchoolIDs, value)
	case key == "grade_id":
		scope.GradeIDs = append(scope.GradeIDs, value)
	case key == "class_id":
		scope.ClassIDs = append(scope.ClassIDs, value)
	case key == "exam_id":
		scope.ExamIDs = append(scope.ExamIDs, value)
	case key == "submission_id":
		scope.SubmissionIDs = append(scope.SubmissionIDs, value)
	case key == "file_asset_id" || strings.HasSuffix(key, "_file_asset_id"):
		scope.FileIDs = append(scope.FileIDs, value)
	case strings.HasSuffix(key, "download_url"):
		if parsed, err := url.Parse(value); err == nil {
			parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			for index, part := range parts {
				if part == "files" && index+1 < len(parts) {
					scope.FileIDs = append(scope.FileIDs, parts[index+1])
				}
			}
		}
	}
}

func payloadContains(value any, key string, expected string) bool {
	switch typed := value.(type) {
	case map[string]any:
		return payloadMapContains(typed, key, expected)
	case []any:
		for _, nested := range typed {
			if payloadContains(nested, key, expected) {
				return true
			}
		}
	}
	return false
}

func payloadMapContains(value map[string]any, key, expected string) bool {
	// 资源值只接受去空格后的字符串精确匹配；非字符串仍可作为嵌套 payload 继续查找。
	for itemKey, nested := range value {
		if strings.EqualFold(strings.TrimSpace(itemKey), key) {
			if text, ok := nested.(string); ok && strings.TrimSpace(text) == expected {
				return true
			}
		}
		if payloadContains(nested, key, expected) {
			return true
		}
	}
	return false
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
