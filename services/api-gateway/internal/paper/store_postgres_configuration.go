package paper

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

func (s *PostgresStore) Readiness(ctx context.Context, tenantID string, examID string) (ReadinessResult, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ReadinessResult{}, err
	}
	defer tx.Rollback()
	result, status, _, err := calculateReadiness(ctx, tx, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	result, err = loadReadinessConfirmationFacts(ctx, tx, tenantID, examID, status, result)
	if err != nil {
		return ReadinessResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return ReadinessResult{}, err
	}
	return result, nil
}

func loadReadinessConfirmationFacts(ctx context.Context, queryer postgresQueryer, tenantID, examID, status string, result ReadinessResult) (ReadinessResult, error) {
	var confirmedAt time.Time
	var confirmedBy string
	var snapshotID string
	var importSnapshotAvailable bool
	err := queryer.QueryRowContext(ctx, `
SELECT confirmed_at, confirmed_by::text, id::text, import_snapshot_json IS NOT NULL
FROM exam_readiness_snapshot
WHERE tenant_id = $1::uuid AND exam_id = $2::uuid AND status = 'passed' AND configuration_hash = $3
ORDER BY confirmed_at DESC LIMIT 1
`, tenantID, examID, result.ConfigurationHash).Scan(&confirmedAt, &confirmedBy, &snapshotID, &importSnapshotAvailable)
	if err == nil {
		result.Confirmed, result.ConfirmedAt, result.ConfirmedBy = status == "ready" || status == "collecting", &confirmedAt, confirmedBy
		result.SnapshotID, result.ImportSnapshotAvailable = snapshotID, importSnapshotAvailable
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ReadinessResult{}, err
	}
	// 下游阶段保留准备完成的历史事实；它不覆盖上面的当前配置匹配，也不能用于导入或再次确认。
	switch status {
	case "collecting", "grading", "reviewing", "finalized", "archived":
		var preparationConfirmedAt time.Time
		var preparationSnapshotID string
		err = queryer.QueryRowContext(ctx, `
SELECT confirmed_at, id::text FROM exam_readiness_snapshot
WHERE tenant_id = $1::uuid AND exam_id = $2::uuid AND status = 'passed'
ORDER BY confirmed_at DESC, id DESC LIMIT 1
`, tenantID, examID).Scan(&preparationConfirmedAt, &preparationSnapshotID)
		if err == nil {
			result.PreparationConfirmedAt, result.PreparationSnapshotID = &preparationConfirmedAt, preparationSnapshotID
		} else if !errors.Is(err, sql.ErrNoRows) {
			return ReadinessResult{}, err
		}
	}
	return result, nil
}

