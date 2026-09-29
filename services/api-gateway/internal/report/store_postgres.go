package report

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"encoding/json"
	"errors"
	"strings"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) StudentReport(ctx context.Context, tenantID string, examID string, studentID string) (StudentReport, error) {
	data, err := s.loadDataset(ctx, s.db, tenantID, examID)
	if err != nil {
		return StudentReport{}, err
	}
	return buildStudentReport(data, studentID), nil
}

func (s *PostgresStore) Overview(ctx context.Context, tenantID string, examID string) (OverviewReport, error) {
	data, err := s.loadDataset(ctx, s.db, tenantID, examID)
	if err != nil {
		return OverviewReport{}, err
	}
	return buildOverview(data), nil
}

func (s *PostgresStore) ClassReports(ctx context.Context, tenantID string, examID string) ([]ClassReport, error) {
	data, err := s.loadDataset(ctx, s.db, tenantID, examID)
	if err != nil {
		return nil, err
	}
	return buildClassReports(data), nil
}

func (s *PostgresStore) QuestionAnalysis(ctx context.Context, tenantID string, examID string) ([]QuestionAnalysis, error) {
	data, err := s.loadDataset(ctx, s.db, tenantID, examID)
	if err != nil {
		return nil, err
	}
	return buildQuestionAnalysis(data), nil
}

func (s *PostgresStore) GradingQuality(ctx context.Context, tenantID string, examID string) (GradingQualityReport, error) {
	data, err := s.loadDataset(ctx, s.db, tenantID, examID)
	if err != nil {
		return GradingQualityReport{}, err
	}
	return s.loadQuality(ctx, s.db, tenantID, examID, data)
}

func (s *PostgresStore) Export(ctx context.Context, tenantID string, examID string, actorID string) (ExportResult, error) {
	result, err := s.exportOnce(ctx, tenantID, examID, actorID)
	if err != nil && commandreceipt.ID(ctx) != "" && isUniqueViolation(err) {
		// 可重复读事务可能先建立快照，再等待同一命令的 advisory lock；旧快照看不到胜者回执，
		// 但唯一索引会使插入失败。事务已回滚后重新取一次快照即可安全返回不可变回执。
		return s.exportOnce(ctx, tenantID, examID, actorID)
	}
	return result, err
}

func (s *PostgresStore) exportOnce(ctx context.Context, tenantID string, examID string, actorID string) (ExportResult, error) {
	// CSV 和持久回执必须描述同一数据库快照；下面所有读取都使用此事务，不能改用 s.db，
	// 否则单连接池可能死锁，也会把不同提交点的数据拼在一起。
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return ExportResult{}, err
	}
	defer tx.Rollback()
	var replay ExportResult
	if found, err := commandreceipt.Load(ctx, tx, tenantID, actorID, "report.export", examID, nil, &replay); err != nil || found {
		return replay, err
	}
	data, err := s.loadDataset(ctx, tx, tenantID, examID)
	if err != nil {
		return ExportResult{}, err
	}
	overview := buildOverview(data)
	classes := buildClassReports(data)
	questions := buildQuestionAnalysis(data)
	quality, err := s.loadQuality(ctx, tx, tenantID, examID, data)
	if err != nil {
		return ExportResult{}, err
	}
	result := buildExportCSV(tenantID, examID, actorID, overview, classes, questions, quality)
	reportData, _ := json.Marshal(map[string]any{
		"row_count":    result.RowCount,
		"watermark":    result.Watermark,
		"content_type": result.ContentType,
	})
	err = tx.QueryRowContext(ctx, `
INSERT INTO report (tenant_id, exam_id, report_type, scope_type, status, data, generated_by, generated_at)
VALUES ($1, $2::uuid, 'export', 'exam', 'succeeded', $3, $4::uuid, now())
RETURNING id::text
`, tenantID, examID, reportData, actorID).Scan(&result.ReportID)
	if err != nil {
		return ExportResult{}, err
	}
	if err := commandreceipt.Save(ctx, tx, tenantID, actorID, "report.export", examID, nil, result); err != nil {
		return ExportResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ExportResult{}, err
	}
	return result, nil
}

type sqlStateError interface {
	SQLState() string
}

func isUniqueViolation(err error) bool {
	var state sqlStateError
	return errors.As(err, &state) && state.SQLState() == "23505"
}

type reportQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *PostgresStore) loadDataset(ctx context.Context, q reportQueryer, tenantID string, examID string) (dataset, error) {
	data := dataset{ExamID: examID}
	var hasRelease bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM score_release_current WHERE tenant_id=$1::uuid AND exam_id=$2::uuid)`, tenantID, examID).Scan(&hasRelease); err != nil {
		return dataset{}, err
	}
	subQuery := `
