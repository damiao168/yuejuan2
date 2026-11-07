package score

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

var errReportScope = errors.New("whole-exam score report requires school management access")

type scopedScoreReportExporter interface {
	ExportExamReport(context.Context, auth.AccessScope, string, string) (ExportResult, error)
}

func validateReportSubjects(scope auth.AccessScope, subjects []reportSubject) error {
	if scope.TenantID == "" || len(subjects) == 0 {
		return errReportScope
	}
	// 单科的分派或可见权限不能扩展成兄弟学科及全班排名；报告限定为整校管理范围。
	for _, subject := range subjects {
		if subject.SchoolID == "" || subject.SchoolID != subjects[0].SchoolID || !scope.ManagesSchool(subject.SchoolID) {
			return errReportScope
		}
	}
	return nil
}

func (s *PostgresStore) ExportExamReport(ctx context.Context, scope auth.AccessScope, examID, actorID string) (ExportResult, error) {
	// 场次、冻结名册、最终题分和总成绩必须来自同一只读快照，不能在多个查询间拼接不同汇总版本。
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ExportResult{}, err
	}
	defer tx.Rollback()
	var sessionID, schoolID string
	data := examReport{}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(e.exam_session_id::text,''),e.school_id::text,
 COALESCE(parent.name,e.name),school.name
FROM exam e JOIN school ON school.tenant_id=e.tenant_id AND school.id=e.school_id
LEFT JOIN exam_session parent ON parent.tenant_id=e.tenant_id AND parent.id=e.exam_session_id AND parent.deleted_at IS NULL
WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid AND e.deleted_at IS NULL
 AND (e.exam_session_id IS NULL OR parent.id IS NOT NULL)`, scope.TenantID, examID).Scan(&sessionID, &schoolID, &data.Name, &data.SchoolName)
	if errors.Is(err, sql.ErrNoRows) {
		return ExportResult{}, ErrNotFound
	}
	if err != nil {
		return ExportResult{}, err
	}
	if !scope.ManagesSchool(schoolID) {
		return ExportResult{}, errReportScope
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,subject,school_id::text FROM exam
WHERE tenant_id=$1::uuid AND deleted_at IS NULL
 AND (($3<>'' AND exam_session_id::text=$3) OR ($3='' AND id=$2::uuid))
ORDER BY subject,id`, scope.TenantID, examID, sessionID)
	if err != nil {
		return ExportResult{}, err
	}
	for rows.Next() {
		var subject reportSubject
		if err = rows.Scan(&subject.ID, &subject.Code, &subject.SchoolID); err != nil {
			rows.Close()
			return ExportResult{}, err
		}
		data.Subjects = append(data.Subjects, subject)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return ExportResult{}, err
	}
	rows.Close()
	if err = validateReportSubjects(scope, data.Subjects); err != nil {
		return ExportResult{}, err
	}
	ids := make([]string, len(data.Subjects))
	indices := map[string]int{}
	for i, subject := range data.Subjects {
		ids[i] = subject.ID
		indices[subject.ID] = i
	}
	rows, err = tx.QueryContext(ctx, examReportEntriesSQL, scope.TenantID, strings.Join(ids, ","))
	if err != nil {
		return ExportResult{}, err
	}
	for rows.Next() {
		var entry reportEntry
		var subjectID, state string
		var total, maximum sql.NullFloat64
		var valid bool
		if err = rows.Scan(&subjectID, &entry.StudentID, &entry.StudentNo, &entry.Name, &entry.Cohort, &entry.GradeID, &entry.GradeName, &entry.ClassID, &entry.ClassName, &state, &total, &maximum, &valid); err != nil {
			rows.Close()
			return ExportResult{}, err
		}
		entry.Status = state
		if valid && total.Valid && maximum.Valid {
			entry.Score = &total.Float64
			entry.Max = &maximum.Float64
		}
		index, ok := indices[subjectID]
		if !ok {
			rows.Close()
			return ExportResult{}, ErrInvalidInput
		}
		data.Subjects[index].Entries = append(data.Subjects[index].Entries, entry)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return ExportResult{}, err
	}
	rows.Close()
	result, err := buildExamReportCSV(data, scope.TenantID, examID, actorID, time.Now().UTC())
	if err != nil {
		return ExportResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return ExportResult{}, err
	}
	return result, nil
}

