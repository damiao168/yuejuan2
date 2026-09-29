package score

import (
	"bytes"
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/csvsafe"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) FinalizeExam(ctx context.Context, tenantID string, examID string, actorID string) (FinalizeResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FinalizeResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExamTx(ctx, tx, tenantID, examID); err != nil {
		return FinalizeResult{}, err
	}
	if locked, err := s.hasLockedGradesTx(ctx, tx, tenantID, examID); err != nil {
		return FinalizeResult{}, err
	} else if locked {
		return FinalizeResult{}, ErrInvalidTransition
	}
	if invalid, err := s.countUnexpectedSubmissionStatusesTx(ctx, tx, tenantID, examID, "pending_confirmation"); err != nil {
		return FinalizeResult{}, err
	} else if invalid > 0 {
		return FinalizeResult{}, ErrInvalidTransition
	}
	createdHuman, err := s.createFinalsFromHumanTx(ctx, tx, tenantID, examID, actorID)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("create finals from human: %w", err)
	}
	createdRule, err := s.createFinalsFromRuleTx(ctx, tx, tenantID, examID, actorID)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("create finals from rule: %w", err)
	}
	if err := s.recalculateSubmissionGradesTx(ctx, tx, tenantID, examID, actorID); err != nil {
		return FinalizeResult{}, fmt.Errorf("recalculate submission grades: %w", err)
	}
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, false)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("quality report: %w", err)
	}
	grades, err := s.listGradesTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("list grades: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return FinalizeResult{}, err
	}
	return FinalizeResult{
		Status:            "pending_confirmation",
		CreatedFinals:     int(createdHuman + createdRule),
		SubmissionGrades:  grades,
		Quality:           quality,
		AvailableStatuses: Statuses(),
	}, nil
}

func (s *PostgresStore) ListExamGrades(ctx context.Context, tenantID string, examID string, filter GradeListFilter) (GradeListResult, error) {
	result := GradeListResult{Grades: []SubmissionGrade{}}
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(BOOL_AND(locked OR status IN ('published', 'locked')), false)
FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
`, tenantID, examID).Scan(&result.Total, &result.AllLocked); err != nil {
		return GradeListResult{}, err
	}

	where := "sg.tenant_id = $1 AND sg.exam_id::text = $2 AND sg.deleted_at IS NULL"
	args := []any{tenantID, examID}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where += fmt.Sprintf(" AND sg.status = $%d", len(args))
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		args = append(args, "%"+query+"%")
		placeholder := fmt.Sprintf("$%d", len(args))
		where += " AND (sg.anonymous_code ILIKE " + placeholder +
			" OR sg.submission_id::text ILIKE " + placeholder +
			" OR COALESCE(candidate.student_name_snapshot, '') ILIKE " + placeholder +
			" OR COALESCE(candidate.student_no_snapshot, '') ILIKE " + placeholder +
			" OR COALESCE(candidate.class_name_snapshot, '') ILIKE " + placeholder + ")"
	}
	countQuery := `
SELECT COUNT(*)
FROM submission_grade sg
LEFT JOIN exam_candidate_snapshot candidate
  ON candidate.tenant_id=sg.tenant_id AND candidate.exam_id=sg.exam_id AND candidate.student_id=sg.student_id
WHERE ` + where
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&result.FilteredTotal); err != nil {
		return GradeListResult{}, err
	}

	pageWhere := where
	pageArgs := append([]any(nil), args...)
	if filter.CursorAnonymousCode != "" && filter.CursorID != "" {
		pageArgs = append(pageArgs, filter.CursorAnonymousCode, filter.CursorID)
		codeArg, idArg := len(pageArgs)-1, len(pageArgs)
		pageWhere += fmt.Sprintf(" AND (sg.anonymous_code > $%d OR (sg.anonymous_code = $%d AND sg.id::text > $%d))", codeArg, codeArg, idArg)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 51
	}
	pageArgs = append(pageArgs, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT sg.id::text, sg.tenant_id::text, sg.exam_id::text, sg.submission_id::text, COALESCE(sg.student_id::text, ''),
  sg.anonymous_code, sg.total_score::float8, sg.max_score::float8, sg.status, sg.locked,
  COALESCE(sg.confirmed_by::text, ''), sg.confirmed_at, COALESCE(sg.published_by::text, ''), sg.published_at,
  sg.revision, sg.created_by::text, sg.created_at, sg.updated_at
FROM submission_grade sg
LEFT JOIN exam_candidate_snapshot candidate
  ON candidate.tenant_id=sg.tenant_id AND candidate.exam_id=sg.exam_id AND candidate.student_id=sg.student_id
WHERE `+pageWhere+`
ORDER BY sg.anonymous_code, sg.id
LIMIT $`+fmt.Sprint(len(pageArgs)), pageArgs...)
	if err != nil {
		return GradeListResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		grade, scanErr := scanSubmissionGrade(rows)
		if scanErr != nil {
			return GradeListResult{}, scanErr
		}
		result.Grades = append(result.Grades, grade)
	}
	if err := rows.Err(); err != nil {
		return GradeListResult{}, err
	}
	if err := rows.Close(); err != nil {
		return GradeListResult{}, err
	}
	if len(result.Grades) > 0 {
		items, err := s.listFinalGradesForSubmissions(ctx, tenantID, result.Grades)
		if err != nil {
			return GradeListResult{}, err
		}
		bySubmission := make(map[string][]FinalGrade, len(result.Grades))
		for _, item := range items {
			bySubmission[item.SubmissionID] = append(bySubmission[item.SubmissionID], item)
		}
		for i := range result.Grades {
			result.Grades[i].Items = bySubmission[result.Grades[i].SubmissionID]
		}
	}
	return result, nil
}

