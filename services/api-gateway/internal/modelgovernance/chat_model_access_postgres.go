package modelgovernance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

const chatAccessSQL = `SELECT revision,model_ids FROM tenant_chat_model_access WHERE tenant_id=$1::uuid`

func scanChatAccess(row rowScanner) (ChatModelAccess, error) {
	var result ChatModelAccess
	var raw []byte
	if err := row.Scan(&result.Revision, &raw); err != nil {
		return result, err
	}
	return result, json.Unmarshal(raw, &result.ModelIDs)
}
func (s *PostgresStore) GetChatModelAccess(ctx context.Context, tenantID string) (ChatModelAccess, error) {
	result, err := scanChatAccess(s.db.QueryRowContext(ctx, chatAccessSQL, tenantID))
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	configs, err := s.ListManagedAPIConfigs(ctx, tenantID)
	return initialChatAccess(configs), err
}
func (s *PostgresStore) SaveChatModelAccess(ctx context.Context, tenantID, actorID string, input ChatModelAccessInput) (ChatModelAccess, error) {
	input, err := normalizeChatAccess(input)
	if err != nil {
		return ChatModelAccess{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatModelAccess{}, err
	}
	defer tx.Rollback()
	// 先按稳定顺序锁定模型，再锁开放设置；与停用、凭据修改及删除串行，空设置也校验修订。
	rows, err := tx.QueryContext(ctx, managedAPIConfigSelect+` WHERE tenant_id=$1::uuid AND deleted_at IS NULL ORDER BY id FOR SHARE`, tenantID)
	if err != nil {
		return ChatModelAccess{}, err
	}
	configs := []ManagedAPIConfig{}
	for rows.Next() {
		config, e := scanManagedAPIConfig(rows)
		if e != nil {
			rows.Close()
			return ChatModelAccess{}, e
		}
		configs = append(configs, config)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ChatModelAccess{}, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('chat-access:'||$1,0))`, tenantID); err != nil {
		return ChatModelAccess{}, err
	}
	current, err := scanChatAccess(tx.QueryRowContext(ctx, chatAccessSQL+` FOR UPDATE`, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		current = initialChatAccess(configs)
	} else if err != nil {
		return ChatModelAccess{}, err
	}
	if err = validateChatAccess(current, input, configs); err != nil {
		return ChatModelAccess{}, err
	}
	raw, err := json.Marshal(input.ModelIDs)
	if err != nil {
		return ChatModelAccess{}, err
	}
	result, err := scanChatAccess(tx.QueryRowContext(ctx, `INSERT INTO tenant_chat_model_access(tenant_id,model_ids,revision,updated_by)
 VALUES($1::uuid,$2::jsonb,$3::uuid,$4::uuid) ON CONFLICT(tenant_id) DO UPDATE SET model_ids=EXCLUDED.model_ids,revision=EXCLUDED.revision,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp() RETURNING revision,model_ids`, tenantID, string(raw), uuid.NewString(), actorID))
	if err != nil {
		return ChatModelAccess{}, err
	}
	if err = tx.Commit(); err != nil {
		return ChatModelAccess{}, err
	}
	return result, nil
}

// 默认项与聊天开放范围已经分开；只在旧继承成员会改变时物化名单，和模型更新一起提交或回滚。
// 即使已有显式名单，也先按 ID 锁定模型行，防止切换默认项时反向取锁，与开放保存相互等待。
// 需要写继承名单时，最后再取与 SaveChatModelAccess 相同的 advisory lock。
func preserveInheritedChatAccessPostgres(ctx context.Context, tx *sql.Tx, tenantID, changedID string, newDefault bool) error {
	rows, err := tx.QueryContext(ctx, `SELECT id::text,is_default,COALESCE(created_by::text,'') FROM managed_model_api_config WHERE tenant_id=$1::uuid AND deleted_at IS NULL ORDER BY id FOR UPDATE`, tenantID)
	if err != nil {
		return err
	}
	ids := []string{}
	actorID, changed := "00000000-0000-0000-0000-000000000000", false
	for rows.Next() {
		var id, creator string
		var isDefault bool
		if err = rows.Scan(&id, &isDefault, &creator); err != nil {
			rows.Close()
			return err
		}
		if isDefault {
			ids = append(ids, id)
			if creator != "" {
				actorID = creator
			}
			if id == changedID && !newDefault || id != changedID && newDefault {
				changed = true
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || !changed {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tenant_chat_model_access WHERE tenant_id=$1::uuid)`, tenantID).Scan(&exists); err != nil || exists {
		return err
	}
	if user, ok := auth.UserFromContext(ctx); ok && uuid.Validate(user.ID) == nil {
		actorID = user.ID
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('chat-access:'||$1,0))`, tenantID); err != nil {
		return err
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	// 等待锁期间可能已有显式开放设置；不能用继承快照覆盖管理员刚保存的名单。
	_, err = tx.ExecContext(ctx, `INSERT INTO tenant_chat_model_access(tenant_id,model_ids,updated_by) VALUES($1::uuid,$2::jsonb,$3::uuid) ON CONFLICT(tenant_id) DO NOTHING`, tenantID, string(raw), actorID)
	return err
}