SELECT item.submission_id::text, COALESCE(item.student_id::text, ''), COALESCE(st.class_id::text, ''),
  COALESCE(sc.name, ''), COALESCE(NULLIF(sub.candidate_no,''),sub.id::text), item.total_score::float8, item.max_score::float8
FROM score_release_current current_release
JOIN score_release release ON release.tenant_id=current_release.tenant_id AND release.id=current_release.release_id AND release.status='published'
JOIN score_release_item item ON item.tenant_id=release.tenant_id AND item.release_id=release.id
JOIN submission sub ON sub.tenant_id=item.tenant_id AND sub.id=item.submission_id AND sub.deleted_at IS NULL
LEFT JOIN student st ON st.tenant_id = item.tenant_id AND st.id = item.student_id AND st.deleted_at IS NULL
LEFT JOIN school_class sc ON sc.tenant_id = st.tenant_id AND sc.id = st.class_id AND sc.deleted_at IS NULL
WHERE current_release.tenant_id = $1 AND current_release.exam_id::text = $2
ORDER BY sub.candidate_no, sub.id
`
	if !hasRelease {
		// 旧考试通过 submission_grade 发布，没有 score_release 快照；保留已发布分数，
		// 但不能把之后新增的 AI 或人工评语挂到这些已发布事实。
		subQuery = `
SELECT sg.submission_id::text, COALESCE(sg.student_id::text, ''), COALESCE(st.class_id::text, ''),
  COALESCE(sc.name, ''), sg.anonymous_code, sg.total_score::float8, sg.max_score::float8
FROM submission_grade sg
LEFT JOIN student st ON st.tenant_id=sg.tenant_id AND st.id=sg.student_id AND st.deleted_at IS NULL
LEFT JOIN school_class sc ON sc.tenant_id=st.tenant_id AND sc.id=st.class_id AND sc.deleted_at IS NULL
WHERE sg.tenant_id=$1::uuid AND sg.exam_id=$2::uuid AND sg.status='published' AND sg.locked AND sg.deleted_at IS NULL
ORDER BY sg.anonymous_code`
	}
	subRows, err := q.QueryContext(ctx, subQuery, tenantID, examID)
	if err != nil {
		return dataset{}, err
	}
	defer subRows.Close()
	for subRows.Next() {
		var sub submissionRecord
		if err := subRows.Scan(&sub.ID, &sub.StudentID, &sub.ClassID, &sub.ClassName, &sub.AnonymousCode, &sub.TotalScore, &sub.MaxScore); err != nil {
			return dataset{}, err
		}
		data.Submissions = append(data.Submissions, sub)
	}
	if err := subRows.Err(); err != nil {
		return dataset{}, err
	}
	// 对学生展示的反馈取发布快照，并受该次发布的 show_feedback 控制；不能读取后来改写的原始批阅意见。
	gradeQuery := `
SELECT released_question.submission_id::text, COALESCE(item.student_id::text, ''), COALESCE(st.class_id::text, ''),
  COALESCE(sc.name, ''), released_question.question_id::text, released_question.question_no, q.question_type, fg.answer_segment_id::text,
  released_question.score::float8, released_question.max_score::float8, released_question.source_type, q.knowledge_points,
  COALESCE(ans.answer_payload, '{}'::jsonb),
  ag.suggested_score::float8, hg.score::float8,
  CASE WHEN COALESCE((release.visibility_policy->>'show_feedback')::boolean, false)
    THEN NULLIF(BTRIM(released_question.student_explanation->>'feedback'), '') ELSE NULL END
FROM score_release_current current_release
JOIN score_release release ON release.tenant_id=current_release.tenant_id AND release.id=current_release.release_id AND release.status='published'
JOIN score_release_item item ON item.tenant_id=release.tenant_id AND item.release_id=release.id
JOIN score_release_question released_question ON released_question.tenant_id=item.tenant_id AND released_question.release_id=item.release_id AND released_question.submission_id=item.submission_id
JOIN final_grade fg ON fg.tenant_id=released_question.tenant_id AND fg.id=released_question.final_grade_id AND fg.deleted_at IS NULL
LEFT JOIN student st ON st.tenant_id = item.tenant_id AND st.id = item.student_id AND st.deleted_at IS NULL
LEFT JOIN school_class sc ON sc.tenant_id = st.tenant_id AND sc.id = st.class_id AND sc.deleted_at IS NULL
JOIN question q ON q.tenant_id = released_question.tenant_id AND q.id = released_question.question_id AND q.deleted_at IS NULL
LEFT JOIN LATERAL (
  SELECT answer_payload
  FROM answer_segment_answer
  WHERE tenant_id = fg.tenant_id AND answer_segment_id = fg.answer_segment_id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) ans ON true
