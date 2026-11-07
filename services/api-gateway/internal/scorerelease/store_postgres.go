package scorerelease

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
	"time"
)

// DashboardReader 只提供聚合数据。
// 发布服务不会从仪表盘接收 Gold 答案、阅卷观察、候选文本或其他敏感质量数据。
type DashboardReader interface {
	Get(context.Context, string, string) (qualitydashboard.Dashboard, error)
}

type PostgresStore struct {
	db            *sql.DB
	dashboard     DashboardReader
	now           func() time.Time
	gradePreparer GradePreparer
}

// GradePreparer 只在调用方的建快照事务中准备成绩，不开启独立事务或提前公开。
type GradePreparer interface {
	PrepareForReleaseTx(context.Context, *sql.Tx, string, string, string) (bool, error)
}

// WithGradePreparer 由组装入口注入同一数据库的成绩存储，保持准备和快照原子提交。
func (s *PostgresStore) WithGradePreparer(preparer GradePreparer) *PostgresStore {
	s.gradePreparer = preparer
	return s
}

func NewPostgresStore(db *sql.DB, dashboard DashboardReader) *PostgresStore {
	return &PostgresStore{db: db, dashboard: dashboard, now: func() time.Time { return time.Now().UTC() }}
}

func lockExam(ctx context.Context, tx *sql.Tx, tenantID, examID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, tenantID, examID)
	return err
}
