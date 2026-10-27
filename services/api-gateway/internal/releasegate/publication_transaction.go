package releasegate

import (
	"context"
	"database/sql"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

type transactionalGateStore interface {
	ActivePolicyTx(context.Context, *sql.Tx, string, string) (Policy, error)
	ApprovedWaiversTx(context.Context, *sql.Tx, string, string, string) ([]Waiver, error)
	AppendEvidenceTx(context.Context, *sql.Tx, Evidence) (Evidence, error)
}

type transactionalRegradeReader interface {
	BlockingRegradeCountTx(context.Context, *sql.Tx, string, string) (int, error)
}

// 只把最终求值需要的读取绑定到发布事务，其他 Store 方法不会在此适配器中调用。
type transactionGateStore struct {
	Store
	transactionalGateStore
	tx *sql.Tx
}

func (s transactionGateStore) ActivePolicy(ctx context.Context, tenantID, examID string) (Policy, error) {
	return s.ActivePolicyTx(ctx, s.tx, tenantID, examID)
}

func (s transactionGateStore) ApprovedWaivers(ctx context.Context, tenantID, examID, policyID string) ([]Waiver, error) {
	return s.ApprovedWaiversTx(ctx, s.tx, tenantID, examID, policyID)
}

func (s *Service) finalPublishCheck(ctx context.Context, tx *sql.Tx, release scorerelease.Release, actorID string, base scorerelease.Gate) (Evidence, error) {
	evaluation, err := s.evaluateBase(ctx, tx, release.TenantID, release.ExamID, base)
	if err != nil {
		return Evidence{}, err
	}
	evidence := Evidence{TenantID: release.TenantID, ExamID: release.ExamID, ReleaseID: release.ID, Phase: EvidencePublish, Evaluation: evaluation, CreatedBy: actorID, CreatedAt: s.now().UTC()}
	evidence.Hash = evidenceHash(evidence)
	if tx != nil {
		evidence, err = s.store.(transactionalGateStore).AppendEvidenceTx(ctx, tx, evidence)
	} else {
		evidence, err = s.store.AppendEvidence(ctx, evidence)
	}
	if err != nil {
		return Evidence{}, err
	}
	if !evaluation.Passed {
		return evidence, scorerelease.ErrGateBlocked
	}
	return evidence, nil
}
