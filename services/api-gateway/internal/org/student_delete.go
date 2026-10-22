package org

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"github.com/jackc/pgx/v5/pgconn"
)

// DeleteStudent 永久删除尚未被业务使用的停用学生；已有关联时保留名册和学籍供恢复使用。
func (h *Handler) DeleteStudent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := h.store.DeleteStudent(r.Context(), mustUser(r).TenantID, id)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			httpx.Error(w, r, http.StatusNotFound, "student_not_found", "student not found")
		case errors.Is(err, ErrStudentMustBeInactive):
			httpx.Error(w, r, http.StatusConflict, "student_must_be_inactive", "disable the student before permanent deletion")
		case errors.Is(err, ErrStudentInUse):
			httpx.Error(w, r, http.StatusConflict, "student_in_use", "student has linked exam, submission, grade or account records")
		default:
			httpx.Error(w, r, http.StatusInternalServerError, "student_delete_failed", "failed to permanently delete student")
		}
		return
	}
	h.auditAction(r, "org.student_deleted", "student", id, "permanently delete unused inactive student")
	httpx.JSON(w, http.StatusOK, map[string]any{"student_id": id, "deleted": true})
}

// DeleteStudent 锁定学生后检查引用；删除学籍与学生原子提交，外键冲突时全部回滚。
func (s *PostgresStore) DeleteStudent(ctx context.Context, tenantID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM student WHERE tenant_id=$1 AND id::text=$2 AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "inactive" {
		return ErrStudentMustBeInactive
	}
	// 历史或软删除的业务记录也属于引用，不能为了移除名册而级联删考试、答卷、成绩。
	// 学生账号作用域使用 JSON 而非外键，需要在数据库外键保护之外单独检查。
	var inUse bool
	err = tx.QueryRowContext(ctx, `SELECT
 EXISTS(SELECT 1 FROM exam_candidate_snapshot WHERE tenant_id=$1 AND student_id::text=$2)
 OR EXISTS(SELECT 1 FROM exam_student_attendance WHERE tenant_id=$1 AND student_id::text=$2)
 OR EXISTS(SELECT 1 FROM submission WHERE tenant_id=$1 AND student_id::text=$2)
 OR EXISTS(SELECT 1 FROM submission_grade WHERE tenant_id=$1 AND student_id::text=$2)
 OR EXISTS(SELECT 1 FROM score_release_item WHERE tenant_id=$1 AND student_id::text=$2)
 OR EXISTS(SELECT 1 FROM answer_sheet_print_sheet WHERE tenant_id=$1 AND student_id::text=$2)
 OR EXISTS(SELECT 1 FROM capture_page WHERE tenant_id=$1 AND barcode_student_id::text=$2)
 OR EXISTS(SELECT 1 FROM appeal WHERE tenant_id=$1 AND student_id::text=$2)
 OR EXISTS(SELECT 1 FROM question_appeal WHERE tenant_id=$1 AND student_id::text=$2)
 OR EXISTS(SELECT 1 FROM user_role WHERE tenant_id=$1 AND deleted_at IS NULL
   AND jsonb_path_exists(data_scope,'$.**.student_id ? (@ == $student_id)',jsonb_build_object('student_id',$2::text)))`, tenantID, id).Scan(&inUse)
	if err != nil {
		return err
	}
	if inUse {
		return ErrStudentInUse
	}
	// 新建名册已有初始学籍；仅清除此学生的学籍，其他业务引用由上面的检查和外键共同阻止。
	if _, err = tx.ExecContext(ctx, `DELETE FROM student_enrollment WHERE tenant_id=$1 AND student_id::text=$2`, tenantID, id); err != nil {
		return studentDeleteConstraintError(err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM student WHERE tenant_id=$1 AND id::text=$2`, tenantID, id); err != nil {
		return studentDeleteConstraintError(err)
	}
	return studentDeleteConstraintError(tx.Commit())
}

func studentDeleteConstraintError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrStudentInUse
	}
	return err
}

// MemoryStore 只持有名册；生产环境的跨模块引用保护由 PostgreSQL 外键及事务执行。
func (s *MemoryStore) DeleteStudent(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	student, ok := s.students[id]
	if !ok || student.TenantID != tenantID {
		return ErrNotFound
	}
	if student.Status != "inactive" {
		return ErrStudentMustBeInactive
	}
	delete(s.students, id)
	return nil
}
