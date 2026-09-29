package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type QueryObserver struct {
	SlowThreshold time.Duration
	Observe       func(operation string, outcome string, duration time.Duration, slow bool)
	LogSlow       func(ctx context.Context, operation string, duration time.Duration, queryErr error)
}

// OpenPostgres 创建连接池；启用租户隔离时，每次数据库操作都会绑定上下文中的租户。
// 返回的清理函数可重复调用，并负责释放 pgx 注册配置；创建连接池本身不验证连通性。
func OpenPostgres(cfg config.PostgresConfig, observers ...QueryObserver) (*sql.DB, func() error, error) {
	connConfig, err := newPostgresConnConfig(cfg, observers...)
	if err != nil {
		return nil, nil, err
	}
	registeredConfig := stdlib.RegisterConnConfig(connConfig)
	var database *sql.DB
	if cfg.TenantRLSEnabled {
		driverContext, ok := stdlib.GetDefaultDriver().(driver.DriverContext)
		if !ok {
			stdlib.UnregisterConnConfig(registeredConfig)
			return nil, nil, fmt.Errorf("pgx driver does not support connectors")
		}
		connector, connectorErr := driverContext.OpenConnector(registeredConfig)
		if connectorErr != nil {
			stdlib.UnregisterConnConfig(registeredConfig)
			return nil, nil, connectorErr
		}
		database = sql.OpenDB(tenantConnector{base: connector})
	} else {
		database, err = sql.Open("pgx", registeredConfig)
	}
	if err != nil {
		stdlib.UnregisterConnConfig(registeredConfig)
		return nil, nil, err
	}
	database.SetMaxOpenConns(cfg.MaxOpenConns)
	database.SetMaxIdleConns(cfg.MaxIdleConns)
	database.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	database.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	var closeOnce sync.Once
	var closeErr error
	closeDatabase := func() error {
		closeOnce.Do(func() {
			closeErr = database.Close()
			stdlib.UnregisterConnConfig(registeredConfig)
		})
		return closeErr
	}
	return database, closeDatabase, nil
}

func newPostgresConnConfig(cfg config.PostgresConfig, observers ...QueryObserver) (*pgx.ConnConfig, error) {
	connConfig, err := pgx.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, err
	}
	connConfig.RuntimeParams["statement_timeout"] = postgresDuration(cfg.StatementTimeout)
	connConfig.RuntimeParams["lock_timeout"] = postgresDuration(cfg.LockTimeout)
	if len(observers) > 0 {
		connConfig.Tracer = queryTracer{observer: observers[0]}
	}
	return connConfig, nil
}

func postgresDuration(value time.Duration) string {
	return strconv.FormatInt(value.Milliseconds(), 10) + "ms"
}

type queryTraceState struct {
	startedAt time.Time
	operation string
	skip      bool
}

type queryTraceKey struct{}

type queryTracer struct {
	observer QueryObserver
}

func (t queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	// 租户设置属于连接管理开销，排除它可避免把一次业务查询重复计入指标。
	if strings.Contains(data.SQL, "set_config('edugrade.tenant_id'") || strings.EqualFold(strings.TrimSpace(data.SQL), "SET ROLE "+tenantRuntimeRole) {
		return context.WithValue(ctx, queryTraceKey{}, queryTraceState{skip: true})
	}
	return context.WithValue(ctx, queryTraceKey{}, queryTraceState{
		startedAt: time.Now(),
		operation: sqlOperation(data.SQL),
	})
}

func (t queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	state, ok := ctx.Value(queryTraceKey{}).(queryTraceState)
	if !ok {
		return
	}
	if state.skip {
		return
	}
	duration := time.Since(state.startedAt)
	slow := t.observer.SlowThreshold > 0 && duration >= t.observer.SlowThreshold
	outcome := "ok"
	if data.Err != nil {
		outcome = "error"
	}
	if t.observer.Observe != nil {
		t.observer.Observe(state.operation, outcome, duration, slow)
	}
	if slow && t.observer.LogSlow != nil {
		t.observer.LogSlow(ctx, state.operation, duration, data.Err)
	}
}

// 指标只保留操作类别，避免把 SQL 正文中的业务数据带入监控标签。
func sqlOperation(statement string) string {
	fields := strings.Fields(statement)
	if len(fields) == 0 {
		return "unknown"
	}
	operation := strings.ToLower(strings.Trim(fields[0], "();"))
	switch operation {
	case "select", "insert", "update", "delete", "with", "copy", "begin", "commit", "rollback":
		return operation
	default:
		return "other"
	}
}
