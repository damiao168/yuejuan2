package processing

import (
	"context"
	"database/sql"
	"errors"
)

// lockProjectionArchiveBoundary 在读取归档事实前取得成绩业务锁和行锁。
// 归档提交之后的投影只确认消费版本，不重建冻结的状态和异常；旧快照冲突交由投影重试处理。
func lockProjectionArchiveBoundary(ctx context.Context, tx *sql.Tx, tenantID, examID string) (bool, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`, tenantID, examID); err != nil {
		return false, err
	}
	var archived bool
	var sessionID sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT archived_at IS NOT NULL OR status='archived',exam_session_id::text FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid FOR SHARE`, tenantID, examID).Scan(&archived, &sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if sessionID.Valid {
		var parentArchived bool
		if err := tx.QueryRowContext(ctx, `SELECT archived_at IS NOT NULL OR status='archived' FROM exam_session WHERE tenant_id=$1::uuid AND id=$2::uuid FOR SHARE`, tenantID, sessionID.String).Scan(&parentArchived); err != nil {
			return false, err
		}
		archived = archived || parentArchived
	}
	return archived, nil
}
