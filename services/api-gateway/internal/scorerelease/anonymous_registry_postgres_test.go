package scorerelease

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image/png"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
)

type anonymousRegistryFixture struct {
	db                              *sql.DB
	tenant, actor, exam, submission string
	page                            anonymousSourcePage
	objects                         *files.MemoryObjectStorage
}

// 使用全量迁移后的独立测试库，保留真实外键、文件引用和证据删除触发器。
func newAnonymousRegistryFixture(t *testing.T) anonymousRegistryFixture {
	t.Helper()
	f := anonymousRegistryFixture{db: formalExportTestDB(t), objects: files.NewMemoryObjectStorage()}
	ctx := t.Context()
	if err := f.db.QueryRowContext(ctx, `SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id AND u.username='teacher' WHERE t.code='demo'`).Scan(&f.tenant, &f.actor); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	school := uuid.NewString()
	f.exam, f.submission = uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'匿名页事务测试学校',$1,'active')`, school, f.tenant)
	exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'匿名页事务测试','mathematics','midterm_exam',100,'grading','human_review_required','manual_after_confirmation',$4::uuid)`, f.exam, f.tenant, school, f.actor)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, syntheticAnonymousIdentityPage()); err != nil {
		t.Fatal(err)
	}
	body := encoded.Bytes()
	f.page = anonymousSourcePage{pageID: uuid.NewString(), pageNo: 1, bucket: "registry-test", key: "tenant/" + f.tenant + "/source.png", size: int64(len(body)), sourceHash: sha256Hex(body), templateID: uuid.NewString(), templateHash: "synthetic-template-hash", contentType: "image/png",
		layout: paper.TemplateLayout{Pages: []paper.TemplatePage{{PageNo: 1, Width: 1000, Height: 1400, IdentityRegions: []paper.LayoutRegion{{X: 0, Y: 0, Width: 0.5, Height: 0.2}}}}}}
	if err := f.objects.Put(ctx, f.page.bucket, f.page.key, bytes.NewReader(body), int64(len(body)), "image/png"); err != nil {
		t.Fatal(err)
	}
	asset, err := files.NewPostgresStore(f.db).Create(ctx, files.CreateAssetInput{TenantID: f.tenant, ExamID: f.exam, OwnerType: "exam", OwnerID: f.exam, OriginalName: "source.png", ContentType: "image/png", SizeBytes: int64(len(body)), HashSHA256: f.page.sourceHash, StorageBucket: f.page.bucket, StorageKey: f.page.key, Visibility: "private", UploadedBy: f.actor})
	if err != nil {
		t.Fatal(err)
	}
	paperID := uuid.NewString()
	exec(`INSERT INTO exam_paper(id,tenant_id,exam_id,file_asset_id,version_no,status,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'uploaded',$5::uuid)`, paperID, f.tenant, f.exam, asset.ID, f.actor)
	exec(`INSERT INTO answer_sheet_template(id,tenant_id,exam_id,exam_paper_id,version_no,name,status,page_count,layout,content_hash,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'合成模板','draft',1,'{}','synthetic-template-hash',$5::uuid)`, f.page.templateID, f.tenant, f.exam, paperID, f.actor)
	exec(`INSERT INTO submission(id,tenant_id,exam_id,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,'manual_import','quality_checked',1,1,'passed',$4::uuid)`, f.submission, f.tenant, f.exam, f.actor)
	exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, f.page.pageID, f.tenant, f.submission, asset.ID)
	// 控制断言：现行数据库确实禁止直接删除业务资产，不能靠关闭保护使补偿测试通过。
	if _, err := files.NewPostgresStore(f.db).Delete(ctx, f.tenant, asset.ID); err == nil {
		t.Fatal("business evidence deletion trigger is not active")
	}
	return f
}

