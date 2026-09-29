package idempotency

import (
	"context"
	"errors"
	"time"
)

var (
	ErrKeyConflict = errors.New("idempotency key reused with a different request")
	ErrInProgress  = errors.New("idempotent operation is in progress")
)

type BeginInput struct {
	TenantID      string
	ActorID       string
	Method        string
	Route         string
	Key           string
	RequestHash   string
	RequestBody   []byte
	ExpiresAt     time.Time
	AllowTakeover bool
	StaleBefore   time.Time
}

type Record struct {
	State           string
	RequestHash     string
	RequestBody     []byte
	ResponseStatus  int
	ResponseHeaders map[string]string
	ResponseBody    []byte
	UpdatedAt       time.Time
}

type Store interface {
	// Begin 返回的布尔值表示是否取得执行权；为 false 时只能重放已有结果。
	Begin(ctx context.Context, input BeginInput) (Record, bool, error)
	Complete(ctx context.Context, input BeginInput, status int, headers map[string]string, body []byte) error
	Abort(ctx context.Context, input BeginInput) error
}