func (s *PostgresStore) CheckQuality(ctx context.Context, tenantID string, examID string, requirePendingPublish bool) (QualityReport, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return QualityReport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, requirePendingPublish)
	if err != nil {
		return QualityReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return QualityReport{}, err
	}
	return quality, nil
}

func (s *PostgresStore) ListRoster(ctx context.Context, tenantID string, examID string) (RosterReport, error) {
	entries, err := s.listRosterEntries(ctx, tenantID, examID)
	if err != nil {
		return RosterReport{}, err
	}
	var summary RosterSummary
	if err := s.db.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(*)
   FROM exam_candidate_snapshot candidate
   WHERE candidate.tenant_id = $1 AND candidate.exam_id = $2::uuid),
  (SELECT COUNT(*)
   FROM submission sub
   WHERE sub.tenant_id = $1 AND sub.exam_id = $2::uuid AND sub.deleted_at IS NULL)
	`, tenantID, examID).Scan(&summary.Expected, &summary.Received); err != nil {
		return RosterReport{}, err
	}
	for _, entry := range entries {
		switch entry.Status {
		case "graded":
			summary.Graded++
		case "absent":
			summary.Absent++
		case "missing_pages":
			summary.MissingPages++
			summary.Unresolved++
		default:
			summary.Unresolved++
		}
		if strings.HasPrefix(entry.Key, "submission:") {
			summary.Unidentified++
		}
	}
	return RosterReport{Entries: entries, Summary: summary}, nil
}

func (s *PostgresStore) SetAttendance(ctx context.Context, tenantID string, examID string, studentID string, actorID string, input AttendanceInput) (RosterReport, error) {
	var valid bool
	input, valid = normalizeAttendanceInput(input)
	if !valid {
		return RosterReport{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RosterReport{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExamTx(ctx, tx, tenantID, examID); err != nil {
		return RosterReport{}, err
	}
	var examStatus string
	if err := tx.QueryRowContext(ctx, `
SELECT e.status
FROM exam e
WHERE e.tenant_id = $1 AND e.id = $2::uuid AND e.deleted_at IS NULL
  AND EXISTS (
    SELECT 1 FROM exam_candidate_snapshot candidate
    WHERE candidate.tenant_id=e.tenant_id AND candidate.exam_id=e.id
      AND candidate.student_id=$3::uuid
  )
FOR UPDATE
`, tenantID, examID, studentID).Scan(&examStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RosterReport{}, ErrNotFound
		}
		return RosterReport{}, err
	}
	if examStatus == "published" || examStatus == "archived" {
		return RosterReport{}, ErrInvalidTransition
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO exam_student_attendance (
  tenant_id, exam_id, student_id, status, reason, marked_by, marked_at
)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::uuid, now())
ON CONFLICT (tenant_id, exam_id, student_id) DO UPDATE
SET status = EXCLUDED.status,
    reason = EXCLUDED.reason,
    marked_by = EXCLUDED.marked_by,
    marked_at = now(),
    updated_at = now(),
    deleted_at = NULL
`, tenantID, examID, studentID, input.Status, input.Reason, actorID); err != nil {
		return RosterReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return RosterReport{}, err
	}
	return s.ListRoster(ctx, tenantID, examID)
}

