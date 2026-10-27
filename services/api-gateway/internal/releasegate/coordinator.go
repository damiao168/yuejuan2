package releasegate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

// ScoreReleasePublisher 只提供范围受限的 A18 发布操作。
// 协调器看不到成绩事实，也不能发布可变结果。
type ScoreReleasePublisher interface {
	Publish(context.Context, string, string, string) (scorerelease.Release, error)
}

// PublicationCoordinator 是既有 A18 端点的 A20 组装点。
// 宿主必须先在同一租户作用域内解析 releaseID -> examID，再调用此协调器，而非直接调用 Store.Publish。
// 最终证据与发布共用考试锁内的临界区；PostgreSQL 实现还共用发布事务，不能以两次独立调用替代。
type PublicationCoordinator struct {
	gates    *Service
	releases ScoreReleasePublisher
}

func NewPublicationCoordinator(gates *Service, releases ScoreReleasePublisher) *PublicationCoordinator {
	return &PublicationCoordinator{gates: gates, releases: releases}
}

func (c *PublicationCoordinator) Publish(ctx context.Context, tenantID, examID, releaseID, actorID string) (scorerelease.Release, Evidence, error) {
	if c == nil || c.gates == nil || c.releases == nil || !validIDs(tenantID, examID, releaseID, actorID) {
		return scorerelease.Release{}, Evidence{}, ErrInvalidInput
	}
	if publisher, ok := c.releases.(interface {
		PublishGuarded(context.Context, string, string, string, scorerelease.PublishGuard) (scorerelease.Release, error)
	}); ok {
		var evidence Evidence
		release, err := publisher.PublishGuarded(ctx, tenantID, releaseID, actorID, func(ctx context.Context, tx *sql.Tx, release scorerelease.Release, base scorerelease.Gate) error {
			if release.TenantID != tenantID || release.ExamID != examID || release.ID != releaseID {
				return ErrInvalidInput
			}
			var err error
			evidence, err = c.gates.finalPublishCheck(ctx, tx, release, actorID, base)
			return err
		})
		if err != nil && !errors.Is(err, scorerelease.ErrGateBlocked) {
			// 事务写入失败会回滚证据，不能返回一个看似已经保存的证据标识。
			evidence = Evidence{}
		}
		return release, evidence, err
	}
	evidence, err := c.gates.RecheckForPublish(ctx, tenantID, examID, releaseID, actorID)
	if err != nil {
		if errors.Is(err, ErrGateBlocked) {
			// 传出 A18 的通用哨兵错误，让其 HTTP 边界返回明确冲突，同时 A20 保留只追加的证据。
			return scorerelease.Release{}, evidence, fmt.Errorf("%w: %v", scorerelease.ErrGateBlocked, err)
		}
		return scorerelease.Release{}, evidence, err
	}
	// 旧适配器仍可返回当前阻断原因，但缺少原子发布能力时不能使用通过证据继续公开。
	return scorerelease.Release{}, evidence, ErrAtomicPublicationUnavailable
}
