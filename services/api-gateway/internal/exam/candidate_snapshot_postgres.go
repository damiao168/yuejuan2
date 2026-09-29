package exam

import (
	"context"
	"database/sql"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// refreshExamCandidateSnapshot 随考试创建或班级修改重建名册，事务由调用方持有。
// RebuildCandidateSnapshot 会在同一事务内锁定考试并检查阶段，避免新入口遗漏冻结约束。
func (s *PostgresStore) refreshExamCandidateSnapshot(ctx context.Context, tx *sql.Tx, tenantID, examID string) error {
	return RebuildCandidateSnapshot(ctx, tx, tenantID, examID)
}

// RebuildCandidateSnapshot 在调用方事务中锁定考试行并只允许 draft/configured。
// 它与准备确认共享事务，阶段不允许时返回 ErrCandidatesFrozen；快照复制当时的学籍信息。
func RebuildCandidateSnapshot(ctx context.Context, tx *sql.Tx, tenantID, examID string) error {
	var status string
	if err := tx.QueryRowContext(ctx, `
SELECT status
FROM exam
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL
FOR UPDATE
`, tenantID, examID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !CanChangeCandidateRoster(status) {
		return ErrCandidatesFrozen
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM exam_candidate_snapshot WHERE tenant_id=$1 AND exam_id=$2::uuid`, tenantID, examID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO exam_candidate_snapshot (
  tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,
  grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot
)
SELECT DISTINCT
  ec.tenant_id,ec.exam_id,st.id,st.student_no,st.name,
  cls.grade_id,grade.name,cls.id,cls.name
FROM exam_class ec
JOIN school_class cls
  ON cls.tenant_id=ec.tenant_id AND cls.id=ec.class_id AND cls.deleted_at IS NULL
JOIN grade
  ON grade.tenant_id=cls.tenant_id AND grade.id=cls.grade_id AND grade.deleted_at IS NULL
JOIN student_enrollment enrollment
  ON enrollment.tenant_id=ec.tenant_id AND enrollment.class_id=ec.class_id
  AND enrollment.academic_year_id=cls.academic_year_id
  AND enrollment.status='enrolled' AND enrollment.end_date IS NULL AND enrollment.deleted_at IS NULL
JOIN student st
  ON st.tenant_id=enrollment.tenant_id AND st.id=enrollment.student_id
  AND st.status='active' AND st.deleted_at IS NULL
WHERE ec.tenant_id=$1 AND ec.exam_id=$2::uuid AND ec.deleted_at IS NULL
`, tenantID, examID)
	return err
}

func (s *PostgresStore) RefreshCandidateSnapshot(ctx context.Context, scope auth.AccessScope, examID string) (CandidateRefreshResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CandidateRefreshResult{}, err
	}
	defer tx.Rollback()

	current, err := s.getExamForUpdate(ctx, tx, scope, examID)
	if err != nil {
		return CandidateRefreshResult{}, err
	}
	if !CanChangeCandidateRoster(current.Status) {
		return CandidateRefreshResult{}, ErrCandidatesFrozen
	}

	before, err := candidateSnapshotIDs(ctx, tx, scope.TenantID, examID)
	if err != nil {
		return CandidateRefreshResult{}, err
	}
	if err := RebuildCandidateSnapshot(ctx, tx, scope.TenantID, examID); err != nil {
		return CandidateRefreshResult{}, err
	}
	after, err := candidateSnapshotIDs(ctx, tx, scope.TenantID, examID)
	if err != nil {
		return CandidateRefreshResult{}, err
	}

	result := CandidateRefreshResult{BeforeCount: len(before), AfterCount: len(after)}
	for id := range after {
		if _, existed := before[id]; !existed {
			result.AddedCount++
		}
	}
	for id := range before {
		if _, remains := after[id]; !remains {
			result.RemovedCount++
		}
	}
	if err := tx.Commit(); err != nil {
		return CandidateRefreshResult{}, err
	}
	return result, nil
}

func candidateSnapshotIDs(ctx context.Context, tx *sql.Tx, tenantID, examID string) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT student_id::text
FROM exam_candidate_snapshot
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