func (s *PostgresStore) ConfirmReadiness(ctx context.Context, tenantID string, examID string, userID string, input ConfirmReadinessInput) (ReadinessResult, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ReadinessResult{}, err
	}
	defer tx.Rollback()
	lockedStatus, err := lockReadinessExam(ctx, tx, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	if err := lockObjectiveConfirmationRulesTx(ctx, tx, tenantID, examID); err != nil {
		return ReadinessResult{}, err
	}
	// 全部试卷配置写入都取得同一考试行锁。
	// 必须持锁后才读取全部准备度输入，使语义快照、考生冻结和状态转换描述同一个已提交版本。
	result, status, candidateIDs, err := calculateReadiness(ctx, tx, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	// 锁内重新计算的内容必须与管理员核对版本一致；冲突在冻结名册和写快照前返回，事务回滚。
	if input.ExpectedConfigurationHash != "" && input.ExpectedConfigurationHash != result.ConfigurationHash {
		return result, ErrReadinessChanged
	}
	if !result.Ready || lockedStatus != status {
		return result, ErrNotReady
	}
	if lockedStatus == "draft" || lockedStatus == "configured" {
		if err := exam.RebuildCandidateSnapshot(ctx, tx, tenantID, examID); err != nil {
			return ReadinessResult{}, err
		}
		frozenCandidateClasses, err := loadReadinessCandidateClasses(ctx, tx, tenantID, examID, "ready")
		if err != nil {
			return ReadinessResult{}, err
		}
		// 组织侧转班没有考试锁；摘要计算到重建之间的学生或班级变化必须回滚，不能冻结未核对归属。
		if !slices.Equal(readinessCandidateIDs(frozenCandidateClasses), candidateIDs) || !slices.Equal(frozenCandidateClasses, result.candidateClasses) {
			return result, ErrReadinessChanged
		}
	}
	checks, _ := json.Marshal(result.Checks)
	questions, err := listQuestions(ctx, tx, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	// 发布与准备确认共享事务；任何草稿冲突或后续冻结失败都回滚全部新规则。
	if hasObjectiveRuleAutoPreview(result) {
		if err := publishObjectiveConfirmationRulesTx(ctx, tx, tenantID, examID, userID, status, questions); err != nil {
			return result, err
		}
	}
	if err := lockReadinessReferenceMaterials(ctx, tx, tenantID, examID, questions); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE exam_readiness_snapshot SET status = 'invalidated', invalidated_at = now(), invalidation_reason = 'superseded by new confirmation'
WHERE tenant_id = $1::uuid AND exam_id = $2::uuid AND status = 'passed'
`, tenantID, examID); err != nil {
		return ReadinessResult{}, err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE exam SET status = 'ready', updated_at = now() WHERE tenant_id = $1::uuid AND id = $2::uuid AND status IN ('draft', 'configured', 'ready')`, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	count, _ := updated.RowsAffected()
	if count != 1 {
		return result, ErrNotReady
	}
	// 模板更新不取得考试锁；转入 ready 后必须核对触发器真正冻结的评估事实，变化则整笔回滚。
	frozenAssessmentFacts, err := loadReadinessAssessmentFacts(ctx, tx, tenantID, examID, "ready")
	if err != nil {
		return ReadinessResult{}, err
	}
	if readinessAssessmentFactsHash(frozenAssessmentFacts) != result.assessmentFactsHash {
		return result, ErrReadinessChanged
	}
	// 转入 ready 状态时，数据库触发器冻结评估事实。提交前将这些准确身份绑定到内容伴随快照中。
	importSnapshot := newReadinessImportSnapshot(result.ConfigurationHash, questions)
	if err := bindReadinessAssessmentSnapshots(ctx, tx, tenantID, examID, &importSnapshot); err != nil {
		return ReadinessResult{}, err
	}
	importSnapshotJSON, err := json.Marshal(importSnapshot)
	if err != nil {
		return ReadinessResult{}, err
	}
	importSnapshotHash := stableContentHash(importSnapshot)
	now := time.Now().UTC()
	var snapshotID string
	if err := tx.QueryRowContext(ctx, `
	INSERT INTO exam_readiness_snapshot (
	  tenant_id, exam_id, configuration_hash, status, checks, confirmed_by, confirmed_at,
	  import_snapshot_schema_version, import_snapshot_hash, import_snapshot_json
	)
	VALUES ($1::uuid, $2::uuid, $3, 'passed', $4::jsonb, $5::uuid, $6, $9, $7, $8::jsonb)
	RETURNING confirmed_at,id::text
	`, tenantID, examID, result.ConfigurationHash, checks, userID, now, importSnapshotHash, importSnapshotJSON, importSnapshot.Version).Scan(&now, &snapshotID); err != nil {
		return ReadinessResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReadinessResult{}, err
	}
	result.Confirmed, result.ConfirmedAt, result.ConfirmedBy = true, &now, userID
	result.SnapshotID, result.ImportSnapshotAvailable = snapshotID, true
	return result, nil
}

// 调用方持有事务；先取得考试互斥锁，再锁定考试行，后续确认步骤不得提前读取配置。
func lockReadinessExam(ctx context.Context, tx *sql.Tx, tenantID, examID string) (string, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, examID); err != nil {
		return "", err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, examID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	if status != "draft" && status != "configured" && status != "ready" {
		return "", ErrNotReady
	}
	return status, nil
}

func bindReadinessAssessmentSnapshots(ctx context.Context, tx *sql.Tx, tenantID, examID string, snapshot *readinessImportSnapshot) error {
	for index := range snapshot.Questions {
		question := &snapshot.Questions[index]
		if err := tx.QueryRowContext(ctx, `SELECT id::text,content_hash FROM exam_question_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid ORDER BY snapshot_version DESC LIMIT 1`, tenantID, examID, question.ID).Scan(&question.AssessmentSnapshotID, &question.AssessmentSnapshotHash); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) StartCollection(ctx context.Context, tenantID string, examID string, _ string) (ReadinessResult, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ReadinessResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, examID); err != nil {
		return ReadinessResult{}, err
	}
	var lockedStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, examID).Scan(&lockedStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReadinessResult{}, ErrNotFound
		}
		return ReadinessResult{}, err
	}
	if lockedStatus != "ready" {
		return ReadinessResult{}, ErrNotReady
	}
	result, status, _, err := calculateReadiness(ctx, tx, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	if !result.Ready || status != "ready" {
		return result, ErrNotReady
	}
	if err := lockReadinessReferenceMaterials(ctx, tx, tenantID, examID, result.referenceQuestions); err != nil {
		return result, err
	}
	var confirmedAt time.Time
	var confirmedBy string
	var snapshotID string
	var importSnapshotAvailable bool
	if err := tx.QueryRowContext(ctx, `
SELECT confirmed_at, confirmed_by::text, id::text, import_snapshot_json IS NOT NULL FROM exam_readiness_snapshot
WHERE tenant_id = $1::uuid AND exam_id = $2::uuid AND status = 'passed' AND configuration_hash = $3
ORDER BY confirmed_at DESC LIMIT 1
`, tenantID, examID, result.ConfigurationHash).Scan(&confirmedAt, &confirmedBy, &snapshotID, &importSnapshotAvailable); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, ErrNotReady
		}
		return ReadinessResult{}, err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE exam SET status = 'collecting', updated_at = now() WHERE tenant_id = $1::uuid AND id = $2::uuid AND status = 'ready'`, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	count, _ := updated.RowsAffected()
	if count != 1 {
		return result, ErrNotReady
	}
	if err := tx.Commit(); err != nil {
		return ReadinessResult{}, err
	}
	result.Confirmed, result.ConfirmedAt, result.ConfirmedBy = true, &confirmedAt, confirmedBy
	result.SnapshotID, result.ImportSnapshotAvailable = snapshotID, importSnapshotAvailable
	result.PreparationConfirmedAt, result.PreparationSnapshotID = &confirmedAt, snapshotID
	return result, nil
}

// 准备阶段使用当前在读名册，后续阶段使用冻结的考试考生快照；摘要包含具体人员身份。
func calculateReadiness(ctx context.Context, queryer postgresQueryer, tenantID string, examID string) (ReadinessResult, string, []string, error) {
	var total float64
	var status string
	var classIDsJSON, candidateIDsJSON []byte
	err := queryer.QueryRowContext(ctx, `
SELECT e.total_score::float8, e.status,
       COALESCE((
         SELECT jsonb_agg(scope.class_id ORDER BY scope.class_id)
         FROM (
           SELECT DISTINCT ec.class_id::text AS class_id
           FROM exam_class ec
           WHERE ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL
         ) scope
       ), '[]'::jsonb),
       CASE WHEN e.status IN ('draft','configured') THEN
         COALESCE((
           SELECT jsonb_agg(scope.student_id ORDER BY scope.student_id)
           FROM (
             SELECT DISTINCT st.id::text AS student_id
             FROM exam_class ec
             JOIN school_class cls ON cls.tenant_id=ec.tenant_id AND cls.id=ec.class_id AND cls.deleted_at IS NULL
             JOIN student_enrollment enrollment ON enrollment.tenant_id=ec.tenant_id AND enrollment.class_id=ec.class_id AND enrollment.academic_year_id=cls.academic_year_id AND enrollment.status='enrolled' AND enrollment.end_date IS NULL AND enrollment.deleted_at IS NULL
             JOIN student st ON st.tenant_id=enrollment.tenant_id AND st.id=enrollment.student_id AND st.status='active' AND st.deleted_at IS NULL
             WHERE ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL
           ) scope
         ), '[]'::jsonb)
       ELSE
         COALESCE((
           SELECT jsonb_agg(candidate.student_id::text ORDER BY candidate.student_id::text)
           FROM exam_candidate_snapshot candidate
           WHERE candidate.tenant_id=e.tenant_id AND candidate.exam_id=e.id
         ), '[]'::jsonb)
       END
FROM exam e
WHERE e.tenant_id = $1::uuid AND e.id = $2::uuid AND e.deleted_at IS NULL
`, tenantID, examID).Scan(&total, &status, &classIDsJSON, &candidateIDsJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReadinessResult{}, "", nil, ErrNotFound
		}
		return ReadinessResult{}, "", nil, err
	}
	var classIDs, candidateIDs []string
	if err := json.Unmarshal(classIDsJSON, &classIDs); err != nil {
		return ReadinessResult{}, "", nil, err
	}
	if err := json.Unmarshal(candidateIDsJSON, &candidateIDs); err != nil {
		return ReadinessResult{}, "", nil, err
	}
	papers, err := listPapers(ctx, queryer, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, "", nil, err
	}
	questions, err := listQuestions(ctx, queryer, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, "", nil, err
	}
	templates, err := listTemplates(ctx, queryer, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, "", nil, err
	}
	result := buildReadinessForScope(total, classIDs, candidateIDs, papers, questions, templates)
	result, err = withReferenceReadiness(ctx, queryer, tenantID, examID, questions, result)
	if err != nil {
		return ReadinessResult{}, "", nil, err
	}
	var latest PaperImportJob
	err = queryer.QueryRowContext(ctx, `SELECT id::text,current_generation,status,COALESCE(structure_confirmed_generation,0) FROM paper_import_job WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL ORDER BY created_at DESC,id DESC LIMIT 1`, tenantID, examID).Scan(&latest.ID, &latest.Generation, &latest.Status, &latest.StructureConfirmedGeneration)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ReadinessResult{}, "", nil, err
	}
	if err == nil {
		result = withStructureReadiness(result, latest)
	}
	result, err = assessmentReadinessHash(ctx, queryer, tenantID, examID, status, result)
	if err != nil {
		return ReadinessResult{}, "", nil, err
	}
	result, err = withObjectiveReadinessPlan(ctx, queryer, tenantID, examID, status, questions, result)
	if err != nil {
		return ReadinessResult{}, "", nil, err
	}
	return result, status, candidateIDs, nil
}