func (s *PostgresStore) ConfirmGrades(ctx context.Context, tenantID string, examID string, actorID string, input ConfirmInput) ([]SubmissionGrade, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var replay []SubmissionGrade
	if found, err := commandreceipt.Load(ctx, tx, tenantID, actorID, "score.confirm", examID, input, &replay); err != nil || found {
		return replay, err
	}
	if err := lockExamTx(ctx, tx, tenantID, examID); err != nil {
		return nil, err
	}
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, false)
	if err != nil {
		return nil, err
	}
	if !quality.Passed {
		return nil, ErrQualityGateFailed
	}
	count, err := s.countSubmissionGradesTx(ctx, tx, tenantID, examID)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, ErrNotFound
	}
	if invalid, err := s.countUnexpectedSubmissionStatusesTx(ctx, tx, tenantID, examID, "pending_confirmation"); err != nil {
		return nil, err
	} else if invalid > 0 {
		return nil, ErrInvalidTransition
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE submission_grade
SET status = 'confirmed', confirmed_by = $3::uuid, confirmed_at = now(), revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND locked = false AND status = 'pending_confirmation'
`, tenantID, examID, actorID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE final_grade
SET status = 'confirmed', updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND locked = false AND status = 'pending_confirmation'
`, tenantID, examID); err != nil {
		return nil, err
	}
	grades, err := s.listGradesTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return nil, err
	}
	if err := commandreceipt.Save(ctx, tx, tenantID, actorID, "score.confirm", examID, input, grades); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return grades, nil
}

func (s *PostgresStore) PublishGrades(ctx context.Context, tenantID string, examID string, actorID string, input PublishInput) (PublishResult, error) {
	// 总分公开、题分锁定、考试状态和命令回执在同一事务提交，失败不能只留下部分发布事实。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var replay PublishResult
	if found, err := commandreceipt.Load(ctx, tx, tenantID, actorID, "score.publish", examID, input, &replay); err != nil || found {
		return replay, err
	}
	if err := lockExamTx(ctx, tx, tenantID, examID); err != nil {
		return PublishResult{}, err
	}
	if locked, err := s.hasLockedGradesTx(ctx, tx, tenantID, examID); err != nil {
		return PublishResult{}, err
	} else if locked {
		return PublishResult{}, ErrInvalidTransition
	}
	quality, err := s.qualityReportTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return PublishResult{}, err
	}
	if !quality.Passed {
		return PublishResult{Status: "blocked", Quality: quality}, ErrQualityGateFailed
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE submission_grade
SET status = 'published', locked = true, published_by = $3::uuid, published_at = now(), revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
`, tenantID, examID, actorID); err != nil {
		return PublishResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE final_grade
SET status = 'locked', locked = true, updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
`, tenantID, examID); err != nil {
		return PublishResult{}, err
	}
	if err := publishExamTx(ctx, tx, tenantID, examID); err != nil {
		return PublishResult{}, err
	}
	grades, err := s.listGradesTx(ctx, tx, tenantID, examID, true)
	if err != nil {
		return PublishResult{}, err
	}
	result := PublishResult{
		Status:           "published",
		SubmissionGrades: grades,
		Quality:          QualityReport{Passed: true},
		PublishedAt:      now,
	}

	if err := commandreceipt.Save(ctx, tx, tenantID, actorID, "score.publish", examID, input, result); err != nil {
		return PublishResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishResult{}, err
	}
	return result, nil
}

