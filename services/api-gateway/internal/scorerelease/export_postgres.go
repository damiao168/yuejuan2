package scorerelease

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

// ExportCurrentPublishedCSV 只接受整校成绩管理范围，按父场次读取全部学科。
// 同步导出最多20科、20万条应考科目记录，超过范围拒绝，不截断成看似完整的成绩单。
func (s *PostgresStore) ExportCurrentPublishedCSV(ctx context.Context, scope auth.AccessScope, examID string, now time.Time) (formalExportResult, error) {
	if _, err := uuid.Parse(examID); err != nil {
		return formalExportResult{}, ErrInvalidInput
	}
	if scope.TenantID == "" || scope.ActorID == "" {
		return formalExportResult{}, ErrForbidden
	}
	// 同一数据库快照固定所有学科的 current 指针，避免多科导出在一次发布切换中拼出不同读取时点。
	// 后续所有分数只读取固定 release_id 的不可变条目，不读取 submission_grade 或 final_grade。
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return formalExportResult{}, err
	}
	defer tx.Rollback()
	var session, schoolID, name, school string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(e.exam_session_id::text,''),e.school_id::text,COALESCE(parent.name,e.name),school.name
FROM exam e JOIN school ON school.tenant_id=e.tenant_id AND school.id=e.school_id
LEFT JOIN exam_session parent ON parent.tenant_id=e.tenant_id AND parent.id=e.exam_session_id AND parent.deleted_at IS NULL
WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid AND e.deleted_at IS NULL
AND (e.exam_session_id IS NULL OR parent.id IS NOT NULL)`, scope.TenantID, examID).Scan(&session, &schoolID, &name, &school)
	if errors.Is(err, sql.ErrNoRows) {
		return formalExportResult{}, ErrNotFound
	}
	if err != nil {
		return formalExportResult{}, err
	}
	if !scope.ManagesSchool(schoolID) {
		return formalExportResult{}, ErrForbidden
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.id::text,e.subject,e.school_id::text,e.status,COALESCE(r.id::text,''),COALESCE(r.version,0),COALESCE(r.visibility_policy,'{}'::jsonb)
FROM exam e LEFT JOIN score_release_current current ON current.tenant_id=e.tenant_id AND current.exam_id=e.id
LEFT JOIN score_release r ON r.tenant_id=current.tenant_id AND r.exam_id=e.id AND r.id=current.release_id AND r.status='published'
WHERE e.tenant_id=$1::uuid AND e.deleted_at IS NULL
AND (($3<>'' AND e.exam_session_id::text=$3) OR ($3='' AND e.id=$2::uuid)) ORDER BY e.subject,e.id LIMIT 21`, scope.TenantID, examID, session)
	if err != nil {
		return formalExportResult{}, err
	}
	subjects := []formalExportSubject{}
	indices := map[string]int{}
	ids := []string{}
	for rows.Next() {
		var subject formalExportSubject
		var subjectSchool, status string
		var policy []byte
		if err = rows.Scan(&subject.ExamID, &subject.Subject, &subjectSchool, &status, &subject.ReleaseID, &subject.Version, &policy); err != nil {
			rows.Close()
			return formalExportResult{}, err
		}
		if subjectSchool != schoolID || !scope.ManagesSchool(subjectSchool) {
			rows.Close()
			return formalExportResult{}, ErrForbidden
		}
		if subject.ReleaseID == "" || status == "draft" || status == "configured" {
			rows.Close()
			return formalExportResult{}, errFormalExportIncomplete
		}
		if err = json.Unmarshal(policy, &subject.Policy); err != nil {
			rows.Close()
			return formalExportResult{}, err
		}
		indices[subject.ExamID] = len(subjects)
		ids = append(ids, subject.ExamID)
		subjects = append(subjects, subject)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return formalExportResult{}, err
	}
	rows.Close()
	if len(subjects) == 0 || len(subjects) > 20 {
		return formalExportResult{}, errFormalExportIncomplete
	}
	// 条目身份必须与冻结名册一致。旧版没有名册、重复学生或发布条目游离于名册之外时拒绝“完整成绩单”。
	var outside bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM score_release_current current
JOIN score_release_item item ON item.tenant_id=current.tenant_id AND item.release_id=current.release_id
WHERE current.tenant_id=$1::uuid AND current.exam_id::text=ANY(string_to_array($2,','))
AND (item.student_id IS NULL OR NOT EXISTS(SELECT 1 FROM exam_candidate_snapshot c WHERE c.tenant_id=current.tenant_id AND c.exam_id=current.exam_id AND c.student_id=item.student_id)))`, scope.TenantID, strings.Join(ids, ",")).Scan(&outside)
	if err != nil {
		return formalExportResult{}, err
	}
	if outside {
		return formalExportResult{}, errFormalExportIncomplete
	}
	rows, err = tx.QueryContext(ctx, `SELECT c.exam_id::text,c.student_id::text,c.student_no_snapshot,c.student_name_snapshot,
COALESCE(c.grade_id_snapshot::text,''),COALESCE(c.grade_name_snapshot,''),COALESCE(c.class_id_snapshot::text,''),COALESCE(c.class_name_snapshot,''),
item.total_score::float8,item.max_score::float8
FROM exam_candidate_snapshot c
JOIN score_release_current current ON current.tenant_id=c.tenant_id AND current.exam_id=c.exam_id
JOIN score_release r ON r.tenant_id=current.tenant_id AND r.exam_id=c.exam_id AND r.id=current.release_id AND r.status='published'
LEFT JOIN score_release_item item ON item.tenant_id=c.tenant_id AND item.release_id=r.id AND item.student_id=c.student_id
WHERE c.tenant_id=$1::uuid AND c.exam_id::text=ANY(string_to_array($2,',')) ORDER BY c.exam_id,c.student_id LIMIT 200001`, scope.TenantID, strings.Join(ids, ","))
	if err != nil {
		return formalExportResult{}, err
	}
	count := 0
	for rows.Next() {
		var entry formalExportEntry
		var subjectID string
		var score, maximum sql.NullFloat64
		if err = rows.Scan(&subjectID, &entry.StudentID, &entry.StudentNo, &entry.Name, &entry.GradeID, &entry.GradeName, &entry.ClassID, &entry.ClassName, &score, &maximum); err != nil {
			rows.Close()
			return formalExportResult{}, err
		}
		if score.Valid {
			entry.Score = &score.Float64
		}
		if maximum.Valid {
			entry.Max = &maximum.Float64
		}
		index, exists := indices[subjectID]
		if !exists {
			rows.Close()
			return formalExportResult{}, errFormalExportIncomplete
		}
		subjects[index].Entries = append(subjects[index].Entries, entry)
		count++
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return formalExportResult{}, err
	}
	rows.Close()
	if count > 200000 {
		return formalExportResult{}, errFormalExportIncomplete
	}
	for _, subject := range subjects {
		if len(subject.Entries) == 0 {
			return formalExportResult{}, errFormalExportIncomplete
		}
	}
	result, err := buildFormalCSV(name, school, scope.ActorID, subjects, now)
	if err != nil {
		return formalExportResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return formalExportResult{}, err
	}
	return result, nil
}
