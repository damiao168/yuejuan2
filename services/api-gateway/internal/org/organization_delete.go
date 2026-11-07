package org

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func (h *Handler) DeleteGrade(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteGrade(r.Context(), mustUser(r).TenantID, id); err != nil {
		writeOrganizationDeleteError(w, r, err, "grade")
		return
	}
	h.auditAction(r, "org.grade_deleted", "grade", id, "soft delete empty grade cohort annual records")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) DeleteClass(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteClass(r.Context(), mustUser(r).TenantID, id); err != nil {
		writeOrganizationDeleteError(w, r, err, "class")
		return
	}
	h.auditAction(r, "org.class_deleted", "school_class", id, "soft delete empty class")
	w.WriteHeader(http.StatusNoContent)
}

func writeOrganizationDeleteError(w http.ResponseWriter, r *http.Request, err error, resource string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, resource+"_not_found", "所选年级或班级不存在，可能已被删除")
	case errors.Is(err, ErrGradeHasClasses):
		httpx.Error(w, r, http.StatusConflict, "grade_has_classes", "该届别下仍有班级，请先删除空班级或调整班级后再删除年级")
	case errors.Is(err, ErrClassHasStudents):
		httpx.Error(w, r, http.StatusConflict, "class_has_students", "班级下仍有学生（含停用学生），请先调整学生归属后再删除班级")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, resource+"_delete_failed", "暂时无法删除，请稍后重试")
	}
}

// DeleteGrade 删除同届别的空年级记录；固定届别与历史学年仍保留，历史快照不重写。
func (s *PostgresStore) DeleteGrade(ctx context.Context, tenantID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cohortID string
	var schoolID string
	err = tx.QueryRowContext(ctx, `SELECT grade_cohort_id::text,school_id::text FROM grade WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL`, tenantID, id).Scan(&cohortID, &schoolID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = lockGradeSchool(ctx, tx, tenantID, schoolID); err != nil {
		return err
	}
	// 年级写入统一先锁学校再锁cohort；新建班级只需按cohort→grade顺序加锁。
	// 等待中的创建必须重新检查父记录，不能在“无子对象”检查与删除之间插入。
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM grade_cohort WHERE tenant_id=$1 AND id::text=$2 FOR UPDATE`, tenantID, cohortID).Scan(&cohortID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM grade WHERE tenant_id=$1 AND grade_cohort_id::text=$2 AND deleted_at IS NULL ORDER BY id FOR UPDATE`, tenantID, cohortID)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var gradeID string
		if err = rows.Scan(&gradeID); err != nil {
			rows.Close()
			return err
		}
		found = found || gradeID == id
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !found {
		return ErrNotFound
	}
	var hasClasses bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM school_class cls JOIN grade g ON g.tenant_id=cls.tenant_id AND g.id=cls.grade_id WHERE cls.tenant_id=$1 AND g.grade_cohort_id::text=$2 AND cls.deleted_at IS NULL)`, tenantID, cohortID).Scan(&hasClasses)
	if err != nil {
		return err
	}
	if hasClasses {
		return ErrGradeHasClasses
	}
	if _, err = tx.ExecContext(ctx, `UPDATE grade SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND grade_cohort_id::text=$2 AND deleted_at IS NULL`, tenantID, cohortID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteClass 仅软删没有当前学生的班级；停用不是转出，历史已结束学籍则继续保留。
func (s *PostgresStore) DeleteClass(ctx context.Context, tenantID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var classID string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM school_class WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&classID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// 单个创建、CSV 批量导入和转入学籍持本班共享锁，删除检查在获得排他锁后的新快照执行。
	// 同时检查旧 class_id 和在读学籍，兼容缓存未同步的旧资料；不按学生启用状态过滤。
	var hasStudents bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM student st WHERE st.tenant_id=$1 AND st.deleted_at IS NULL AND (st.class_id::text=$2 OR EXISTS(SELECT 1 FROM student_enrollment enrollment WHERE enrollment.tenant_id=st.tenant_id AND enrollment.student_id=st.id AND enrollment.class_id::text=$2 AND enrollment.status='enrolled' AND enrollment.end_date IS NULL AND enrollment.deleted_at IS NULL)))`, tenantID, id).Scan(&hasStudents)
	if err != nil {
		return err
	}
	if hasStudents {
		return ErrClassHasStudents
	}
	if _, err = tx.ExecContext(ctx, `UPDATE school_class SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND id::text=$2`, tenantID, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *MemoryStore) DeleteGrade(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.grades[id]
	if !ok || item.TenantID != tenantID || s.deletedGrades[id] {
		return ErrNotFound
	}
	group := map[string]bool{}
	for gradeID, grade := range s.grades {
		if grade.TenantID == tenantID && grade.GradeCohortID == item.GradeCohortID {
			group[gradeID] = true
		}
	}
	for classID, cls := range s.classes {
		if cls.TenantID == tenantID && group[cls.GradeID] && !s.deletedClasses[classID] {
			return ErrGradeHasClasses
		}
	}
	for gradeID := range group {
		s.deletedGrades[gradeID] = true
	}
	return nil
}

func (s *MemoryStore) DeleteClass(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.classes[id]
	if !ok || item.TenantID != tenantID || s.deletedClasses[id] {
		return ErrNotFound
	}
	for _, student := range s.students {
		if student.TenantID == tenantID && student.ClassID == id {
			return ErrClassHasStudents
		}
	}
	s.deletedClasses[id] = true
	return nil
}
