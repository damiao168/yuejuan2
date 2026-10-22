package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

// 使用唯一 schema 的最小父表与正式库迁移，验证真实事务、不可变快照及共享对象保护。
// 此夹具不替代完整部署迁移或对象存储访问测试。
func newTemplateLibraryPostgresFixture(t *testing.T) (*PostgresStore, auth.AccessScope, AnswerSheetTemplate, string, string) {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_TEMPLATE_LIBRARY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_TEMPLATE_LIBRARY_TEST_DATABASE_URL is not configured")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("review_template_library_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
	})
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" {
		t.Fatal("PostgreSQL URL is required")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("application_name", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(8)
	for _, ddl := range []string{
		`CREATE TABLE tenant(id uuid PRIMARY KEY)`,
		`CREATE TABLE school(id uuid PRIMARY KEY)`,
		`CREATE TABLE app_user(id uuid PRIMARY KEY)`,
		`CREATE TABLE exam(id uuid PRIMARY KEY,tenant_id uuid,school_id uuid,status text,deleted_at timestamptz)`,
		`CREATE TABLE exam_class(tenant_id uuid,exam_id uuid,class_id uuid,deleted_at timestamptz)`,
		`CREATE TABLE file_asset(id uuid PRIMARY KEY,tenant_id uuid,school_id uuid,exam_id uuid,submission_id uuid,owner_type text,owner_id uuid,original_name text,content_type text,size_bytes bigint,hash_sha256 text,storage_bucket text,storage_key text,visibility text,uploaded_by uuid,lifecycle_status text NOT NULL DEFAULT 'active',deleted_at timestamptz)`,
		`CREATE TABLE exam_paper(id uuid PRIMARY KEY,tenant_id uuid,exam_id uuid,file_asset_id uuid,version_no int,status text,uploaded_by uuid,purpose text,deleted_at timestamptz,UNIQUE(tenant_id,exam_id,version_no))`,
		`CREATE TABLE question(id uuid PRIMARY KEY,tenant_id uuid,exam_id uuid,question_no text,question_type text,status text,sort_order int DEFAULT 1,deleted_at timestamptz)`,
		`CREATE TABLE answer_sheet_template(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id uuid,exam_id uuid,exam_paper_id uuid,version_no int,revision int NOT NULL DEFAULT 1,name text,status text NOT NULL DEFAULT 'draft',page_count int,layout jsonb,content_hash text,created_by uuid,locked_by uuid,locked_at timestamptz,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),deleted_at timestamptz,UNIQUE(tenant_id,exam_id,version_no))`,
		`CREATE FUNCTION edugrade_tenant_matches(candidate uuid) RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true $$`,
	} {
		if _, err = db.ExecContext(t.Context(), ddl); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000190_answer_sheet_template_library.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), string(migration)); err != nil {
		t.Fatal(err)
	}
	scope := auth.AccessScope{TenantID: uuid.NewString(), ActorID: uuid.NewString(), TenantWide: true}
	school, sourceExam, targetExam := uuid.NewString(), uuid.NewString(), uuid.NewString()
	source := AnswerSheetTemplate{ID: uuid.NewString(), TenantID: scope.TenantID, ExamID: sourceExam, ExamPaperID: uuid.NewString(), Name: "source", Revision: 3, PageCount: 1, Status: "locked"}
	fileID, qID := uuid.NewString(), uuid.NewString()
	source.Layout = NormalizeTemplateLayout(TemplateLayout{Pages: []TemplatePage{{PageNo: 1, Width: 1000, Height: 1500, QuestionRegions: []LayoutRegion{{ID: "r", QuestionID: qID, X: .1, Y: .2, Width: .8, Height: .5}}}}})
	source.ContentHash = stableContentHash(source.Layout)
	raw, _ := json.Marshal(source.Layout)
	inserts := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO tenant VALUES($1)`, []any{scope.TenantID}},
		{`INSERT INTO school VALUES($1)`, []any{school}},
		{`INSERT INTO app_user VALUES($1)`, []any{scope.ActorID}},
		{`INSERT INTO exam VALUES($1,$2,$3,'configured',NULL),($4,$2,$3,'configured',NULL)`, []any{sourceExam, scope.TenantID, school, targetExam}},
		{`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,status) VALUES($1,$2,$3,'17','calculation','active'),($4,$2,$5,'17','calculation','active')`, []any{qID, scope.TenantID, sourceExam, uuid.NewString(), targetExam}},
		{`INSERT INTO file_asset(id,tenant_id,school_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1,$2,$3,$4,'exam_paper',$5,'blank.png','image/png',100,$6,'test','blank-object','private',$7)`, []any{fileID, scope.TenantID, school, sourceExam, source.ExamPaperID, strings.Repeat("a", 64), scope.ActorID}},
		{`INSERT INTO exam_paper(id,tenant_id,exam_id,file_asset_id,version_no,status,uploaded_by,purpose) VALUES($1,$2,$3,$4,1,'uploaded',$5,'answer_sheet')`, []any{source.ExamPaperID, scope.TenantID, sourceExam, fileID, scope.ActorID}},
		{`INSERT INTO answer_sheet_template(id,tenant_id,exam_id,exam_paper_id,version_no,revision,name,status,page_count,layout,content_hash,created_by) VALUES($1,$2,$3,$4,1,3,'source','locked',1,$5,$6,$7)`, []any{source.ID, scope.TenantID, sourceExam, source.ExamPaperID, raw, source.ContentHash, scope.ActorID}},
	}
	for _, insert := range inserts {
		if _, err = db.ExecContext(t.Context(), insert.query, insert.args...); err != nil {
			t.Fatal(err)
		}
	}
	return NewPostgresStore(db), scope, source, targetExam, fileID
}

func TestTemplateLibraryPostgresPublicationAndInstantiationAreIdempotentAndAtomic(t *testing.T) {
	s, scope, source, target, fileID := newTemplateLibraryPostgresFixture(t)
	input := PublishTemplateToLibraryInput{Name: "库模板", ExpectedRevision: 3, ConfirmedBlankReference: true, ConfirmedReusableLayout: true}
	var wg sync.WaitGroup
	versions := make(chan AnswerSheetTemplateLibraryVersion, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.PublishTemplateToLibrary(t.Context(), scope, source.ID, input)
			versions <- out
			errs <- err
		}()
	}
	wg.Wait()
	close(versions)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var item AnswerSheetTemplateLibraryVersion
	for next := range versions {
		if item.ID != "" && item.ID != next.ID {
			t.Fatal("duplicate concurrent publication")
		}
		item = next
	}
	// 库引用、原来源及后续实例共享对象；生命周期变更必须在删除对象之前被数据库阻止。
	if _, err := s.db.ExecContext(t.Context(), `UPDATE file_asset SET lifecycle_status='pending_delete' WHERE id=$1`, fileID); err == nil {
		t.Fatal("source object deletion was not pinned")
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE answer_sheet_template_library SET name='changed' WHERE id=$1`, item.ID); err == nil {
		t.Fatal("published library version mutated")
	}
	if _, err := s.db.ExecContext(t.Context(), `DELETE FROM question WHERE exam_id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InstantiateTemplateLibrary(t.Context(), scope, target, item.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing question accepted: %v", err)
	}
	var count int
	if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM exam_paper WHERE exam_id=$1`, target).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed instantiate left paper: count=%d err=%v", count, err)
	}
	qID := uuid.NewString()
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,status) VALUES($1,$2,$3,'17','calculation','active')`, qID, scope.TenantID, target); err != nil {
		t.Fatal(err)
	}
	ids := make(chan string, 2)
	errs = make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.InstantiateTemplateLibrary(t.Context(), scope, target, item.ID)
			ids <- out.Template.ID
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for next := range ids {
		if id != "" && id != next {
			t.Fatal("concurrent instantiate duplicated template")
		}
		id = next
	}
	out, err := s.InstantiateTemplateLibrary(t.Context(), scope, target, item.ID)
	if err != nil || !out.Reused || out.Template.Layout.Pages[0].QuestionRegions[0].QuestionID != qID || out.Template.Status != "draft" {
		t.Fatalf("incorrect exam mapping: %#v %v", out, err)
	}
	if _, err = s.db.ExecContext(t.Context(), `UPDATE file_asset SET lifecycle_status='pending_delete' WHERE id=$1`, out.Paper.FileAssetID); err == nil {
		t.Fatal("instance could delete the shared object")
	}
	if _, err = s.db.ExecContext(t.Context(), `UPDATE exam SET status='collecting' WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InstantiateTemplateLibrary(t.Context(), scope, target, item.ID); err != nil {
		t.Fatalf("read-only retry should return existing instance: %v", err)
	}
}