func (s *PostgresStore) GetStudentGrade(ctx context.Context, tenantID string, studentID string, examID string) (SubmissionGrade, error) {
	grade, err := scanSubmissionGrade(s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, submission_id::text, COALESCE(student_id::text, ''),
  anonymous_code, total_score::float8, max_score::float8, status, locked,
  COALESCE(confirmed_by::text, ''), confirmed_at, COALESCE(published_by::text, ''), published_at,
  revision, created_by::text, created_at, updated_at
FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND student_id::text = $3 AND status = 'published' AND locked = true AND deleted_at IS NULL
`, tenantID, examID, studentID))
	if err != nil {
		return SubmissionGrade{}, err
	}
	items, err := s.listFinalGrades(ctx, tenantID, grade.SubmissionID)
	if err != nil {
		return SubmissionGrade{}, err
	}
	grade.Items = items
	return grade, nil
}

func (s *PostgresStore) ExportGradesCSV(ctx context.Context, tenantID string, examID string, actorID string) (ExportResult, error) {
	grades, err := s.listGrades(ctx, tenantID, examID, false)
	if err != nil {
		return ExportResult{}, err
	}
	if len(grades) == 0 {
		return ExportResult{}, ErrNotFound
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	exportedAt := time.Now().UTC().Format(time.RFC3339)
	watermark := fmt.Sprintf("EduGrade export tenant=%s exam=%s actor=%s at=%s", tenantID, examID, actorID, exportedAt)
	_ = writer.Write([]string{"submission_id", "student_id", "anonymous_code", "total_score", "max_score", "status", "locked", "exported_by", "exported_at", "watermark"})
	for _, grade := range grades {
		_ = writer.Write(csvsafe.Row([]string{
			grade.SubmissionID,
			grade.StudentID,
			grade.AnonymousCode,
			fmt.Sprintf("%.2f", grade.TotalScore),
			fmt.Sprintf("%.2f", grade.MaxScore),
			grade.Status,
			fmt.Sprintf("%t", grade.Locked),
			actorID,
			exportedAt,
			watermark,
		}))
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{
		Filename:    fmt.Sprintf("exam-%s-grades.csv", examID),
		ContentType: "text/csv; charset=utf-8",
		Content:     buf.Bytes(),
		RowCount:    len(grades),
		Watermark:   watermark,
	}, nil
}

func (s *PostgresStore) createFinalsFromHumanTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, actorID string) (int64, error) {
	result, err := tx.ExecContext(ctx, `
WITH latest_human AS (
  SELECT DISTINCT ON (rt.answer_segment_id)
    rt.tenant_id, rt.exam_id, rt.question_id, rt.question_no, rt.answer_segment_id, rt.submission_id,
    rt.anonymous_code, hg.score, hg.max_score
  FROM review_task rt
  JOIN human_grade hg ON hg.tenant_id = rt.tenant_id AND hg.review_task_id = rt.id AND hg.deleted_at IS NULL
  WHERE rt.tenant_id = $1
    AND rt.exam_id::text = $2
    AND rt.deleted_at IS NULL
    AND rt.grade_round = 'single'
    AND rt.status IN ('submitted', 'completed')
  ORDER BY rt.answer_segment_id, hg.created_at DESC
)
INSERT INTO final_grade (
  tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, source, status, locked, created_by
)
SELECT tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, 'single_review', 'pending_confirmation', false, $3::uuid
FROM latest_human lh
WHERE NOT EXISTS (
  SELECT 1 FROM final_grade fg
  WHERE fg.tenant_id = lh.tenant_id AND fg.answer_segment_id = lh.answer_segment_id AND fg.deleted_at IS NULL
)
`, tenantID, examID, actorID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *PostgresStore) createFinalsFromRuleTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, actorID string) (int64, error) {
	// 已有 question_grade 时使用其当前已确认规则结果；旧 ai_grade 只兼容尚未迁入题分模型的答题区域。
	// 兼容路径仍排除 Mock、待复核及非规则评分结果，避免把模型建议直接变为最终题分。
	result, err := tx.ExecContext(ctx, `
WITH confirmed_rule AS (
  SELECT g.tenant_id, g.exam_id, g.question_id, seg.question_no, g.answer_segment_id,
    seg.submission_id, COALESCE(NULLIF(sub.candidate_no, ''), seg.id::text) AS anonymous_code,
    g.score, g.max_score
  FROM question_grade g
  JOIN answer_segment seg ON seg.tenant_id=g.tenant_id AND seg.id=g.answer_segment_id AND seg.deleted_at IS NULL
  JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
  WHERE g.tenant_id=$1 AND g.exam_id::text=$2 AND g.deleted_at IS NULL
    AND g.is_current AND g.status='confirmed' AND g.source='rule_confirmed'
), latest_legacy_rule AS (
  SELECT DISTINCT ON (ag.answer_segment_id)
    ag.tenant_id, sub.exam_id, ag.question_id, ag.question_no, ag.answer_segment_id,
    seg.submission_id, COALESCE(NULLIF(sub.candidate_no, ''), seg.id::text) AS anonymous_code,
    ag.suggested_score AS score, ag.max_score
  FROM ai_grade ag
  JOIN answer_segment seg ON seg.tenant_id = ag.tenant_id AND seg.id = ag.answer_segment_id AND seg.deleted_at IS NULL
  JOIN submission sub ON sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id AND sub.deleted_at IS NULL
  WHERE ag.tenant_id = $1
    AND sub.exam_id::text = $2
    AND ag.deleted_at IS NULL
    AND ag.grader_type = 'rule_based_objective'
    AND ag.auto_pass = true
    AND ag.needs_human_review = false
    AND ag.mock = false
    AND ag.status = 'succeeded'
    AND NOT EXISTS (
      SELECT 1 FROM question_grade g
      WHERE g.tenant_id=ag.tenant_id AND g.answer_segment_id=ag.answer_segment_id AND g.deleted_at IS NULL
    )
  ORDER BY ag.answer_segment_id, ag.created_at DESC
), latest_rule AS (
  SELECT * FROM confirmed_rule
  UNION ALL
  SELECT * FROM latest_legacy_rule
)
INSERT INTO final_grade (
  tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, source, status, locked, created_by
)
SELECT tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, 'rule_auto', 'pending_confirmation', false, $3::uuid
FROM latest_rule lr
WHERE NOT EXISTS (
  SELECT 1 FROM final_grade fg
  WHERE fg.tenant_id = lr.tenant_id AND fg.answer_segment_id = lr.answer_segment_id AND fg.deleted_at IS NULL
)
`, tenantID, examID, actorID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *PostgresStore) recalculateSubmissionGradesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, actorID string) error {
	_, err := tx.ExecContext(ctx, `
WITH latest_final AS (
  SELECT DISTINCT ON (answer_segment_id)
    tenant_id, exam_id, submission_id, anonymous_code, score, max_score
  FROM final_grade
  WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
  ORDER BY answer_segment_id, created_at DESC
),
summed AS (
  SELECT
    lf.tenant_id,
    lf.exam_id,
    lf.submission_id,
    COALESCE(sub.student_id, NULL) AS student_id,
    COALESCE(NULLIF(sub.candidate_no, ''), MIN(lf.anonymous_code)) AS anonymous_code,
    SUM(lf.score) AS total_score,
    SUM(lf.max_score) AS max_score
  FROM latest_final lf
  JOIN submission sub ON sub.tenant_id = lf.tenant_id AND sub.id = lf.submission_id AND sub.deleted_at IS NULL
  GROUP BY lf.tenant_id, lf.exam_id, lf.submission_id, sub.student_id, sub.candidate_no
)
INSERT INTO submission_grade (
  tenant_id, exam_id, submission_id, student_id, anonymous_code, total_score, max_score, status, locked, created_by
)
SELECT tenant_id, exam_id, submission_id, student_id, anonymous_code, total_score, max_score, 'pending_confirmation', false, $3::uuid
FROM summed
ON CONFLICT (tenant_id, exam_id, submission_id) DO UPDATE
SET total_score = EXCLUDED.total_score,
    max_score = EXCLUDED.max_score,
    anonymous_code = EXCLUDED.anonymous_code,
    student_id = EXCLUDED.student_id,
    status = CASE WHEN submission_grade.locked THEN submission_grade.status ELSE 'pending_confirmation' END,
    revision = submission_grade.revision + 1,
    updated_at = now()
`, tenantID, examID, actorID)
	return err
}

func (s *PostgresStore) qualityReportTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, requirePendingPublish bool) (QualityReport, error) {
	issues := []QualityIssue{}
	checks := []struct {
		code    string
		message string
		query   string
	}{
		{
			code:    "unfinished_review_tasks",
			message: "there are unfinished review tasks",
			query: `SELECT COUNT(*) FROM review_task
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND status NOT IN ('submitted', 'completed')`,
		},
		{
			code:    "unfinished_arbitration_tasks",
			message: "there are unfinished arbitration tasks",
			query: `SELECT COUNT(*) FROM arbitration_task
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND status <> 'submitted'`,
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
			query: `SELECT COUNT(*) FROM answer_segment seg
WHERE seg.tenant_id = $1 AND EXISTS (
  SELECT 1 FROM submission sub WHERE sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id AND sub.exam_id::text = $2 AND sub.deleted_at IS NULL
)
AND seg.deleted_at IS NULL
AND NOT EXISTS (
  SELECT 1 FROM final_grade fg WHERE fg.tenant_id = seg.tenant_id AND fg.answer_segment_id = seg.id AND fg.deleted_at IS NULL
)`,
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
	if requirePendingPublish {
		rosterChecks := []struct {
			code    string
			message string
			query   string
		}{
			{
				code:    "missing_submission_unresolved",
				message: "expected students have no matched submission",
				query: `WITH roster AS (
  SELECT candidate.student_id AS id
  FROM exam_candidate_snapshot candidate
  LEFT JOIN exam_student_attendance ea ON ea.tenant_id = candidate.tenant_id AND ea.exam_id = candidate.exam_id
    AND ea.student_id = candidate.student_id AND ea.deleted_at IS NULL
  WHERE candidate.tenant_id = $1 AND candidate.exam_id = $2::uuid
    AND COALESCE(ea.status, 'expected') <> 'absent'
)
SELECT COUNT(*) FROM roster r
WHERE NOT EXISTS (
  SELECT 1 FROM submission sub
  WHERE sub.tenant_id = $1 AND sub.exam_id = $2::uuid
    AND sub.student_id = r.id AND sub.deleted_at IS NULL
)`,
			},
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
LEFT JOIN exam_student_attendance ea ON ea.tenant_id = sub.tenant_id AND ea.exam_id = sub.exam_id
  AND ea.student_id = sub.student_id AND ea.deleted_at IS NULL
WHERE sub.tenant_id = $1 AND sub.exam_id = $2::uuid AND sub.deleted_at IS NULL
  AND (
    sub.student_id IS NULL
    OR NOT EXISTS (SELECT 1 FROM roster r WHERE r.id = sub.student_id)
    OR COALESCE(ea.status, 'expected') = 'absent'
    OR COALESCE(sc.count, 0) > 1
  )`,
			},
			{
				code:    "missing_pages_unresolved",
				message: "matched submissions have unresolved missing or rejected pages",
				query: `WITH roster AS (
  SELECT candidate.student_id AS id
  FROM exam_candidate_snapshot candidate
  LEFT JOIN exam_student_attendance ea ON ea.tenant_id = candidate.tenant_id AND ea.exam_id = candidate.exam_id
    AND ea.student_id = candidate.student_id AND ea.deleted_at IS NULL
  WHERE candidate.tenant_id = $1 AND candidate.exam_id = $2::uuid
    AND COALESCE(ea.status, 'expected') <> 'absent'
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
		total, err := s.countSubmissionGradesTx(ctx, tx, tenantID, examID)
		if err != nil {
			return QualityReport{}, err
		}
		if total == 0 {
			issues = append(issues, QualityIssue{Code: "no_submission_grades", Message: "there are no submission grades to publish", Blocking: true, Count: 1})
		}
		unconfirmed, err := scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND status NOT IN ('confirmed', 'pending_publish', 'published', 'locked')`, tenantID, examID)
		if err != nil {
			return QualityReport{}, err
		}
		if unconfirmed > 0 {
			issues = append(issues, QualityIssue{Code: "grades_not_confirmed", Message: "there are grades not ready for publish", Blocking: true, Count: unconfirmed})
		}
	}
	return QualityReport{Passed: len(issues) == 0, Issues: issues}, nil
}

