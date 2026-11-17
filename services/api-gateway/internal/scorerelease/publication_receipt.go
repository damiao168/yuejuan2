package scorerelease

import (
	"context"
	"database/sql"
)

// PublicationReceipt 仅供旧发布入口把命令回执并入正式发布事务，不另建发布实现。
// Load 负责核对原命令身份和请求；Save 接收最终冻结事实，以及本次是否实际公开了新版本。
// PostgreSQL 回调只能使用传入事务，使失败时公开指针与回执一起回滚。
// 内存回调的 tx 为 nil，先于公开状态变更执行；回调须自行保持失败时回执未写入，且不能重入本发布存储。
type PublicationReceipt interface {
	Load(context.Context, *sql.Tx) (Release, bool, error)
	Save(ctx context.Context, tx *sql.Tx, release Release, facts []SubmissionFact, newlyPublished bool) error
}

type publicationReceiptContextKey struct{}
type initialPublicationOnlyContextKey struct{}

// WithPublicationReceipt 为本次兼容调用附带事务回执；普通发布请求不设置它。
func WithPublicationReceipt(ctx context.Context, receipt PublicationReceipt) context.Context {
	return context.WithValue(ctx, publicationReceiptContextKey{}, receipt)
}

// WithInitialPublicationOnly 限制兼容调用只公开首版。一旦已有当前版本，发布锁内只记录现有版本的命令回执。
func WithInitialPublicationOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, initialPublicationOnlyContextKey{}, true)
}

func initialPublicationOnly(ctx context.Context) bool {
	initial, _ := ctx.Value(initialPublicationOnlyContextKey{}).(bool)
	return initial
}

func publicationReceiptFromContext(ctx context.Context) PublicationReceipt {
	receipt, _ := ctx.Value(publicationReceiptContextKey{}).(PublicationReceipt)
	return receipt
}

func validReceiptRelease(replay, target Release) bool {
	return replay.ID != "" && replay.TenantID == target.TenantID && replay.ExamID == target.ExamID && replay.Status == StatusPublished
}

func (s *PostgresStore) savePublicationReceipt(ctx context.Context, tx *sql.Tx, release Release, newlyPublished bool) error {
	receipt := publicationReceiptFromContext(ctx)
	if receipt == nil {
		return nil
	}
	// 发布过程中可能刷新或合并草稿；回执必须使用事务内最终快照，不能用创建草稿时的旧事实。
	facts, err := s.releaseFactsTx(ctx, tx, release.TenantID, release.ID)
	if err != nil {
		return err
	}
	return receipt.Save(ctx, tx, release, facts, newlyPublished)
}
