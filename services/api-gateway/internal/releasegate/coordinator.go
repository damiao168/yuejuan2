package releasegate

import (
	"context"
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
// A18 的 Postgres 发布事务仍重新计算自身来源门禁，因此并发成绩变化只能导致拒绝，不能凭过时证据发布。
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
	evidence, err := c.gates.RecheckForPublish(ctx, tenantID, examID, releaseID, actorID)
	if err != nil {
		if errors.Is(err, ErrGateBlocked) {
			// 传出 A18 的通用哨兵错误，让其 HTTP 边界返回明确冲突，同时 A20 保留只追加的证据。
			return scorerelease.Release{}, evidence, fmt.Errorf("%w: %v", scorerelease.ErrGateBlocked, err)
		}
		return scorerelease.Release{}, evidence, err
	}
	release, err := c.releases.Publish(ctx, tenantID, releaseID, actorID)
	if err != nil {
		return scorerelease.Release{}, evidence, err
	}
	return release, evidence, nil
}
