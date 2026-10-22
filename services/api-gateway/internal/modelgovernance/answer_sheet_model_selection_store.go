package modelgovernance

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (s *MemoryStore) GetAnswerSheetModelSelection(_ context.Context, tenantID string) (DocumentModelSelection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if item, ok := s.answerSheetSelections[tenantID]; ok {
		return item, nil
	}
	return initialAnswerSheetSelection(), nil
}

func (s *MemoryStore) SaveAnswerSheetModelSelection(_ context.Context, tenantID, _ string, input DocumentModelSelectionInput) (DocumentModelSelection, error) {
	input, err := normalizeAnswerSheetSelection(input)
	if err != nil {
		return DocumentModelSelection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.answerSheetSelections[tenantID]
	if !ok {
		current = initialAnswerSheetSelection()
	}
	if current.Revision != input.ExpectedRevision {
		return DocumentModelSelection{}, ErrAnswerSheetSelectionChanged
	}
	if input.Mode == "model" {
		config, ok := s.managedConfigs[input.ModelConfigID]
		if !ok || config.TenantID != tenantID || config.DeletedAt != nil || !CanAssignAnswerSheetModel(config) || s.managedSecrets[config.ID] == "" {
			return DocumentModelSelection{}, ErrAnswerSheetModelRequired
		}
	}
	now := time.Now().UTC()
	item := DocumentModelSelection{Revision: uuid.NewString(), Mode: input.Mode, ModelConfigID: input.ModelConfigID, UpdatedAt: &now}
	s.answerSheetSelections[tenantID] = item
	return item, nil
}

const answerSheetSelectionSQL = `SELECT revision,mode,COALESCE(managed_model_api_config_id::text,''),updated_at FROM tenant_answer_sheet_model_selection WHERE tenant_id=$1::uuid`

func scanAnswerSheetSelection(row rowScanner) (DocumentModelSelection, error) {
	var item DocumentModelSelection
	err := row.Scan(&item.Revision, &item.Mode, &item.ModelConfigID, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return initialAnswerSheetSelection(), nil
	}
	return item, err
}

func (s *PostgresStore) GetAnswerSheetModelSelection(ctx context.Context, tenantID string) (DocumentModelSelection, error) {
	return scanAnswerSheetSelection(s.db.QueryRowContext(ctx, answerSheetSelectionSQL, tenantID))
}

func (s *PostgresStore) SaveAnswerSheetModelSelection(ctx context.Context, tenantID, actorID string, input DocumentModelSelectionInput) (DocumentModelSelection, error) {
	input, err := normalizeAnswerSheetSelection(input)
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
			return DocumentModelSelection{}, ErrAnswerSheetModelRequired
		}
		if err != nil {
			return DocumentModelSelection{}, err
		}
		if !CanAssignAnswerSheetModel(config) {
			return DocumentModelSelection{}, ErrAnswerSheetModelRequired
		}
	}
	// absent 也需要学校锁，防止两次首次保存同时成功。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('answer-sheet-model:'||$1,0))`, tenantID); err != nil {
		return DocumentModelSelection{}, err
	}
	current, err := scanAnswerSheetSelection(tx.QueryRowContext(ctx, answerSheetSelectionSQL+` FOR UPDATE`, tenantID))
	if err != nil {
		return DocumentModelSelection{}, err
	}
	if current.Revision != input.ExpectedRevision {
		return DocumentModelSelection{}, ErrAnswerSheetSelectionChanged
	}
	item, err := scanAnswerSheetSelection(tx.QueryRowContext(ctx, `INSERT INTO tenant_answer_sheet_model_selection(tenant_id,mode,managed_model_api_config_id,revision,updated_by)
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
