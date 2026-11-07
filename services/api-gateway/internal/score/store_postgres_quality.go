package score

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/score/autoprep"
)

// CheckQuality 是发布前的只读读取。publish 阶段在自动准备前提成立时按“创建草稿自动准备之后”的状态判断，
// 不报告准备能消除的阻断；confirmation 阶段与所有写操作内部的检查仍用严格版本。
func (s *PostgresStore) CheckQuality(ctx context.Context, tenantID string, examID string, requirePendingPublish bool) (QualityReport, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return QualityReport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	prepared := false
	if requirePendingPublish {
		state, err := autoprep.ReadState(ctx, tx, tenantID, examID)
		if err != nil {
			return QualityReport{}, err
		}
		prepared = state.Automatic()
	}
	quality, err := s.qualityReportModeTx(ctx, tx, tenantID, examID, requirePendingPublish, prepared)
	if err != nil {
		return QualityReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return QualityReport{}, err
	}
	return quality, nil
}

// qualityReportTx 是事务内的严格检查，供汇总、确认、旧版发布和发布前准备使用，结果直接决定写入。
func (s *PostgresStore) qualityReportTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, requirePendingPublish bool) (QualityReport, error) {
	return s.qualityReportModeTx(ctx, tx, tenantID, examID, requirePendingPublish, false)
}