func (s *PostgresStore) listRosterEntries(ctx context.Context, tenantID string, examID string) ([]RosterEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
  'student:' || candidate.student_id::text,
  candidate.student_id::text, candidate.student_no_snapshot, candidate.student_name_snapshot,
  candidate.class_id_snapshot::text, candidate.class_name_snapshot,
  CASE WHEN COALESCE(sub.submission_count, 0) = 1 THEN COALESCE(sub.id::text, '') ELSE '' END,
  CASE WHEN COALESCE(sub.submission_count, 0) = 1 THEN COALESCE(sub.candidate_no, '') ELSE '' END,
  CASE
    WHEN COALESCE(ea.status, 'expected') = 'absent' AND COALESCE(sub.submission_count, 0) = 0 THEN 'absent'
    WHEN COALESCE(sub.submission_count, 0) <> 1 OR COALESCE(ea.status, 'expected') = 'absent' THEN 'unmatched'
    WHEN sub.actual_page_count < sub.expected_page_count OR sub.quality_status = 'failed' OR sub.status = 'rejected' THEN 'missing_pages'
    WHEN sg.id IS NOT NULL THEN 'graded'
    ELSE 'unmatched'
  END,
  CASE
    WHEN COALESCE(ea.status, 'expected') = 'absent' AND COALESCE(sub.submission_count, 0) = 0 THEN 'absent'
    WHEN COALESCE(ea.status, 'expected') = 'absent' THEN 'absent_has_submission'
    WHEN COALESCE(sub.submission_count, 0) = 0 THEN 'missing_submission'
    WHEN COALESCE(sub.submission_count, 0) > 1 THEN 'duplicate_submission'
    WHEN sub.actual_page_count < sub.expected_page_count OR sub.quality_status = 'failed' OR sub.status = 'rejected' THEN 'missing_pages'
    WHEN sg.id IS NOT NULL THEN 'graded'
    ELSE 'grading_incomplete'
  END,
  COALESCE(sub.expected_page_count, 0), COALESCE(sub.actual_page_count, 0),
  sg.total_score::float8, sg.max_score::float8,
  COALESCE(ea.reason, ''), COALESCE(ea.marked_by::text, ''), ea.marked_at
FROM exam_candidate_snapshot candidate
LEFT JOIN LATERAL (
  SELECT candidate_submission.*, COUNT(*) OVER () AS submission_count
  FROM submission candidate_submission
  WHERE candidate_submission.tenant_id = candidate.tenant_id
    AND candidate_submission.exam_id = candidate.exam_id
    AND candidate_submission.student_id = candidate.student_id
    AND candidate_submission.deleted_at IS NULL
  ORDER BY candidate_submission.created_at DESC, candidate_submission.id
  LIMIT 1
) sub ON true
LEFT JOIN submission_grade sg ON sg.tenant_id = $1 AND sg.exam_id = $2::uuid
  AND sg.submission_id = sub.id AND sg.deleted_at IS NULL
LEFT JOIN exam_student_attendance ea ON ea.tenant_id = $1 AND ea.exam_id = $2::uuid
  AND ea.student_id = candidate.student_id AND ea.deleted_at IS NULL
WHERE candidate.tenant_id = $1 AND candidate.exam_id = $2::uuid
ORDER BY candidate.class_name_snapshot, candidate.student_no_snapshot, candidate.student_name_snapshot
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []RosterEntry{}
	for rows.Next() {
		entry, err := scanRosterEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	unidentified, err := s.db.QueryContext(ctx, `
WITH roster AS (
  SELECT candidate.student_id AS id,
    candidate.student_no_snapshot AS student_no,
    candidate.student_name_snapshot AS student_name,
    candidate.class_id_snapshot AS class_id,
    candidate.class_name_snapshot AS class_name
  FROM exam_candidate_snapshot candidate
  WHERE candidate.tenant_id = $1 AND candidate.exam_id = $2::uuid
),
submission_counts AS (
  SELECT student_id, COUNT(*) AS count
  FROM submission
  WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL
  GROUP BY student_id
)
SELECT
  'submission:' || sub.id::text,
  COALESCE(sub.student_id::text, ''), COALESCE(r.student_no, ''), COALESCE(r.student_name, ''),
  COALESCE(r.class_id::text, ''), COALESCE(r.class_name, ''),
  sub.id::text, COALESCE(sub.candidate_no, ''),
  'unmatched',
  CASE
    WHEN sub.student_id IS NULL THEN 'student_unidentified'
    WHEN r.id IS NULL THEN 'student_not_in_roster'
    WHEN COALESCE(ea.status, 'expected') = 'absent' THEN 'absent_has_submission'
    ELSE 'duplicate_submission'
  END,
  sub.expected_page_count, sub.actual_page_count,
  NULL::float8, NULL::float8, '', '', NULL::timestamptz
FROM submission sub
LEFT JOIN roster r ON r.id = sub.student_id
LEFT JOIN submission_counts sc ON sc.student_id = sub.student_id
LEFT JOIN exam_student_attendance ea ON ea.tenant_id = sub.tenant_id AND ea.exam_id = sub.exam_id
  AND ea.student_id = sub.student_id AND ea.deleted_at IS NULL
WHERE sub.tenant_id = $1 AND sub.exam_id = $2::uuid AND sub.deleted_at IS NULL
  AND (
    sub.student_id IS NULL OR r.id IS NULL
    OR COALESCE(ea.status, 'expected') = 'absent'
    OR COALESCE(sc.count, 0) > 1
  )
ORDER BY sub.created_at, sub.id
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer unidentified.Close()
	for unidentified.Next() {
		entry, err := scanRosterEntry(unidentified)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, unidentified.Err()
}

func (s *PostgresStore) listGrades(ctx context.Context, tenantID string, examID string, withItems bool) ([]SubmissionGrade, error) {
	return s.listGradesWithQueryer(ctx, s.db, tenantID, examID, withItems)
}

func (s *PostgresStore) listGradesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, withItems bool) ([]SubmissionGrade, error) {
	return s.listGradesWithQueryer(ctx, tx, tenantID, examID, withItems)
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *PostgresStore) listGradesWithQueryer(ctx context.Context, q queryer, tenantID string, examID string, withItems bool) ([]SubmissionGrade, error) {
	rows, err := q.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, submission_id::text, COALESCE(student_id::text, ''),
  anonymous_code, total_score::float8, max_score::float8, status, locked,
  COALESCE(confirmed_by::text, ''), confirmed_at, COALESCE(published_by::text, ''), published_at,
  revision, created_by::text, created_at, updated_at
FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
ORDER BY anonymous_code, created_at
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubmissionGrade{}
	for rows.Next() {
		grade, err := scanSubmissionGrade(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, grade)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if withItems && len(out) > 0 {
		items, err := s.listFinalGradesForExamWithQueryer(ctx, q, tenantID, examID)
		if err != nil {
			return nil, err
		}
		bySubmission := make(map[string][]FinalGrade, len(out))
		for _, item := range items {
			bySubmission[item.SubmissionID] = append(bySubmission[item.SubmissionID], item)
		}
		for i := range out {
			out[i].Items = bySubmission[out[i].SubmissionID]
		}
	}
	return out, nil
}

func (s *PostgresStore) listFinalGrades(ctx context.Context, tenantID string, submissionID string) ([]FinalGrade, error) {
	return s.listFinalGradesWithQueryer(ctx, s.db, tenantID, submissionID)
}

func (s *PostgresStore) listFinalGradesWithQueryer(ctx context.Context, q queryer, tenantID string, submissionID string) ([]FinalGrade, error) {
	rows, err := q.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, score::float8, max_score::float8,
  source, status, locked, created_by::text, created_at, updated_at
FROM final_grade
WHERE tenant_id = $1 AND submission_id::text = $2 AND deleted_at IS NULL
ORDER BY question_no, created_at DESC
`, tenantID, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FinalGrade{}
	for rows.Next() {
		grade, err := scanFinalGrade(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, grade)
	}
	return out, rows.Err()
}

func (s *PostgresStore) listFinalGradesForExamWithQueryer(ctx context.Context, q queryer, tenantID string, examID string) ([]FinalGrade, error) {
	rows, err := q.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, score::float8, max_score::float8,
  source, status, locked, created_by::text, created_at, updated_at
FROM final_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
ORDER BY submission_id, question_no, created_at DESC
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FinalGrade, 0)
	for rows.Next() {
		grade, scanErr := scanFinalGrade(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, grade)
	}
	return out, rows.Err()
}

func (s *PostgresStore) listFinalGradesForSubmissions(ctx context.Context, tenantID string, grades []SubmissionGrade) ([]FinalGrade, error) {
	if len(grades) == 0 {
		return []FinalGrade{}, nil
	}
	args := make([]any, 0, len(grades)+1)
	args = append(args, tenantID)
	placeholders := make([]string, 0, len(grades))
	seen := make(map[string]struct{}, len(grades))
	for _, grade := range grades {
		if _, exists := seen[grade.SubmissionID]; exists {
			continue
		}
		seen[grade.SubmissionID] = struct{}{}
		args = append(args, grade.SubmissionID)
		placeholders = append(placeholders, fmt.Sprintf("$%d::uuid", len(args)))
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, score::float8, max_score::float8,
  source, status, locked, created_by::text, created_at, updated_at
FROM final_grade
WHERE tenant_id = $1 AND submission_id IN (`+strings.Join(placeholders, ",")+`) AND deleted_at IS NULL
ORDER BY submission_id, question_no, created_at DESC
`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FinalGrade, 0)
	for rows.Next() {
		grade, scanErr := scanFinalGrade(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, grade)
	}
	return out, rows.Err()
}

func (s *PostgresStore) hasLockedGradesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) (bool, error) {
	count, err := scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND (locked = true OR status IN ('published', 'locked'))`, tenantID, examID)
	return count > 0, err
}

func (s *PostgresStore) countSubmissionGradesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) (int, error) {
	return scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL`, tenantID, examID)
}

func (s *PostgresStore) countUnexpectedSubmissionStatusesTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, allowedStatus string) (int, error) {
	return scalarCountTx(ctx, tx, `SELECT COUNT(*) FROM submission_grade
WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL
  AND (locked = true OR status <> $3)`, tenantID, examID, allowedStatus)
}

// 同一考试的汇总、确认、发布及出勤修改共用事务锁；新增同类写操作也应遵守此约定。
func lockExamTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, tenantID, examID)
	return err
}

func publishExamTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string) error {
	result, err := tx.ExecContext(ctx, `
UPDATE exam
SET status = 'published', updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
  AND status IN ('draft', 'configured', 'ready', 'collecting', 'grading', 'reviewing', 'finalized')
`, tenantID, examID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 1 {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM exam WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
)
`, tenantID, examID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return ErrInvalidTransition
}

