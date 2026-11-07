package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
)

func validatePaperImportAssetsInTx(ctx context.Context, tx *sql.Tx, tenantID, examID, paperID string, sources []CreatePaperImportSourceInput) error {
	// 所有资产都在创建事务内按原顺序核验；考试关联不能由客户端提供的学科代替。
	if paperID != "" {
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM exam_paper WHERE tenant_id=$1 AND exam_id=$2 AND id=$3::uuid AND deleted_at IS NULL)`, tenantID, examID, paperID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrInvalidInput
		}
	}
	for _, source := range sources {
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM file_asset WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL AND (exam_id IS NULL OR exam_id=$3::uuid))`, tenantID, source.FileAssetID, examID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrInvalidInput
		}
	}
	return nil
}

// AddPaperImportSources 先识别已接受命令的重放，再校验代次；否则首次成功后的客户端重试会被当成过期请求。
func (s *PostgresStore) AddPaperImportSources(ctx context.Context, tenantID, id, userID string, input AddPaperImportSourcesInput) (PaperImportJob, error) {
	if input.CommandID == "" {
		input.CommandID = uuid.NewString()
	}
	if len(input.Sources) == 0 || input.ExpectedGeneration <= 0 {
		return PaperImportJob{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	requestHash := paperImportCommandHash(input)
	if existingID, existingHash, replayErr := findPaperImportCommandInTx(ctx, tx, tenantID, userID, input.CommandID); replayErr == nil {
		if existingID != id || existingHash != requestHash {
			return PaperImportJob{}, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return PaperImportJob{}, err
		}
		return s.GetPaperImport(ctx, tenantID, id)
	} else if !errors.Is(replayErr, ErrNotFound) {
		return PaperImportJob{}, replayErr
	}
	if err = ensureImportExamMutableInTx(ctx, tx, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	var status string
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT status,current_generation FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&status, &generation); errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, ErrNotFound
	} else if err != nil {
		return PaperImportJob{}, err
	}
	if status == "applied" || status == "processing" {
		return PaperImportJob{}, ErrConflict
	}
	if input.ExpectedGeneration > 0 && input.ExpectedGeneration != generation {
		return PaperImportJob{}, ErrConflict
	}
	if err = addPaperImportSourcesInTx(ctx, tx, tenantID, id, input.Sources); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET status='processing',error_code='',issues='[]'::jsonb,structured_issues='[]'::jsonb,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	job, err := getPaperImportInTx(ctx, tx, tenantID, id)
	if err != nil {
		return PaperImportJob{}, err
	}
	revision := paperImportSourceConfigurationHash(job.Sources)
	generation++
	var runID string
	if err = tx.QueryRowContext(ctx, `INSERT INTO paper_import_run
(tenant_id,paper_import_id,generation,command_id,command_type,command_request_hash,actor_id,source_revision,status,dispatch_status)
VALUES($1,$2::uuid,$3,$4,'add_sources',$5,$6::uuid,$7,'processing','pending') RETURNING id::text`, tenantID, id, generation, input.CommandID, requestHash, userID, revision).Scan(&runID); err != nil {
		return PaperImportJob{}, err
	}
	if err = savePaperImportSourceSnapshot(ctx, tx, tenantID, runID, job.Sources); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET current_generation=$3,source_revision=$4,result_generation=NULL,result_task_id=NULL,result_payload_hash=NULL WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id, generation, revision); err != nil {
		return PaperImportJob{}, err
	}
	if err = supersedePaperImportRunsInTx(ctx, tx, tenantID, id, generation); err != nil {
		return PaperImportJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return s.GetPaperImport(ctx, tenantID, id)
}

func addPaperImportSourcesInTx(ctx context.Context, tx *sql.Tx, tenantID, id string, sources []CreatePaperImportSourceInput) error {
	for _, source := range sources {
		if source.FileAssetID == "" || source.DocumentIndex < 0 || !validPaperImportRole(source.RoleHint, true) {
			return ErrInvalidInput
		}
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO paper_import_source(tenant_id,paper_import_id,file_asset_id,document_index,role_hint) SELECT $1,$2::uuid,$3::uuid,$4,$5 WHERE EXISTS(SELECT 1 FROM file_asset f JOIN paper_import_job j ON j.tenant_id=f.tenant_id AND j.id=$2::uuid WHERE f.tenant_id=$1 AND f.id=$3::uuid AND f.deleted_at IS NULL AND (f.exam_id IS NULL OR f.exam_id=j.exam_id))`, tenantID, id, source.FileAssetID, source.DocumentIndex, defaultRoleHint(source.RoleHint))
		if insertErr != nil {
			return ErrInvalidInput
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			return affectedErr
		}
		if affected != 1 {
			return ErrInvalidInput
		}
	}
	return nil
}

func (s *PostgresStore) ReplacePaperImportSources(ctx context.Context, tenantID, id, userID string, input ReplacePaperImportSourcesInput) (PaperImportJob, error) {
	if input.CommandID == "" {
		input.CommandID = uuid.NewString()
	}
	if input.ExpectedGeneration <= 0 {
		return PaperImportJob{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	requestHash := paperImportCommandHash(input)
	if existingID, existingHash, replayErr := findPaperImportCommandInTx(ctx, tx, tenantID, userID, input.CommandID); replayErr == nil {
		if existingID != id || existingHash != requestHash {
			return PaperImportJob{}, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return PaperImportJob{}, err
		}
		return s.GetPaperImport(ctx, tenantID, id)
	} else if !errors.Is(replayErr, ErrNotFound) {
		return PaperImportJob{}, replayErr
	}
	if err = ensureImportExamMutableInTx(ctx, tx, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	var status string
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT status,current_generation FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&status, &generation); errors.Is(err, sql.ErrNoRows) {
		return PaperImportJob{}, ErrNotFound
	} else if err != nil {
		return PaperImportJob{}, err
	}
	if status == "applied" {
		return PaperImportJob{}, ErrConflict
	}
	if input.ExpectedGeneration > 0 && input.ExpectedGeneration != generation {
		return PaperImportJob{}, ErrConflict
	}
	active, err := lockActivePaperImportSourcesInTx(ctx, tx, tenantID, id)
	if err != nil {
		return PaperImportJob{}, err
	}
	if err = validateReplacePaperImportSources(input.Sources, active); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, id); err != nil {
		return PaperImportJob{}, err
	}
	if err = restorePaperImportSourcesInTx(ctx, tx, tenantID, id, input.Sources); err != nil {
		return PaperImportJob{}, err
	}
	// 删除全部来源仍生成新代次并作废旧任务，但不派发空输入，避免旧识别结果重新出现。
	if len(input.Sources) == 0 {
		issues, _ := json.Marshal([]string{"已删除全部考试资料，请重新上传正确的资料"})
		_, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET status='failed',error_code='paper_import_no_sources',issues=$3,structured_issues='[]'::jsonb,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id, issues)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET status='processing',error_code='',issues='[]'::jsonb,structured_issues='[]'::jsonb,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id)
	}
	if err != nil {
		return PaperImportJob{}, err
	}
	job, loadErr := getPaperImportInTx(ctx, tx, tenantID, id)
	if loadErr != nil {
		return PaperImportJob{}, loadErr
	}
	revision := paperImportSourceConfigurationHash(job.Sources)
	generation++
	runStatus, dispatch := "processing", "pending"
	if len(input.Sources) == 0 {
		runStatus, dispatch = "failed", "not_required"
	}
	var runID string
	if err = tx.QueryRowContext(ctx, `INSERT INTO paper_import_run
(tenant_id,paper_import_id,generation,command_id,command_type,command_request_hash,actor_id,source_revision,status,dispatch_status,error_code)
VALUES($1,$2::uuid,$3,$4,'replace_sources',$5,$6::uuid,$7,$8,$9,CASE WHEN $8='failed' THEN 'paper_import_no_sources' ELSE NULL END) RETURNING id::text`, tenantID, id, generation, input.CommandID, requestHash, userID, revision, runStatus, dispatch).Scan(&runID); err != nil {
		return PaperImportJob{}, err
	}
	if err = savePaperImportSourceSnapshot(ctx, tx, tenantID, runID, job.Sources); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET current_generation=$3,source_revision=$4,result_generation=NULL,result_task_id=NULL,result_payload_hash=NULL WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, id, generation, revision); err != nil {
		return PaperImportJob{}, err
	}
	if err = supersedePaperImportRunsInTx(ctx, tx, tenantID, id, generation); err != nil {
		return PaperImportJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return s.GetPaperImport(ctx, tenantID, id)
}

// 来源集合已经验证并软删除；只按请求顺序恢复所选来源，任何行数异常都由调用方回滚整次替换。
func restorePaperImportSourcesInTx(ctx context.Context, tx *sql.Tx, tenantID, id string, sources []ReplacePaperImportSourceInput) error {
	for _, source := range sources {
		result, err := tx.ExecContext(ctx, `UPDATE paper_import_source SET document_index=$4,role_hint=$5,detected_role='unknown',role_confidence=0,processing_status='pending',deleted_at=NULL,updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND id=$3::uuid`, tenantID, id, source.ID, source.DocumentIndex, defaultRoleHint(source.RoleHint))
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrConflict
		}
	}
	return nil
}

func lockActivePaperImportSourcesInTx(ctx context.Context, tx *sql.Tx, tenantID, id string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM paper_import_source WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id)
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	for rows.Next() {
		var sourceID string
		if err = rows.Scan(&sourceID); err != nil {
			rows.Close()
			return nil, err
		}
		active[sourceID] = true
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return active, nil
}
