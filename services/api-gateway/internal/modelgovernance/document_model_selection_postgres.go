package modelgovernance

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

func scanDocumentSelection(row rowScanner) (DocumentModelSelection, error) {
	var result DocumentModelSelection
	err := row.Scan(&result.Revision, &result.Mode, &result.ModelConfigID, &result.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return initialDocumentSelection(), nil
	}
	return result, err
}

const documentSelectionSQL = `SELECT revision,mode,COALESCE(managed_model_api_config_id::text,''),updated_at FROM tenant_document_model_selection WHERE tenant_id=$1::uuid`

func (s *PostgresStore) GetDocumentModelSelection(ctx context.Context, tenantID string) (DocumentModelSelection, error) {
	return scanDocumentSelection(s.db.QueryRowContext(ctx, documentSelectionSQL, tenantID))
}

func (s *PostgresStore) SaveDocumentModelSelection(ctx context.Context, tenantID, actorID string, input DocumentModelSelectionInput) (DocumentModelSelection, error) {
	input, err := normalizeDocumentSelection(input)
	if err != nil {
		return DocumentModelSelection{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DocumentModelSelection{}, err
	}
	defer tx.Rollback()
	if input.Mode == "model" {
		// 先锁模型，与软删除及凭据更新串行；资格校验和新增解析引用必须在同一事务完成。
		config, err := scanManagedAPIConfig(tx.QueryRowContext(ctx, managedAPIConfigSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, input.ModelConfigID))
		if errors.Is(err, sql.ErrNoRows) {
			return DocumentModelSelection{}, ErrManagedCapabilityRequired
		}
		if err != nil {
			return DocumentModelSelection{}, err
		}
		if config.Status != "active" || !config.CredentialConfigured || !managedCapabilityVerified(config) {
			return DocumentModelSelection{}, ErrManagedCapabilityRequired
		}
	}
	// 空设置也需要学校级锁，避免两个首次保存都依据 absent 成功；已有记录再核对读取修订。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('document-model:'||$1,0))`, tenantID); err != nil {
		return DocumentModelSelection{}, err
	}
	current, err := scanDocumentSelection(tx.QueryRowContext(ctx, documentSelectionSQL+` FOR UPDATE`, tenantID))
	if err != nil {
		return DocumentModelSelection{}, err
	}
	if current.Revision != input.ExpectedRevision {
		return DocumentModelSelection{}, ErrDocumentSelectionChanged
	}
	selection, err := scanDocumentSelection(tx.QueryRowContext(ctx, `
INSERT INTO tenant_document_model_selection (tenant_id,mode,managed_model_api_config_id,revision,updated_by)
VALUES ($1::uuid,$2,NULLIF($3,'')::uuid,$4::uuid,$5::uuid)
ON CONFLICT (tenant_id) DO UPDATE SET mode=EXCLUDED.mode,managed_model_api_config_id=EXCLUDED.managed_model_api_config_id,
revision=EXCLUDED.revision,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()
RETURNING revision,mode,COALESCE(managed_model_api_config_id::text,''),updated_at`, tenantID, input.Mode, input.ModelConfigID, uuid.NewString(), actorID))
	if err != nil {
		return DocumentModelSelection{}, err
	}
	if err = tx.Commit(); err != nil {
		return DocumentModelSelection{}, err
	}
	return selection, nil
}

func (s *PostgresStore) ListGradingModelUses(ctx context.Context, tenantID string) ([]GradingModelUse, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT managed_model_api_config_id,education_stage,subject_code,archetype_code,agent_role FROM model_role_binding WHERE tenant_id=$1::uuid AND status='active' ORDER BY managed_model_api_config_id,education_stage,subject_code,archetype_code,agent_role`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []GradingModelUse{}
	for rows.Next() {
		var item GradingModelUse
		if err := rows.Scan(&item.ModelConfigID, &item.EducationStage, &item.SubjectCode, &item.ArchetypeCode, &item.AgentRole); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
