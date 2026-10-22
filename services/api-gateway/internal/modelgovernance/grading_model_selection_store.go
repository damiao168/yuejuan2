package modelgovernance

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (s *MemoryStore) GetGradingModelSelection(_ context.Context, tenantID string) (DocumentModelSelection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if item, ok := s.gradingSelections[tenantID]; ok {
		return item, nil
	}
	return initialGradingSelection(), nil
}

func (s *MemoryStore) SaveGradingModelSelection(_ context.Context, tenantID, _ string, input DocumentModelSelectionInput) (DocumentModelSelection, error) {
	input, err := normalizeGradingSelection(input)
	if err != nil {
		return DocumentModelSelection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.gradingSelections[tenantID]
	if !ok {
		current = initialGradingSelection()
	}
	if current.Revision != input.ExpectedRevision {
		return DocumentModelSelection{}, ErrGradingSelectionChanged
	}
	if input.Mode == "model" {
		config, ok := s.managedConfigs[input.ModelConfigID]
		if !ok || config.TenantID != tenantID || config.DeletedAt != nil || !CanAssignGradingModel(config) || s.managedSecrets[config.ID] == "" {
			return DocumentModelSelection{}, ErrGradingModelRequired
		}
	}
	now := time.Now().UTC()
	item := DocumentModelSelection{Revision: uuid.NewString(), Mode: input.Mode, ModelConfigID: input.ModelConfigID, UpdatedAt: &now}
	if s.gradingSelections == nil {
		s.gradingSelections = map[string]DocumentModelSelection{}
	}
	s.gradingSelections[tenantID] = item
	return item, nil
}

const gradingSelectionSQL = `SELECT revision,mode,COALESCE(managed_model_api_config_id::text,''),updated_at FROM tenant_grading_model_selection WHERE tenant_id=$1::uuid`

func scanGradingSelection(row rowScanner) (DocumentModelSelection, error) {
	var item DocumentModelSelection
	err := row.Scan(&item.Revision, &item.Mode, &item.ModelConfigID, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return initialGradingSelection(), nil
	}
	return item, err
}

func (s *PostgresStore) GetGradingModelSelection(ctx context.Context, tenantID string) (DocumentModelSelection, error) {
	return scanGradingSelection(s.db.QueryRowContext(ctx, gradingSelectionSQL, tenantID))
}

func (s *PostgresStore) SaveGradingModelSelection(ctx context.Context, tenantID, actorID string, input DocumentModelSelectionInput) (DocumentModelSelection, error) {
	input, err := normalizeGradingSelection(input)
	if err != nil {
		return DocumentModelSelection{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DocumentModelSelection{}, err
	}
	defer tx.Rollback()
	if input.Mode == "model" {
		// 模型行先加锁，与连接更新和删除同序；验证与新增引用必须属于同一事务。
		config, err := scanManagedAPIConfig(tx.QueryRowContext(ctx, managedAPIConfigSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, input.ModelConfigID))
		if errors.Is(err, sql.ErrNoRows) {
			return DocumentModelSelection{}, ErrGradingModelRequired
		}
		if err != nil {
			return DocumentModelSelection{}, err
		}
		if !CanAssignGradingModel(config) {
			return DocumentModelSelection{}, ErrGradingModelRequired
		}
	}
	// absent 也需要学校锁，防止两次首次保存同时成功。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('grading-model:'||$1,0))`, tenantID); err != nil {
		return DocumentModelSelection{}, err
	}
	current, err := scanGradingSelection(tx.QueryRowContext(ctx, gradingSelectionSQL+` FOR UPDATE`, tenantID))
	if err != nil {
		return DocumentModelSelection{}, err
	}
	if current.Revision != input.ExpectedRevision {
		return DocumentModelSelection{}, ErrGradingSelectionChanged
	}
	item, err := scanGradingSelection(tx.QueryRowContext(ctx, `INSERT INTO tenant_grading_model_selection(tenant_id,mode,managed_model_api_config_id,revision,updated_by)
 VALUES($1::uuid,$2,NULLIF($3,'')::uuid,$4::uuid,$5::uuid) ON CONFLICT(tenant_id) DO UPDATE SET mode=EXCLUDED.mode,managed_model_api_config_id=EXCLUDED.managed_model_api_config_id,revision=EXCLUDED.revision,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()
 RETURNING revision,mode,COALESCE(managed_model_api_config_id::text,''),updated_at`, tenantID, input.Mode, input.ModelConfigID, uuid.NewString(), actorID))
	if err != nil {
		return DocumentModelSelection{}, err
	}
	if err = tx.Commit(); err != nil {
		return DocumentModelSelection{}, err
	}
	return item, nil
}