func TestPostgresAnonymousRegistryRollbackAndCommitFailure(t *testing.T) {
	f := newAnonymousRegistryFixture(t)
	ctx := t.Context()
	manager := NewPostgresHighScorePaperManager(f.db, files.NewPostgresStore(f.db), f.objects, f.page.bucket)
	for index, mode := range []string{"business_insert_failure", "commit_failure", "success"} {
		t.Run(mode, func(t *testing.T) {
			release := uuid.NewString()
			if _, err := f.db.ExecContext(ctx, `INSERT INTO score_release(id,tenant_id,exam_id,version,source,reason,idempotency_key,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'initial','合成事务测试',$1,$5::uuid)`, release, f.tenant, f.exam, index+1, f.actor); err != nil {
				t.Fatal(err)
			}
			page := f.page
			if mode == "business_insert_failure" {
				page.pageID = uuid.NewString()
			}
			if mode == "commit_failure" {
				// 注入真实 PostgreSQL 延迟约束失败，让错误发生在 Commit 而不是登记语句。
				_, err := f.db.ExecContext(ctx, `CREATE FUNCTION test_anonymous_commit_failure() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic commit failure'; END $$;
CREATE CONSTRAINT TRIGGER test_anonymous_commit_failure AFTER INSERT ON file_asset DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.owner_type='score_release_anonymous_page') EXECUTE FUNCTION test_anonymous_commit_failure()`)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := f.db.ExecContext(ctx, `DROP TRIGGER test_anonymous_commit_failure ON file_asset; DROP FUNCTION test_anonymous_commit_failure()`); err != nil {
						t.Error(err)
					}
				}()
			}
			err := manager.preparePage(ctx, f.tenant, f.exam, release, f.actor, page)
			if (err == nil) != (mode == "success") {
				t.Fatalf("prepare mode=%s err=%v", mode, err)
			}
			prefix := fmt.Sprintf("tenant/%s/score-release/%s/anonymous/", f.tenant, release)
			var registryCount, businessCount int
			if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_asset WHERE storage_key LIKE $1`, prefix+"%").Scan(&registryCount); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM score_release_anonymous_page WHERE release_id=$1::uuid`, release).Scan(&businessCount); err != nil {
				t.Fatal(err)
			}
			objects, err := f.objects.List(ctx, f.page.bucket, prefix, 10)
			if err != nil {
				t.Fatal(err)
			}
			wantRegistry, wantObjects := 0, 0
			if mode == "success" {
				wantRegistry, wantObjects = 1, 1
			} else if mode == "commit_failure" {
				// 提交报错后保留对象，由巡检识别孤立对象；不能误删结果可能已提交的业务对象。
				wantObjects = 1
			}
			if registryCount != wantRegistry || businessCount != wantRegistry || len(objects) != wantObjects {
				t.Fatalf("registry=%d business=%d objects=%d; want %d/%d", registryCount, businessCount, len(objects), wantRegistry, wantObjects)
			}
		})
	}
}

func TestPostgresReconciliationPreservesLegacyBusinessDeletion(t *testing.T) {
	f := newAnonymousRegistryFixture(t)
	ctx := t.Context()
	for index, present := range []bool{true, false} {
		assetID := uuid.NewString()
		key := fmt.Sprintf("tenant/%s/legacy-%d", f.tenant, index)
		body := []byte("synthetic business evidence")
		// INSERT 直接构造旧版本遗留的待删状态；巡检执行时所有生产触发器保持启用。
		_, err := f.db.ExecContext(ctx, `INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by,lifecycle_status) VALUES($1::uuid,$2::uuid,$3::uuid,'capture',$1::uuid,'synthetic.png','image/png',$4,$5,$6,$7,'private',$8::uuid,'pending_delete')`, assetID, f.tenant, f.exam, len(body), sha256Hex(body), f.page.bucket, key, f.actor)
		if err != nil {
			t.Fatal(err)
		}
		if present {
			if err := f.objects.Put(ctx, f.page.bucket, key, bytes.NewReader(body), int64(len(body)), "image/png"); err != nil {
				t.Fatal(err)
			}
		}
		result, err := files.NewReconciler(f.db, f.objects).Run(ctx, files.ReconciliationOptions{Bucket: f.page.bucket, Repair: true})
		if err != nil || result.Status != "completed" {
			t.Fatalf("protected asset stopped reconciliation: %+v %v", result, err)
		}
		var lifecycle, owner string
		if err := f.db.QueryRowContext(ctx, `SELECT lifecycle_status,owner_type FROM file_asset WHERE id=$1::uuid AND deleted_at IS NULL`, assetID).Scan(&lifecycle, &owner); err != nil {
			t.Fatal(err)
		}
		want := files.LifecycleMissingObject
		if present {
			want = files.LifecycleActive
		}
		if lifecycle != want || owner != "capture" {
			t.Fatalf("legacy evidence lifecycle=%s owner=%s", lifecycle, owner)
		}
		if present {
			if _, err := f.objects.Stat(context.Background(), f.page.bucket, key); err != nil {
				t.Fatal("protected object was physically deleted", err)
			}
		}
		if strings.Contains(result.ErrorDetail, "cannot be deleted") {
			t.Fatal("reconciliation still attempted the protected delete")
		}
	}
}
