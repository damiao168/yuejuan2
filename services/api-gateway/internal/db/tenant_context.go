package db

import (
	"context"
	"database/sql/driver"
	"fmt"

	"github.com/jackc/pgx/v5/stdlib"
)

type tenantContextKey struct{}
type tenantMaintenanceContextKey struct{}

const tenantRuntimeRole = "edugrade_tenant_runtime"
const tenantUnscopedSetting = "unscoped"
const tenantMaintenanceSetting = "maintenance"

// WithTenant binds the authenticated tenant to database operations issued with
// this context. Empty or missing tenant scopes fail closed for RLS-protected data.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantContextKey{}, tenantID)
}

// WithTenantMaintenance explicitly authorizes a process-level job to operate
// across tenants. Request handlers must use WithTenant through auth.WithUser.
func WithTenantMaintenance(ctx context.Context) context.Context {
	return context.WithValue(ctx, tenantMaintenanceContextKey{}, true)
}

func TenantFromContext(ctx context.Context) (string, bool) {
	tenantID, ok := ctx.Value(tenantContextKey{}).(string)
	return tenantID, ok && tenantID != ""
}

func tenantSettingFromContext(ctx context.Context) string {
	if tenantID, ok := TenantFromContext(ctx); ok {
		return tenantID
	}
	if maintenance, _ := ctx.Value(tenantMaintenanceContextKey{}).(bool); maintenance {
		return tenantMaintenanceSetting
	}
	return tenantUnscopedSetting
}

type tenantConnector struct{ base driver.Connector }

func (c tenantConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	pgxConn, ok := conn.(*stdlib.Conn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("tenant RLS requires the pgx stdlib driver")
	}
	var superuser, bypassRLS bool
	if err := pgxConn.Conn().QueryRow(ctx, `SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname=SESSION_USER`).Scan(&superuser, &bypassRLS); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("verify tenant RLS database identity: %w", err)
	}
	if superuser || bypassRLS {
		_ = conn.Close()
		return nil, fmt.Errorf("tenant RLS requires a non-superuser database login without BYPASSRLS")
	}
	return &tenantScopedConn{Conn: conn, pgx: pgxConn}, nil
}

func (c tenantConnector) Driver() driver.Driver { return c.base.Driver() }

type tenantScopedConn struct {
	driver.Conn
	pgx *stdlib.Conn
}

func (c *tenantScopedConn) applyTenant(ctx context.Context) error {
	// 连接会被连接池复用，每次操作前都要覆盖租户设置，不能沿用上个请求的身份。
	// 缺少租户时写入 unscoped，让受 RLS 保护的数据保持不可见。
	if _, err := c.pgx.Conn().Exec(ctx, `SET ROLE `+tenantRuntimeRole); err != nil {
		return fmt.Errorf("activate tenant RLS runtime role: %w", err)
	}
	tenantSetting := tenantSettingFromContext(ctx)
	_, err := c.pgx.Conn().Exec(ctx, `SELECT set_config('edugrade.tenant_id',$1,false)`, tenantSetting)
	return err
}

func (c *tenantScopedConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *tenantScopedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := c.applyTenant(ctx); err != nil {
		return nil, err
	}
	stmt, err := c.pgx.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &tenantScopedStmt{Stmt: stmt, conn: c}, nil
}

func (c *tenantScopedConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *tenantScopedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if err := c.applyTenant(ctx); err != nil {
		return nil, err
	}
	return c.pgx.BeginTx(ctx, opts)
}

func (c *tenantScopedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.applyTenant(ctx); err != nil {
		return nil, err
	}
	return c.pgx.ExecContext(ctx, query, args)
}

func (c *tenantScopedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.applyTenant(ctx); err != nil {
		return nil, err
	}
	return c.pgx.QueryContext(ctx, query, args)
}

func (c *tenantScopedConn) Ping(ctx context.Context) error {
	if err := c.applyTenant(context.Background()); err != nil {
		return err
	}
	return c.pgx.Ping(ctx)
}

func (c *tenantScopedConn) CheckNamedValue(value *driver.NamedValue) error {
	return c.pgx.CheckNamedValue(value)
}

func (c *tenantScopedConn) ResetSession(ctx context.Context) error {
	// 归还后的连接必须清除业务租户；下一次操作再根据自己的上下文重新绑定。
	if err := c.pgx.ResetSession(ctx); err != nil {
		return err
	}
	return c.applyTenant(context.Background())
}

type tenantScopedStmt struct {
	driver.Stmt
	conn *tenantScopedConn
}

func (s *tenantScopedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if err := s.conn.applyTenant(ctx); err != nil {
		return nil, err
	}
	return s.Stmt.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (s *tenantScopedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if err := s.conn.applyTenant(ctx); err != nil {
		return nil, err
	}
	return s.Stmt.(driver.StmtQueryContext).QueryContext(ctx, args)
}
