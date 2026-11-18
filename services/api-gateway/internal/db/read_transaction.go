package db

import (
	"context"
	"database/sql"
)

// ReadExecutor 统一连接池和事务的查询能力。写入仍由业务事务显式执行。
type ReadExecutor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type readTransactionKey struct{}

type readTransaction struct {
	owner *sql.DB
	tx    *sql.Tx
}

// WithReadTransaction 将下游查询绑定到调用方正在使用的事务。调用方负责提交或回滚，并传入事务所属的连接池。
func WithReadTransaction(ctx context.Context, owner *sql.DB, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, readTransactionKey{}, readTransaction{owner: owner, tx: tx})
}

// ReadQueryer 只让显式使用此方法且属于同一连接池的查询复用事务，避免事务内再次等待池连接。
func ReadQueryer(ctx context.Context, owner *sql.DB) ReadExecutor {
	if read, ok := ctx.Value(readTransactionKey{}).(readTransaction); ok && read.owner == owner {
		return read.tx
	}
	return owner
}
