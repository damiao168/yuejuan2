package score

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"github.com/google/uuid"
)

func assertAuditGradeRLSAndFileRetention(t *testing.T, db *sql.DB, tenant, exam string) {
	t.Helper()
	ctx := t.Context()
	for _, table := range []string{"question_grade", "submission_grade", "score_release", "score_release_item", "question_appeal", "exam_candidate_snapshot", "review_task", "arbitration_task", "answer_candidate", "ai_grade", "subjective_grading_run"} {
		var forced bool
		if err := db.QueryRowContext(ctx, `SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid=$1::regclass`, table).Scan(&forced); err != nil || !forced {
			t.Fatal(table, forced, err)
		}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(ctx, `RESET ROLE`)
	if _, err = conn.ExecContext(ctx, `SET ROLE edugrade_tenant_runtime`); err != nil {
		t.Fatal(err)
	}
	for _, contextTenant := range []string{tenant, auth.PlatformTenantID, "unscoped"} {
		if _, err = conn.ExecContext(ctx, `SELECT set_config('edugrade.tenant_id',$1,false)`, contextTenant); err != nil {
			t.Fatal(err)
		}
		var count int
		if err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM submission_grade WHERE exam_id=$1::uuid`, exam).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if contextTenant == tenant && count != 2 || contextTenant != tenant && count != 0 {
			t.Fatal("RLS total visibility", contextTenant, count)
		}
	}
	if _, err = conn.ExecContext(ctx, `INSERT INTO gold_quality_image_pool(tenant_id,hash_sha256,quality_pool) VALUES($1::uuid,$2,'blind')`, tenant, strings.Repeat("f", 64)); err == nil {
		t.Fatal("RLS accepted cross tenant image registration")
	}
	if _, err = conn.ExecContext(ctx, `RESET ROLE`); err != nil {
		t.Fatal(err)
	}
	assetID := uuid.NewString()
	if _, err = db.ExecContext(ctx, `INSERT INTO file_asset(id,tenant_id,owner_type,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by)
VALUES($1::uuid,$2::uuid,'generic','retained.png','image/png',1,$3,'synthetic',$1,'tenant',(SELECT id FROM app_user WHERE tenant_id=$2::uuid LIMIT 1))`, assetID, tenant, strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	// 新增外键引用也必须被动态防删盘点发现，不能只守住历史的三张引用表。
	if _, err = db.ExecContext(ctx, `CREATE TABLE audit_answer_reference(tenant_id UUID,file_asset_id UUID REFERENCES file_asset(id))`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO audit_answer_reference VALUES($1::uuid,$2::uuid)`, tenant, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err = files.NewPostgresStore(db).BeginDelete(ctx, auth.AccessScope{TenantID: tenant, TenantWide: true}, assetID, 1); !errors.Is(err, files.ErrForbidden) {
		t.Fatal("referenced generic asset deleted", err)
	}
	assertAuditFileAliasReferenceRace(t, db, tenant)
}

func assertAuditFileAliasReferenceRace(t *testing.T, db *sql.DB, tenant string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER guard_active_file_reference BEFORE INSERT OR UPDATE ON audit_answer_reference FOR EACH ROW EXECUTE FUNCTION edugrade_guard_file_reference('file_asset_id')`); err != nil {
		t.Fatal(err)
	}
	addPair := func() (string, string) {
		t.Helper()
		first, alias := uuid.NewString(), uuid.NewString()
		for _, id := range []string{first, alias} {
			if _, err := db.ExecContext(ctx, `INSERT INTO file_asset(id,tenant_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by)
VALUES($1::uuid,$2::uuid,'generic',$1::uuid,'alias.png','image/png',1,$3,'synthetic',$4,'tenant',(SELECT id FROM app_user WHERE tenant_id=$2::uuid LIMIT 1))`, id, tenant, strings.Repeat("e", 64), first); err != nil {
				t.Fatal(err)
			}
		}
		return first, alias
	}
	first, alias := addPair()
	reference, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reference.Rollback()
	if _, err = reference.ExecContext(ctx, `INSERT INTO audit_answer_reference VALUES($1::uuid,$2::uuid)`, tenant, alias); err != nil {
		t.Fatal(err)
	}
	// 引用另一个资产别名时仍须挡住当前对象的删除；引用事务提交前，删除不能完成。
	deleted := make(chan error, 1)
	go func() {
		_, deleteErr := files.NewPostgresStore(db).BeginDelete(ctx, auth.AccessScope{TenantID: tenant, TenantWide: true}, first, 1)
		deleted <- deleteErr
	}()
	select {
	case err := <-deleted:
		t.Fatal("deletion passed an in-flight alias reference", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = reference.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-deleted; !errors.Is(err, files.ErrForbidden) {
		t.Fatal("committed alias reference was not retained", err)
	}
	first, alias = addPair()
	if _, err = files.NewPostgresStore(db).BeginDelete(ctx, auth.AccessScope{TenantID: tenant, TenantWide: true}, first, 1); err != nil {
		t.Fatal("unused generic file must remain deletable", err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO audit_answer_reference VALUES($1::uuid,$2::uuid)`, tenant, alias); err == nil {
		t.Fatal("new reference accepted an alias of a pending-delete object")
	}
}