LEFT JOIN LATERAL (
  SELECT suggested_score
  FROM ai_grade
  WHERE tenant_id = fg.tenant_id AND answer_segment_id = fg.answer_segment_id AND deleted_at IS NULL AND status = 'succeeded'
    AND created_at <= release.published_at
  ORDER BY created_at DESC, id DESC
  LIMIT 1
) ag ON true
LEFT JOIN LATERAL (
  SELECT score
  FROM human_grade
  WHERE tenant_id = released_question.tenant_id AND id = released_question.source_id
    AND released_question.source_type = 'single_review' AND deleted_at IS NULL
  LIMIT 1
) hg ON true
WHERE current_release.tenant_id = $1 AND current_release.exam_id::text = $2
ORDER BY released_question.question_no, released_question.id
`
	if !hasRelease {
		gradeQuery = `
SELECT fg.submission_id::text, COALESCE(sg.student_id::text,''), COALESCE(st.class_id::text,''),
  COALESCE(sc.name,''), fg.question_id::text, fg.question_no, question.question_type, fg.answer_segment_id::text,
  fg.score::float8, fg.max_score::float8, fg.source, question.knowledge_points,
  COALESCE(ans.answer_payload,'{}'::jsonb), ag.suggested_score::float8, hg.score::float8, NULL::text
FROM final_grade fg
JOIN submission_grade sg ON sg.tenant_id=fg.tenant_id AND sg.submission_id=fg.submission_id AND sg.deleted_at IS NULL
LEFT JOIN student st ON st.tenant_id=sg.tenant_id AND st.id=sg.student_id AND st.deleted_at IS NULL
LEFT JOIN school_class sc ON sc.tenant_id=st.tenant_id AND sc.id=st.class_id AND sc.deleted_at IS NULL
JOIN question ON question.tenant_id=fg.tenant_id AND question.id=fg.question_id AND question.deleted_at IS NULL
LEFT JOIN LATERAL (
  SELECT answer_payload FROM answer_segment_answer WHERE tenant_id=fg.tenant_id AND answer_segment_id=fg.answer_segment_id AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1
) ans ON true
LEFT JOIN LATERAL (
  SELECT suggested_score FROM ai_grade WHERE tenant_id=fg.tenant_id AND answer_segment_id=fg.answer_segment_id AND deleted_at IS NULL AND status='succeeded' AND created_at<=sg.published_at ORDER BY created_at DESC,id DESC LIMIT 1
) ag ON true
LEFT JOIN LATERAL (
  SELECT score FROM human_grade WHERE tenant_id=fg.tenant_id AND answer_segment_id=fg.answer_segment_id AND deleted_at IS NULL AND created_at<=sg.published_at ORDER BY created_at DESC,id DESC LIMIT 1
) hg ON true
WHERE fg.tenant_id=$1::uuid AND fg.exam_id=$2::uuid AND fg.deleted_at IS NULL AND fg.status IN ('published','locked')
  AND sg.status='published' AND sg.locked
ORDER BY fg.question_no, fg.created_at DESC`
	}
	rows, err := q.QueryContext(ctx, gradeQuery, tenantID, examID)
	if err != nil {
		return dataset{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var record gradeRecord
		var knowledgeRaw []byte
		var payloadRaw []byte
		var aiScore sql.NullFloat64
		var humanScore sql.NullFloat64
		var publishedFeedback sql.NullString
		if err := rows.Scan(
			&record.SubmissionID,
			&record.StudentID,
			&record.ClassID,
			&record.ClassName,
			&record.QuestionID,
			&record.QuestionNo,
			&record.QuestionType,
			&record.AnswerSegmentID,
			&record.Score,
			&record.MaxScore,
			&record.Source,
			&knowledgeRaw,
			&payloadRaw,
			&aiScore,
			&humanScore,
			&publishedFeedback,
		); err != nil {
			return dataset{}, err
		}
		record.KnowledgePoints = parseStringList(knowledgeRaw)
		record.AnswerPayload = parseJSONMap(payloadRaw)
		if aiScore.Valid {
			value := aiScore.Float64
			record.AIScore = &value
		}
		if humanScore.Valid {
			value := humanScore.Float64
			record.HumanScore = &value
		}
		if publishedFeedback.Valid {
			record.TeacherFeedback = append(record.TeacherFeedback, FeedbackItem{QuestionID: record.QuestionID, QuestionNo: record.QuestionNo, Source: "published_release", Text: strings.TrimSpace(publishedFeedback.String)})
		}
		data.Grades = append(data.Grades, record)
	}
	return data, rows.Err()
}

func (s *PostgresStore) loadQuality(ctx context.Context, q reportQueryer, tenantID string, examID string, data dataset) (GradingQualityReport, error) {
	out := GradingQualityReport{ExamID: examID}
	var err error
	out.TotalSegments, err = scalarCount(ctx, q, `SELECT COUNT(*) FROM answer_segment seg
