package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func (s *PostgresOrganizationSummaryStore) DashboardReviewTeacherProgress(ctx context.Context, tenantID string, scope auth.AccessScope, now time.Time) ([]ReviewTeacherProgress, error) {
	if s == nil || s.db == nil {
		return nil, sql.ErrConnDone
	}
	if tenantID == "" || scope.TenantID != tenantID || !scope.IsSchoolWide() || scope.TenantWide || scope.IsPlatform || len(scope.ManagedSchoolIDs()) == 0 {
		return nil, auth.ErrForbidden
	}
	// 先按真实学校归属和有效角色选教师，不沿历史班级绑定查询；兼任任何管理员角色的人不计入。
	// 小题任务独立聚合，避免多角色或多个登录会话把同一任务重复计数。考试范围与工作台 ActiveExams 一致：
	// 学生端发布不改 exam.status，所以还要排除当前版本已正式发布的考试，条件与 exam.PublishedExamIDs 相同。
	rows, err := s.db.QueryContext(ctx, `WITH teachers AS (
  SELECT u.id,u.tenant_id,u.school_id,u.username,u.display_name,u.teaching_subjects,u.security_epoch
  FROM app_user u
  WHERE u.tenant_id=$1::uuid AND u.school_id::text=ANY(string_to_array($2,','))
    AND u.status='active' AND u.deleted_at IS NULL
    AND EXISTS (SELECT 1 FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
      WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL AND r.code IN ('teacher','grader','arbitrator'))
    AND NOT EXISTS (SELECT 1 FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
      WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL AND r.code IN ('platform_admin','tenant_admin','school_admin'))
), progress AS (
  SELECT t.id,
    count(*) FILTER (WHERE task.status IN ('pending','assigned')) AS assigned_count,
    count(*) FILTER (WHERE task.status='in_progress') AS in_progress_count,
    count(*) FILTER (WHERE task.status='returned') AS returned_count,
    count(*) FILTER (WHERE task.status IN ('submitted','completed')) AS submitted_count
  FROM teachers t
  JOIN review_task task ON task.tenant_id=t.tenant_id AND task.assigned_to=t.id AND task.deleted_at IS NULL
  JOIN exam e ON e.tenant_id=task.tenant_id AND e.id=task.exam_id AND e.school_id=t.school_id
    AND e.deleted_at IS NULL AND e.status NOT IN ('published','archived')
    AND NOT EXISTS (SELECT 1 FROM score_release_current sc
      JOIN score_release sr ON sr.tenant_id=sc.tenant_id AND sr.id=sc.release_id AND sr.exam_id=sc.exam_id
      WHERE sc.tenant_id=e.tenant_id AND sc.exam_id=e.id AND sr.status='published' AND sr.published_at IS NOT NULL)
  GROUP BY t.id
), activity AS (
  SELECT t.id,max(session.last_seen_at) AS last_active_at
  FROM teachers t
  JOIN auth_session session ON session.tenant_id=t.tenant_id AND session.user_id=t.id
    AND session.revoked_at IS NULL AND session.expires_at>$3 AND session.locked_at IS NULL
    AND session.security_epoch=t.security_epoch AND session.last_seen_at<=$3
    AND session.created_at<=$3
  GROUP BY t.id
)
SELECT t.id::text,t.username,COALESCE(NULLIF(t.display_name,''),t.username),COALESCE(to_json(t.teaching_subjects)::text,'[]'),
  COALESCE(p.assigned_count,0),COALESCE(p.in_progress_count,0),COALESCE(p.returned_count,0),COALESCE(p.submitted_count,0),
  a.last_active_at
FROM teachers t LEFT JOIN progress p ON p.id=t.id LEFT JOIN activity a ON a.id=t.id
ORDER BY t.display_name,t.username,t.id`, tenantID, strings.Join(scope.ManagedSchoolIDs(), ","), now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ReviewTeacherProgress{}
	for rows.Next() {
		var teacher ReviewTeacherProgress
		var subjectsJSON string
		var lastActive sql.NullTime
		if err := rows.Scan(&teacher.UserID, &teacher.Username, &teacher.DisplayName, &subjectsJSON, &teacher.AssignedCount, &teacher.InProgressCount, &teacher.ReturnedCount, &teacher.SubmittedCount, &lastActive); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(subjectsJSON), &teacher.TeachingSubjects); err != nil {
			return nil, err
		}
		if teacher.TeachingSubjects == nil {
			teacher.TeachingSubjects = []string{}
		}
		teacher.RemainingCount = teacher.AssignedCount + teacher.InProgressCount + teacher.ReturnedCount
		// 在线只依据仍有效的登录活动；过期、退出、锁定或安全版本失效的会话不会留下在线假象。
		if lastActive.Valid {
			value := lastActive.Time.UTC()
			teacher.LastActiveAt = &value
			teacher.IsOnline = !value.Before(now.UTC().Add(-reviewTeacherOnlineWindowSeconds * time.Second))
		}
		result = append(result, teacher)
	}
	return result, rows.Err()
}
