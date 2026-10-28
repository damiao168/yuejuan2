package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

var ErrLocalWorkerProvision = errors.New("local worker provisioning refused: check platform tenant, separate service usernames, credentials and the restricted worker role")

type LocalWorkerInput struct{ Username, Password string }

// ProvisionLocalWorkers 只供本地部署命令调用。既有账号必须已是同一受限服务身份且密码匹配，不改密码、不复活账号、不接管人类账号。
func (s *PostgresStore) ProvisionLocalWorkers(ctx context.Context, workers []LocalWorkerInput) error {
	if len(workers) == 0 || len(workers) > 3 {
		return ErrLocalWorkerProvision
	}
	seen := map[string]bool{}
	for _, worker := range workers {
		if !regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`).MatchString(worker.Username) || !PasswordMeetsMinimum(worker.Password, 15) || seen[worker.Username] {
			return ErrLocalWorkerProvision
		}
		seen[worker.Username] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var tenant string
	// 同一事务串行化本地工具的账号检查与创建；多能力中任一不匹配，整批回滚。
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM tenant WHERE id=$1::uuid AND code='platform' AND status='active' AND deleted_at IS NULL FOR UPDATE`, PlatformTenantID).Scan(&tenant); err != nil {
		return err
	}
	var role string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM role WHERE tenant_id=$1::uuid AND code='page_processing_worker' AND scope_type='service' AND deleted_at IS NULL FOR SHARE`, tenant).Scan(&role); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.code FROM role_permission rp JOIN permission p ON p.tenant_id=rp.tenant_id AND p.id=rp.permission_id AND p.deleted_at IS NULL WHERE rp.tenant_id=$1::uuid AND rp.role_id=$2::uuid AND rp.deleted_at IS NULL`, tenant, role)
	if err != nil {
		return err
	}
	permissions := []string{}
	for rows.Next() {
		var code string
		if err = rows.Scan(&code); err != nil {
			rows.Close()
			return err
		}
		permissions = append(permissions, code)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	sort.Strings(permissions)
	if len(permissions) != 2 || permissions[0] != "file:manage" || permissions[1] != "ocr:manage" {
		return ErrLocalWorkerProvision
	}
	for _, worker := range workers {
		if err = provisionLocalWorker(ctx, tx, tenant, role, worker); err != nil {
			return fmt.Errorf("worker %s: %w", worker.Username, err)
		}
	}
	return tx.Commit()
}

func provisionLocalWorker(ctx context.Context, tx *sql.Tx, tenant, role string, worker LocalWorkerInput) error {
	var id, hash, status string
	var undeleted, unscoped bool
	err := tx.QueryRowContext(ctx, `SELECT id::text,password_hash,status,deleted_at IS NULL,school_id IS NULL FROM app_user WHERE tenant_id=$1::uuid AND username=$2 FOR UPDATE`, tenant, worker.Username).Scan(&id, &hash, &status, &undeleted, &unscoped)
	if err == nil {
		var count int
		var restricted bool
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(bool_and(role_id=$3::uuid AND data_scope->>'scope'='service'),false) FROM user_role WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND deleted_at IS NULL`, tenant, id, role).Scan(&count, &restricted); err != nil {
			return err
		}
		if status != "active" || !undeleted || !unscoped || count != 1 || !restricted || !CheckPassword(hash, worker.Password) {
			return ErrLocalWorkerProvision
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	hash, err = HashPassword(worker.Password)
	if err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO app_user(tenant_id,username,display_name,password_hash,status) VALUES($1::uuid,$2,$2,$3,'active') RETURNING id::text`, tenant, worker.Username, hash).Scan(&id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO user_role(tenant_id,user_id,role_id,data_scope) VALUES($1::uuid,$2::uuid,$3::uuid,'{"scope":"service"}'::jsonb)`, tenant, id, role); err != nil {
		return err
	}
	// 审计只保留身份和职责，不写密码、哈希或启动进程环境。
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(tenant_id,action,target_type,target_id,after_value,reason) VALUES($1::uuid,'auth.local_worker_created','user',$2::uuid,$3::jsonb,'explicitly enable local processing capability')`, tenant, id, auditJSON(map[string]any{"username": worker.Username, "role_code": "page_processing_worker"}))
	return err
}
