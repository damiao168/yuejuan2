package scorerelease

import (
	"context"
	"database/sql"
)

// 主科固定使用详情已读取的发布，兄弟科目在同一条查询中取当前发布。
// 应考名单来自冻结考生范围；不能把同考次中其他同学的科目计入本人的总分。
func (s *PostgresStore) studentSessionSummary(ctx context.Context, tenantID, examID, studentID, releaseID string) (*StudentSessionSummary, float64, float64, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT e.id::text,e.subject,e.name,mine.total_score::float8,mine.max_score::float8,r.published_at,
 COALESCE((r.visibility_policy->>'show_cohort_statistics')::boolean,false),
 (SELECT count(*) FROM score_release_item cohort WHERE cohort.tenant_id=r.tenant_id AND cohort.release_id=r.id AND cohort.student_id IS NOT NULL),
 (SELECT avg(CASE WHEN cohort.max_score>0 THEN cohort.total_score/cohort.max_score ELSE 0 END)::float8 FROM score_release_item cohort WHERE cohort.tenant_id=r.tenant_id AND cohort.release_id=r.id AND cohort.student_id IS NOT NULL)
FROM exam target JOIN exam e ON e.tenant_id=target.tenant_id AND e.deleted_at IS NULL
 AND ((target.exam_session_id IS NOT NULL AND e.exam_session_id=target.exam_session_id) OR e.id=target.id)
LEFT JOIN score_release_current current_release ON current_release.tenant_id=e.tenant_id AND current_release.exam_id=e.id
LEFT JOIN score_release r ON r.tenant_id=e.tenant_id AND r.id=CASE WHEN e.id=$2::uuid THEN $4::uuid ELSE current_release.release_id END AND r.status='published'
LEFT JOIN score_release_item mine ON mine.tenant_id=r.tenant_id AND mine.release_id=r.id AND mine.student_id=$3::uuid
WHERE target.tenant_id=$1::uuid AND target.id=$2::uuid AND target.deleted_at IS NULL
 AND (e.id=target.id OR EXISTS(SELECT 1 FROM exam_candidate_snapshot c WHERE c.tenant_id=e.tenant_id AND c.exam_id=e.id AND c.student_id=$3::uuid))
ORDER BY e.subject,e.id`, tenantID, examID, studentID, releaseID)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	summary := &StudentSessionSummary{Subjects: []StudentSessionSubject{}}
	total, max := 0.0, 0.0
	for rows.Next() {
		var item StudentSessionSubject
		var score, maximum, average sql.NullFloat64
		var published sql.NullTime
		var visible bool
		var count int
		if err := rows.Scan(&item.ExamID, &item.Subject, &item.Name, &score, &maximum, &published, &visible, &count, &average); err != nil {
			return nil, 0, 0, err
		}
		item.Status, item.StatisticsReason = "unpublished", "unpublished"
		if score.Valid && maximum.Valid && published.Valid {
			item.Status = "published"
			item.Score, item.MaxScore, item.PublishedAt = &score.Float64, &maximum.Float64, &published.Time
			summary.PublishedCount++
			total += score.Float64
			max += maximum.Float64
			item.StatisticsReason = "not_public"
			if visible {
				item.StatisticsReason = "small_cohort"
				if count >= privacyMinCohortSize && average.Valid {
					item.StatisticsAvailable = true
					item.StatisticsReason = ""
					item.MeanScoreRate = &average.Float64
					item.SampleSize = count
				}
			}
		}
		summary.Subjects = append(summary.Subjects, item)
	}
	summary.ExpectedCount = len(summary.Subjects)
	return summary, total, max, rows.Err()
}
