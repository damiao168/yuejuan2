package auth

import (
	"context"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

// ExamArchiveResolver 供已有资源权限边界读取归档事实，避免每个业务路由各自解释归档。
// 这里只负责 HTTP 提示；数据库写入约束仍须在事务内处理并发归档和 worker 写回。
type ExamArchiveResolver interface {
	IsExamArchived(ctx context.Context, tenantID, examID string) (bool, error)
}

func (s *PostgresStore) IsExamArchived(ctx context.Context, tenantID, examID string) (bool, error) {
	var archived bool
	err := s.db.QueryRowContext(ctx, `
SELECT e.status = 'archived' OR e.archived_at IS NOT NULL OR es.archived_at IS NOT NULL OR COALESCE(es.status = 'archived', false)
FROM exam e
LEFT JOIN exam_session es ON es.tenant_id=e.tenant_id AND es.id=e.exam_session_id
WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid AND e.deleted_at IS NULL
`, tenantID, examID).Scan(&archived)
	return archived, err
}

func checkExamArchiveBoundary(resolver ResourceBoundaryResolver, w http.ResponseWriter, r *http.Request, scope AccessScope, boundary ResourceBoundary) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || boundary.ExamID == "" {
		return true
	}
	archiveResolver, available := resolver.(ExamArchiveResolver)
	if !available {
		return true
	}
	archived, err := archiveResolver.IsExamArchived(r.Context(), scope.TenantID, boundary.ExamID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "exam_archive_lookup_failed", "暂无法确认考试归档状态，请稍后重试")
		return false
	}
	if archived {
		httpx.Error(w, r, http.StatusConflict, "exam_archived", "考试已归档，当前业务内容只读；请到考试管理查看恢复条件")
		return false
	}
	return true
}