JOIN submission sub ON sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id AND sub.deleted_at IS NULL
WHERE seg.tenant_id = $1 AND sub.exam_id::text = $2 AND seg.deleted_at IS NULL`, tenantID, examID)
	if err != nil {
		return GradingQualityReport{}, err
	}
	for _, grade := range data.Grades {
		if grade.AIScore != nil {
			out.AIGradeCount++
		}
		if grade.Source == "rule_auto" {
			out.AIAcceptedCount++
		}
		if grade.AIScore != nil && grade.HumanScore != nil {
			out.HumanComparableCount++
			if round2(*grade.AIScore) != round2(*grade.HumanScore) {
				out.HumanModifiedCount++
			}
		}
	}
	out.AIAdoptionRate = metric(out.AIAcceptedCount, out.AIGradeCount, "no_ai_grades")
	out.HumanModificationRate = metric(out.HumanModifiedCount, out.HumanComparableCount, "no_ai_human_comparison")
	out.ArbitrationCount, err = scalarCount(ctx, q, `SELECT COUNT(*) FROM arbitration_task WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL`, tenantID, examID)
	if err != nil {
		return GradingQualityReport{}, err
	}
	out.LowConfidenceReviewCount, err = scalarCount(ctx, q, `SELECT COUNT(*) FROM review_task WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND source IN ('ai_low_confidence', 'ocr_low_confidence')`, tenantID, examID)
	if err != nil {
		return GradingQualityReport{}, err
	}
	out.OCRTaskCount, err = scalarCount(ctx, q, `SELECT COUNT(*) FROM ocr_task ot
JOIN submission sub ON sub.tenant_id = ot.tenant_id AND sub.id = ot.submission_id AND sub.deleted_at IS NULL
WHERE ot.tenant_id = $1 AND sub.exam_id::text = $2 AND ot.deleted_at IS NULL`, tenantID, examID)
	if err != nil {
		return GradingQualityReport{}, err
	}
	out.OCRFailedCount, err = scalarCount(ctx, q, `SELECT COUNT(*) FROM ocr_task ot
JOIN submission sub ON sub.tenant_id = ot.tenant_id AND sub.id = ot.submission_id AND sub.deleted_at IS NULL
WHERE ot.tenant_id = $1 AND sub.exam_id::text = $2 AND ot.deleted_at IS NULL AND ot.status = 'failed'`, tenantID, examID)
	if err != nil {
		return GradingQualityReport{}, err
	}
	out.OCRFailureRate = metric(out.OCRFailedCount, out.OCRTaskCount, "no_ocr_tasks")
	var avgDiff sql.NullFloat64
	var maxDiff sql.NullFloat64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*), AVG(score_difference)::float8, MAX(score_difference)::float8 FROM double_mark_session WHERE tenant_id = $1 AND exam_id::text = $2 AND deleted_at IS NULL AND score_difference IS NOT NULL`, tenantID, examID).Scan(&out.DoubleMarkSessionCount, &avgDiff, &maxDiff); err != nil {
		return GradingQualityReport{}, err
	}
	if out.DoubleMarkSessionCount == 0 {
		out.AverageDoubleMarkDiff = Metric{Available: false, Reason: "no_double_mark_sessions"}
		out.MaxDoubleMarkDiff = Metric{Available: false, Reason: "no_double_mark_sessions"}
	} else {
		out.AverageDoubleMarkDiff = Metric{Available: true, Value: round2(avgDiff.Float64), Denominator: out.DoubleMarkSessionCount}
		out.MaxDoubleMarkDiff = Metric{Available: true, Value: round2(maxDiff.Float64), Denominator: out.DoubleMarkSessionCount}
	}
	return out, nil
}

type countQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scalarCount(ctx context.Context, q countQueryer, query string, args ...any) (int, error) {
	var count int
	if err := q.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return count, nil
}