// 覆盖范围使用准备时的冻结题集；缺页、身份冲突、重复答卷、未完成评分或过期总分都保留状态而不提供统计分数。
const examReportEntriesSQL = `WITH latest_final AS (
 SELECT DISTINCT ON (fg.answer_segment_id) fg.* FROM final_grade fg
 WHERE fg.tenant_id=$1::uuid AND fg.exam_id::text=ANY(string_to_array($2,',')) AND fg.deleted_at IS NULL
 ORDER BY fg.answer_segment_id,fg.created_at DESC,fg.id DESC
), totals AS (
 SELECT submission_id,SUM(score) score,SUM(max_score) max_score FROM latest_final GROUP BY submission_id
), candidates AS (
 SELECT c.exam_id::text AS subject_id,c.student_id::text,c.student_no_snapshot,c.student_name_snapshot,
  COALESCE(cohort.entry_year::text||'级','') AS cohort,c.grade_id_snapshot::text,c.grade_name_snapshot,c.class_id_snapshot::text,c.class_name_snapshot,
  CASE WHEN COALESCE(sub.submission_count,0)=0 THEN '缺考'
   WHEN sub.submission_count<>1 OR sub.identity_status IN ('unknown','conflict') THEN '待匹配'
   WHEN sub.actual_page_count<sub.expected_page_count OR sub.quality_status='failed' OR sub.status='rejected' THEN '缺页或答卷异常'
   WHEN sg.id IS NULL OR sg.status='calculating' THEN '待阅卷或待汇总'
   WHEN NOT COALESCE(complete.valid,false) THEN '待完成评分或重新汇总'
   WHEN sg.status='pending_confirmation' THEN '待确认'
   WHEN sg.status='confirmed' THEN '已确认' WHEN sg.status='pending_publish' THEN '待发布'
   WHEN sg.status='published' THEN '已发布' WHEN sg.status='locked' THEN '已锁定' ELSE '待核对' END AS score_state,
  sg.total_score::float8,sg.max_score::float8,
  COALESCE(sub.submission_count=1 AND sub.identity_status NOT IN ('unknown','conflict')
   AND sub.actual_page_count>=sub.expected_page_count AND sub.quality_status<>'failed' AND sub.status<>'rejected'
   AND sg.id IS NOT NULL AND sg.status IN ('pending_confirmation','confirmed','pending_publish','published','locked')
   AND complete.valid,false) AS valid
 FROM exam_candidate_snapshot c
 JOIN exam e ON e.tenant_id=c.tenant_id AND e.id=c.exam_id AND e.deleted_at IS NULL
 LEFT JOIN grade g ON g.tenant_id=c.tenant_id AND g.id=c.grade_id_snapshot
 LEFT JOIN grade_cohort cohort ON cohort.tenant_id=g.tenant_id AND cohort.id=g.grade_cohort_id
 LEFT JOIN LATERAL (
  SELECT item.*,COUNT(*) OVER () AS submission_count FROM submission item
  WHERE item.tenant_id=c.tenant_id AND item.exam_id=c.exam_id AND item.student_id=c.student_id AND item.deleted_at IS NULL
  ORDER BY item.created_at DESC,item.id LIMIT 1
 ) sub ON true
 LEFT JOIN submission_grade sg ON sg.tenant_id=c.tenant_id AND sg.exam_id=c.exam_id AND sg.submission_id=sub.id AND sg.deleted_at IS NULL
 LEFT JOIN totals ON totals.submission_id=sub.id
 LEFT JOIN LATERAL (
  SELECT totals.score=sg.total_score AND totals.max_score=sg.max_score AND sg.max_score=e.total_score
   AND EXISTS(SELECT 1 FROM exam_question_snapshot q WHERE q.tenant_id=c.tenant_id AND q.exam_id=c.exam_id)
   AND NOT EXISTS(SELECT 1 FROM exam_question_snapshot q WHERE q.tenant_id=c.tenant_id AND q.exam_id=c.exam_id AND NOT EXISTS(
    SELECT 1 FROM answer_segment seg JOIN latest_final final ON final.answer_segment_id=seg.id AND final.tenant_id=seg.tenant_id
    WHERE seg.tenant_id=c.tenant_id AND seg.submission_id=sub.id AND seg.question_id=q.question_id AND seg.deleted_at IS NULL))
   AND NOT EXISTS(SELECT 1 FROM answer_segment seg WHERE seg.tenant_id=c.tenant_id AND seg.submission_id=sub.id AND seg.deleted_at IS NULL
    AND NOT EXISTS(SELECT 1 FROM latest_final final WHERE final.tenant_id=seg.tenant_id AND final.answer_segment_id=seg.id))
   AND NOT EXISTS(SELECT 1 FROM review_task task WHERE task.tenant_id=c.tenant_id AND task.submission_id=sub.id AND task.deleted_at IS NULL AND task.status NOT IN ('submitted','completed','cancelled'))
   AND NOT EXISTS(SELECT 1 FROM arbitration_task task WHERE task.tenant_id=c.tenant_id AND task.submission_id=sub.id AND task.deleted_at IS NULL AND task.status NOT IN ('submitted','cancelled')) AS valid
 ) complete ON true
 WHERE c.tenant_id=$1::uuid AND c.exam_id::text=ANY(string_to_array($2,','))
)
SELECT * FROM candidates ORDER BY subject_id,class_name_snapshot,student_no_snapshot,student_id`

func (s *MemoryStore) ExportExamReport(_ context.Context, scope auth.AccessScope, examID, actorID string) (ExportResult, error) {
	if scope.TenantID == "" || !scope.ManagesSchool("") {
		return ExportResult{}, errReportScope
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	subject := reportSubject{ID: examID, Code: "本学科"}
	for _, item := range s.rosterReportLocked(examID).Entries {
		if !strings.HasPrefix(item.Key, "student:") {
			continue
		}
		entry := reportEntry{StudentID: item.StudentID, StudentNo: item.StudentNo, Name: item.StudentName, ClassID: item.ClassID, ClassName: item.ClassName, GradeID: "memory-grade", Status: map[string]string{"graded": "已评分", "submitted": "待阅卷", "absent": "缺考", "unmatched": "待匹配", "missing_pages": "缺页"}[item.Status]}
		if item.Status == "graded" {
			entry.Score = item.TotalScore
			entry.Max = item.MaxScore
		}
		subject.Entries = append(subject.Entries, entry)
	}
	return buildExamReportCSV(examReport{Name: examID, Subjects: []reportSubject{subject}}, scope.TenantID, examID, actorID, time.Now().UTC())
}