func TestTemplateLibraryPostgresDeleteWinningRacePreventsPublication(t *testing.T) {
	s, scope, source, _, fileID := newTemplateLibraryPostgresFixture(t)
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(t.Context(), `UPDATE file_asset SET lifecycle_status='pending_delete' WHERE id=$1`, fileID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := s.PublishTemplateToLibrary(t.Context(), scope, source.ID, PublishTemplateToLibraryInput{Name: "card", ExpectedRevision: 3, ConfirmedBlankReference: true, ConfirmedReusableLayout: true})
		result <- err
	}()
	// 删除事务先持有资产锁；等待收录确实在同一行锁上阻塞，再提交删除状态。
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err = s.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND application_name=current_setting('application_name') AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%FOR SHARE OF p,f%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("publication did not wait on the source asset lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("published deleting reference: %v", err)
	}
	var count int
	if err = s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM answer_sheet_template_library`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("race left a library version: %d %v", count, err)
	}
}

func TestTemplateLibraryPostgresPublicationWinningRaceBlocksSourceDeletion(t *testing.T) {
	s, scope, source, _, fileID := newTemplateLibraryPostgresFixture(t)
	barrier := "publish-barrier/" + source.ID
	// 测试触发器只暂停最终库写入；此时正式代码已经持有来源共享锁和对象事务锁。
	ddl := `CREATE FUNCTION pause_library_publication() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(hashtextextended('` + barrier + `',0)); RETURN NEW; END; $$; CREATE TRIGGER pause_library_publication BEFORE INSERT ON answer_sheet_template_library FOR EACH ROW EXECUTE FUNCTION pause_library_publication()`
	if _, err := s.db.ExecContext(t.Context(), ddl); err != nil {
		t.Fatal(err)
	}
	blocker, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err = blocker.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, barrier); err != nil {
		t.Fatal(err)
	}
	published := make(chan error, 1)
	go func() {
		_, err := s.PublishTemplateToLibrary(t.Context(), scope, source.ID, PublishTemplateToLibraryInput{Name: "card", ExpectedRevision: 3, ConfirmedBlankReference: true, ConfirmedReusableLayout: true})
		published <- err
	}()
	waitLibraryPostgresLock(t, s, "%INSERT INTO answer_sheet_template_library(id%")
	deleted := make(chan error, 1)
	go func() {
		_, err := s.db.ExecContext(t.Context(), `UPDATE file_asset SET lifecycle_status='pending_delete' WHERE id=$1`, fileID)
		deleted <- err
	}()
	waitLibraryPostgresLock(t, s, "%UPDATE file_asset SET lifecycle_status=%")
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-published; err != nil {
		t.Fatalf("publication lost its pinned source: %v", err)
	}
	if err = <-deleted; err == nil {
		t.Fatal("concurrent deletion removed a published reference")
	}
	var status string
	if err = s.db.QueryRowContext(t.Context(), `SELECT lifecycle_status FROM file_asset WHERE id=$1`, fileID).Scan(&status); err != nil || status != "active" {
		t.Fatalf("failed deletion changed lifecycle: %s %v", status, err)
	}
}

func waitLibraryPostgresLock(t *testing.T, s *PostgresStore, pattern string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err := s.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND application_name=current_setting('application_name') AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected blocked database operation: %s", pattern)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
