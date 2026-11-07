package score

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/score/autoprep"
	"strings"
)

func (s *PostgresStore) ListRoster(ctx context.Context, tenantID string, examID string) (RosterReport, error) {
	// 自动准备前提成立时，只差自动汇总的答卷标为 awaiting_aggregation；前提不成立时与原判断相同。
	state, err := autoprep.ReadState(ctx, s.db, tenantID, examID)
	if err != nil {
		return RosterReport{}, err
	}
	entries, err := s.listRosterEntries(ctx, tenantID, examID, state.Automatic())
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
		case "submitted":
			// 等待自动汇总的答卷会在创建草稿时补齐终评和总分，不是需要人工处理的待办。
			if entry.ResolutionCode != "awaiting_aggregation" {
				summary.Unresolved++
			}
		default:
			summary.Unresolved++
		}
		if strings.HasPrefix(entry.Key, "submission:") {
			summary.Unidentified++
		}
	}
	return RosterReport{Entries: entries, Summary: summary}, nil
}

// SetAttendance 保留内部旧调用的显式失败，防止历史客户端继续写入已退役的人工出勤事实。
func (s *PostgresStore) SetAttendance(context.Context, string, string, string, string, AttendanceInput) (RosterReport, error) {
	return RosterReport{}, ErrManualAttendanceRetired
}

// listRosterEntries 的 automatic 取自 autoprep.State.Automatic()。
func (s *PostgresStore) listRosterEntries(ctx context.Context, tenantID string, examID string, automatic bool) ([]RosterEntry, error) {
	// 先区分是否收到答卷，再检查重复、身份、页质和成绩；坏卷仍是已到卷，不能归为缺考绕过后续门禁。
	// awaiting_submission：没有总分，但每个答题区都已有终评或会自动生成终评、每道冻结题目都有答题区、
	// 本答卷没有进行中的阅卷或仲裁任务，且准备会新建总分。automatic=false 时为空集，结果与原判断相同。
	rows, err := s.db.QueryContext(ctx, `
WITH `+autoprep.FinalSourcesCTE+`, awaiting_submission AS (
  SELECT waiting.id FROM submission waiting
  WHERE $3::boolean AND waiting.tenant_id = $1::uuid AND waiting.exam_id = $2::uuid AND waiting.deleted_at IS NULL
    AND NOT EXISTS (
      SELECT 1 FROM answer_segment seg
      WHERE seg.tenant_id = waiting.tenant_id AND seg.submission_id = waiting.id AND seg.deleted_at IS NULL
        AND NOT EXISTS (SELECT 1 FROM final_grade fg WHERE fg.tenant_id = seg.tenant_id AND fg.answer_segment_id = seg.id AND fg.deleted_at IS NULL)
        AND NOT EXISTS (SELECT 1 FROM automatic_final af WHERE af.answer_segment_id = seg.id)
    )
    AND NOT EXISTS (
      SELECT 1 FROM exam_question_snapshot question
      WHERE question.tenant_id = waiting.tenant_id AND question.exam_id = waiting.exam_id
        AND NOT EXISTS (
          SELECT 1 FROM answer_segment seg
          WHERE seg.tenant_id = waiting.tenant_id AND seg.submission_id = waiting.id AND seg.question_id = question.question_id AND seg.deleted_at IS NULL
        )
    )
    AND NOT EXISTS (
      SELECT 1 FROM review_task rt
      WHERE rt.tenant_id = waiting.tenant_id AND rt.submission_id = waiting.id AND rt.deleted_at IS NULL
        AND rt.status IN ('pending', 'assigned', 'in_progress', 'returned')
    )
    AND NOT EXISTS (
      SELECT 1 FROM arbitration_task arb
      WHERE arb.tenant_id = waiting.tenant_id AND arb.submission_id = waiting.id AND arb.deleted_at IS NULL
        AND arb.status IN ('pending', 'assigned')
    )
    AND `+autoprep.NewTotalPredicate("waiting")+`
)
SELECT
  'student:' || candidate.student_id::text,
  candidate.student_id::text, candidate.student_no_snapshot, candidate.student_name_snapshot,
  candidate.class_id_snapshot::text, candidate.class_name_snapshot,
  CASE WHEN COALESCE(sub.submission_count, 0) = 1 THEN COALESCE(sub.id::text, '') ELSE '' END,
  CASE WHEN COALESCE(sub.submission_count, 0) = 1 THEN COALESCE(sub.candidate_no, '') ELSE '' END,
  CASE
    WHEN COALESCE(sub.submission_count, 0) = 0 THEN 'absent'
    WHEN COALESCE(sub.submission_count, 0) <> 1 THEN 'unmatched'
    WHEN sub.identity_status IN ('unknown','conflict') THEN 'unmatched'
    WHEN sub.actual_page_count < sub.expected_page_count OR sub.quality_status = 'failed' OR sub.status = 'rejected' THEN 'missing_pages'
    WHEN sg.id IS NOT NULL THEN 'graded'
    ELSE 'submitted'
  END,
  CASE
    WHEN COALESCE(sub.submission_count, 0) = 0 THEN 'absent'
    WHEN COALESCE(sub.submission_count, 0) > 1 THEN 'duplicate_submission'
    WHEN sub.identity_status IN ('unknown','conflict') THEN 'student_identity_unresolved'
    WHEN sub.actual_page_count < sub.expected_page_count OR sub.quality_status = 'failed' OR sub.status = 'rejected' THEN 'missing_pages'
    WHEN sg.id IS NOT NULL THEN 'graded'
    WHEN sub.id IN (SELECT id FROM awaiting_submission) THEN 'awaiting_aggregation'
    ELSE 'grading_incomplete'
  END,
  COALESCE(sub.expected_page_count, 0), COALESCE(sub.actual_page_count, 0),
  sg.total_score::float8, sg.max_score::float8,
  ''::text, ''::text, NULL::timestamptz
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
WHERE candidate.tenant_id = $1 AND candidate.exam_id = $2::uuid
ORDER BY candidate.class_name_snapshot, candidate.student_no_snapshot, candidate.student_name_snapshot
`, tenantID, examID, automatic)
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
    WHEN sub.identity_status IN ('unknown','conflict') THEN 'student_identity_unresolved'
    WHEN r.id IS NULL THEN 'student_not_in_roster'
    ELSE 'duplicate_submission'
  END,
  sub.expected_page_count, sub.actual_page_count,
  NULL::float8, NULL::float8, '', '', NULL::timestamptz
FROM submission sub
LEFT JOIN roster r ON r.id = sub.student_id
LEFT JOIN submission_counts sc ON sc.student_id = sub.student_id
WHERE sub.tenant_id = $1 AND sub.exam_id = $2::uuid AND sub.deleted_at IS NULL
  AND (
    sub.student_id IS NULL OR r.id IS NULL
    OR sub.identity_status IN ('unknown','conflict')
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
