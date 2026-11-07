package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// ImportStudentsAtomic 只写学生主档与初始学籍，不刷新或修改任何考试考生快照。
// 班级共享锁防止空班删除穿过校验；已有学生行锁与唯一约束共同保护并发导入、转班和改名。
func (s *PostgresStore) ImportStudentsAtomic(ctx context.Context, tenantID string, input []StudentImportRow) (CSVImportResult, error) {
	result := emptyStudentImportResult()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	classIDs := []string{}
	classSeen := map[string]bool{}
	for _, row := range input {
		if !classSeen[row.Student.ClassID] {
			classSeen[row.Student.ClassID] = true
			classIDs = append(classIDs, row.Student.ClassID)
		}
	}
	classes := map[string]Class{}
	rows, err := tx.QueryContext(ctx, `
SELECT cls.id::text,cls.school_id::text,cls.academic_year_id::text,cls.grade_cohort_id::text
FROM school_class cls
JOIN school school ON school.tenant_id=cls.tenant_id AND school.id=cls.school_id AND school.deleted_at IS NULL
JOIN academic_year year ON year.tenant_id=cls.tenant_id AND year.id=cls.academic_year_id AND year.school_id=cls.school_id
JOIN grade_cohort cohort ON cohort.tenant_id=cls.tenant_id AND cohort.id=cls.grade_cohort_id AND cohort.school_id=cls.school_id
WHERE cls.tenant_id=$1 AND cls.id::text=ANY($2::text[]) AND cls.deleted_at IS NULL
ORDER BY cls.id FOR SHARE OF cls`, tenantID, classIDs)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var class Class
		if err = rows.Scan(&class.ID, &class.SchoolID, &class.AcademicYearID, &class.GradeCohortID); err != nil {
			rows.Close()
			return result, err
		}
		classes[class.ID] = class
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	// 先用真实班级归属核对文件，后续 JSON 中的学校 UUID 才可安全用于精确索引匹配。
	// 不能把调用方传入的学校字符串直接转换成 UUID，也不能让无效行先参与写入。
	result, _ = preflightStudentImport(input, classes, map[string]Student{})
	if len(result.Errors) > 0 {
		return result, nil
	}
	payload, err := studentImportPayload(input)
	if err != nil {
		return result, err
	}
	existing, err := lockImportedStudents(ctx, tx, tenantID, payload)
	if err != nil {
		return result, err
	}
	result, pending := preflightStudentImport(input, classes, existing)
	if len(result.Errors) > 0 {
		return result, nil
	}
	if len(pending) > 0 {
		pendingPayload, encodeErr := studentImportPayload(pending)
		if encodeErr != nil {
			return emptyStudentImportResult(), encodeErr
		}
		var enrollmentCount int
		// 先按自然键排序再插入，两个重叠批次采用相同的唯一键等待顺序。
		// ON CONFLICT 只负责等待并发写入；随后回读比较，绝不把不同姓名或班级当作成功跳过。
		err = tx.QueryRowContext(ctx, `
WITH input AS (
 SELECT * FROM jsonb_to_recordset($2::jsonb) AS i(student_no text,name text,school_id uuid,class_id uuid)
), parents AS (
 SELECT i.*,cls.academic_year_id,cls.grade_cohort_id,cohort.entry_year,year.starts_at
 FROM input i
 JOIN school_class cls ON cls.tenant_id=$1 AND cls.id=i.class_id AND cls.school_id=i.school_id AND cls.deleted_at IS NULL
 JOIN academic_year year ON year.tenant_id=cls.tenant_id AND year.id=cls.academic_year_id AND year.school_id=cls.school_id
 JOIN grade_cohort cohort ON cohort.tenant_id=cls.tenant_id AND cohort.id=cls.grade_cohort_id AND cohort.school_id=cls.school_id
), inserted AS (
 INSERT INTO student(tenant_id,school_id,class_id,student_no,name,status,admission_year)
 SELECT $1,school_id,class_id,student_no,name,'active',entry_year FROM parents ORDER BY school_id,student_no
 ON CONFLICT (tenant_id,school_id,student_no) DO NOTHING
 RETURNING id,tenant_id,school_id,class_id,student_no
), enrolled AS (
 INSERT INTO student_enrollment(tenant_id,school_id,student_id,academic_year_id,grade_cohort_id,class_id,status,start_date)
 SELECT st.tenant_id,st.school_id,st.id,p.academic_year_id,p.grade_cohort_id,p.class_id,'enrolled',p.starts_at
 FROM inserted st JOIN parents p ON p.school_id=st.school_id AND p.class_id=st.class_id AND p.student_no=st.student_no
 RETURNING id
)
SELECT (SELECT count(*) FROM inserted),(SELECT count(*) FROM enrolled)`, tenantID, pendingPayload).Scan(&result.Created, &enrollmentCount)
		if err != nil {
			return emptyStudentImportResult(), err
		}
		if result.Created != enrollmentCount {
			return emptyStudentImportResult(), errors.New("student import enrollment count mismatch")
		}
		// ReadCommitted 在唯一冲突等待结束后读取新快照；如果另一管理员写了不同身份，整批（含此前插入）回滚。
		after, readErr := lockImportedStudents(ctx, tx, tenantID, pendingPayload)
		if readErr != nil {
			return emptyStudentImportResult(), readErr
		}
		for _, row := range pending {
			current, ok := after[studentImportKey(row.Student)]
			if !ok || !sameImportedStudent(current, row.Student) {
				result = emptyStudentImportResult()
				result.Errors = append(result.Errors, CSVImportError{Row: row.Row, Message: "该学号刚被其他操作修改，姓名、班级或在籍状态不一致；本批没有导入，请核对后重试"})
				return result, nil
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return emptyStudentImportResult(), err
	}
	result.Skipped, result.Committed = len(input)-result.Created, true
	return result, nil
}

func studentImportPayload(input []StudentImportRow) ([]byte, error) {
	students := make([]Student, 0, len(input))
	for _, row := range input {
		students = append(students, row.Student)
	}
	return json.Marshal(students)
}

func lockImportedStudents(ctx context.Context, tx *sql.Tx, tenantID string, payload []byte) (map[string]Student, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT st.school_id::text,st.student_no,st.name,st.class_id::text,
 CASE WHEN st.deleted_at IS NULL THEN st.status ELSE 'deleted' END
FROM student st
JOIN (SELECT DISTINCT school_id,student_no FROM jsonb_to_recordset($2::jsonb) AS i(school_id uuid,student_no text)) input
 ON input.school_id=st.school_id AND input.student_no=st.student_no
WHERE st.tenant_id=$1 ORDER BY st.school_id,st.student_no FOR UPDATE OF st`, tenantID, payload)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Student{}
	for rows.Next() {
		var student Student
		if err = rows.Scan(&student.SchoolID, &student.StudentNo, &student.Name, &student.ClassID, &student.Status); err != nil {
			return nil, err
		}
		out[studentImportKey(student)] = student
	}
	return out, rows.Err()
}