// qualityReportModeTx 的 prepared=true 只用于发布前的只读读取，且调用方已确认 autoprep.State.Automatic()。
// 此时按“创建草稿时自动准备之后”的状态判断：可直接生成的终评视为已有，准备会新建或重算的总分视为已更新，
// 准备成功后会确认全部待确认总分，所以不报 grades_not_confirmed。准备做不到的事实仍照常报告。
func (s *PostgresStore) qualityReportModeTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, requirePendingPublish bool, prepared bool) (QualityReport, error) {
	issues := []QualityIssue{}
	// 取消评分会保留任务历史；只统计仍可处理的状态，不能把已取消任务重新算作发布待办。
	checks := []struct {
		code    string
		message string
		query   string
	}{
		{
			code:    "capture_collection_incomplete",
			message: "finish collecting answer sheets and verify absentees before publishing",
			query:   autoprep.CollectionIncompleteQuery,
		},
		{
			code:    "unfinished_review_tasks",
			message: "there are unfinished review tasks",
			query: `SELECT COUNT(*) FROM review_task
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND status IN ('pending', 'assigned', 'in_progress', 'returned')`,
		},
		{
			code:    "unfinished_arbitration_tasks",
			message: "there are unfinished arbitration tasks",
			query: `SELECT COUNT(*) FROM arbitration_task
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND status IN ('pending', 'assigned')`,
		},
		{
			code:    "ocr_failed_unhandled",
			message: "there are failed OCR tasks",
			query: `SELECT COUNT(*) FROM ocr_task ot
JOIN submission sub ON sub.tenant_id = ot.tenant_id AND sub.id = ot.submission_id AND sub.deleted_at IS NULL
WHERE ot.tenant_id = $1 AND sub.exam_id::text = $2 AND ot.deleted_at IS NULL AND ot.status = 'failed'`,
		},
		{
			code:    "missing_final_grades",
			message: "there are answer segments without final grades",
			query:   missingFinalGradesQuery(prepared),
		},
	}
	for _, check := range checks {
		count, err := scalarCountTx(ctx, tx, check.query, tenantID, examID)
		if err != nil {
			return QualityReport{}, err
		}
		if count > 0 {
			issues = append(issues, QualityIssue{Code: check.code, Message: check.message, Blocking: true, Count: count})
		}
	}
	// 冻结名册无答卷者自动缺考，不制造缺交待办；收到的未知、重复或缺页答卷仍必须处理。
	rosterChecks := []struct {
		code    string
		message string
		query   string
	}{
		{
			code:    "unidentified_submission",
			message: "submissions are not uniquely matched to an expected student",
			query: `WITH roster AS (
  SELECT candidate.student_id AS id
  FROM exam_candidate_snapshot candidate
  WHERE candidate.tenant_id = $1 AND candidate.exam_id = $2::uuid
),
submission_counts AS (
  SELECT student_id, COUNT(*) AS count
  FROM submission
  WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL
  GROUP BY student_id
)
SELECT COUNT(*)
FROM submission sub
LEFT JOIN submission_counts sc ON sc.student_id = sub.student_id
WHERE sub.tenant_id = $1 AND sub.exam_id = $2::uuid AND sub.deleted_at IS NULL
  AND (
    sub.student_id IS NULL
    OR sub.identity_status IN ('unknown','conflict')
    OR NOT EXISTS (SELECT 1 FROM roster r WHERE r.id = sub.student_id)
    OR COALESCE(sc.count, 0) > 1
  )`,
		},
		{
			code:    "missing_pages_unresolved",
			message: "matched submissions have unresolved missing or rejected pages",
			query: `WITH roster AS (
  SELECT candidate.student_id AS id
  FROM exam_candidate_snapshot candidate
  WHERE candidate.tenant_id = $1 AND candidate.exam_id = $2::uuid
),
submission_counts AS (
  SELECT student_id, COUNT(*) AS count
  FROM submission
  WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL
  GROUP BY student_id
)
SELECT COUNT(*)
FROM submission sub
JOIN roster r ON r.id = sub.student_id
JOIN submission_counts sc ON sc.student_id = sub.student_id AND sc.count = 1
WHERE sub.tenant_id = $1 AND sub.exam_id = $2::uuid AND sub.deleted_at IS NULL
  AND (
    sub.actual_page_count < sub.expected_page_count
    OR sub.quality_status = 'failed'
    OR sub.status = 'rejected'
  )`,
		},
	}
	for _, check := range rosterChecks {
		count, err := scalarCountTx(ctx, tx, check.query, tenantID, examID)
		if err != nil {
			return QualityReport{}, err
		}
		if count > 0 {
			issues = append(issues, QualityIssue{Code: check.code, Message: check.message, Blocking: true, Count: count})
		}
	}
	completeIssues, err := s.gradeCompletenessTx(ctx, tx, tenantID, examID, issues, prepared)
	if err != nil {
		return QualityReport{}, err
	}
	issues = completeIssues
	issues, err = s.totalIntegrityIssuesTx(ctx, tx, tenantID, examID, issues, prepared)
	if err != nil {
		return QualityReport{}, err
	}
	if requirePendingPublish {
		total, err := s.countSubmissionGradesTx(ctx, tx, tenantID, examID)
		if err != nil {
			return QualityReport{}, err
		}
		if total == 0 && prepared {
			// 准备会为有终评或可自动生成终评的答卷新建总分；准备后仍为零才是真正没有可发布的成绩。
			total, err = scalarCountTx(ctx, tx, `WITH `+autoprep.FinalSourcesCTE+`
SELECT COUNT(*) FROM submission sub
WHERE sub.tenant_id = $1::uuid AND sub.exam_id = $2::uuid AND sub.deleted_at IS NULL AND `+autoprep.NewTotalPredicate("sub"), tenantID, examID)
			if err != nil {
				return QualityReport{}, err
			}
		}
		if total == 0 {
			issues = append(issues, QualityIssue{Code: "no_submission_grades", Message: "there are no submission grades to publish", Blocking: true, Count: 1})
		}
		if !prepared {
			unconfirmed, err := scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND status NOT IN ('confirmed', 'pending_publish', 'published', 'locked')`, tenantID, examID)
			if err != nil {
				return QualityReport{}, err
			}
			if unconfirmed > 0 {
				issues = append(issues, QualityIssue{Code: "grades_not_confirmed", Message: "there are grades not ready for publish", Blocking: true, Count: unconfirmed})
			}
		}
	}
	return QualityReport{Passed: len(issues) == 0, Issues: issues}, nil
}

// missingFinalGradesQuery 的严格版统计没有未删除终评的答题区；准备后版本再排除自动汇总会生成终评的答题区。
func missingFinalGradesQuery(prepared bool) string {
	const strict = `SELECT COUNT(*) FROM answer_segment seg
WHERE seg.tenant_id = $1 AND EXISTS (
  SELECT 1 FROM submission sub WHERE sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id AND sub.exam_id::text = $2 AND sub.deleted_at IS NULL
)
AND seg.deleted_at IS NULL
AND NOT EXISTS (
  SELECT 1 FROM final_grade fg WHERE fg.tenant_id = seg.tenant_id AND fg.answer_segment_id = seg.id AND fg.deleted_at IS NULL
)`
	if !prepared {
		return strict
	}
	return `WITH ` + autoprep.FinalSourcesCTE + `
` + strict + `
AND NOT EXISTS (SELECT 1 FROM automatic_final af WHERE af.answer_segment_id = seg.id)`
}
