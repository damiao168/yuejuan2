package scorerelease

import (
	"context"
	"database/sql"
)

// PublishGuard 在最终考试锁内接收重算的基础门禁。PostgreSQL 调用方必须只使用传入事务。
// 返回 ErrGateBlocked 时存储只提交追加的阻断证据，不修改发布状态；其他错误全部回滚。
type PublishGuard func(context.Context, *sql.Tx, Release, Gate) error

type guardedPublisher interface {
	PublishGuarded(context.Context, string, string, string, PublishGuard) (Release, error)
}

// PublishGuarded 将策略检查和成绩公开绑定在同一临界区，不能以事务外检查替代。
func (s *Service) PublishGuarded(ctx context.Context, tenantID, id, actorID string, guard PublishGuard) (Release, error) {
	if guard == nil {
		return Release{}, ErrInvalidInput
	}
	return s.publish(ctx, tenantID, id, actorID, guard)
}