func scalarCountTx(ctx context.Context, tx *sql.Tx, query string, args ...any) (int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

type submissionGradeScanner interface {
	Scan(dest ...any) error
}

func scanRosterEntry(row submissionGradeScanner) (RosterEntry, error) {
	var out RosterEntry
	var totalScore sql.NullFloat64
	var maxScore sql.NullFloat64
	var markedAt sql.NullTime
	if err := row.Scan(
		&out.Key,
		&out.StudentID,
		&out.StudentNo,
		&out.StudentName,
		&out.ClassID,
		&out.ClassName,
		&out.SubmissionID,
		&out.CandidateNo,
		&out.Status,
		&out.ResolutionCode,
		&out.ExpectedPageCount,
		&out.ActualPageCount,
		&totalScore,
		&maxScore,
		&out.AttendanceReason,
		&out.MarkedBy,
		&markedAt,
	); err != nil {
		return RosterEntry{}, err
	}
	if totalScore.Valid {
		out.TotalScore = &totalScore.Float64
	}
	if maxScore.Valid {
		out.MaxScore = &maxScore.Float64
	}
	if markedAt.Valid {
		out.MarkedAt = &markedAt.Time
	}
	return out, nil
}

func scanSubmissionGrade(row submissionGradeScanner) (SubmissionGrade, error) {
	var out SubmissionGrade
	var confirmedAt sql.NullTime
	var publishedAt sql.NullTime
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.SubmissionID,
		&out.StudentID,
		&out.AnonymousCode,
		&out.TotalScore,
		&out.MaxScore,
		&out.Status,
		&out.Locked,
		&out.ConfirmedBy,
		&confirmedAt,
		&out.PublishedBy,
		&publishedAt,
		&out.Revision,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SubmissionGrade{}, ErrNotFound
		}
		return SubmissionGrade{}, err
	}
	if confirmedAt.Valid {
		out.ConfirmedAt = &confirmedAt.Time
	}
	if publishedAt.Valid {
		out.PublishedAt = &publishedAt.Time
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

type finalGradeScanner interface {
	Scan(dest ...any) error
}

func scanFinalGrade(row finalGradeScanner) (FinalGrade, error) {
	var out FinalGrade
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.AnswerSegmentID,
		&out.SubmissionID,
		&out.AnonymousCode,
		&out.Score,
		&out.MaxScore,
		&out.Source,
		&out.Status,
		&out.Locked,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FinalGrade{}, ErrNotFound
		}
		return FinalGrade{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}
