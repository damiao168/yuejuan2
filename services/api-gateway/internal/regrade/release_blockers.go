package regrade

import (
	"context"
	"database/sql"
)

// BlockingReleaseCountTx 只投影阻断公开的作业数，必须在最终发布持有考试锁时调用。
// ready 计划已经冻结，其余非取消状态仍需完成复核；查询不返回答案或分数。
func BlockingReleaseCountTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM regrade_job WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status NOT IN ($3,$4)`, tenantID, examID, StatusCancelled, StatusReadyForRelease).Scan(&count)
	return count, err
}
